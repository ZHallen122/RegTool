package history

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned when the requested snapshot does not exist.
var ErrNotFound = errors.New("history: snapshot not found")

// errIsDir reports an attempt to snapshot a directory instead of a file.
var errIsDir = errors.New("is a directory, not a file")

const (
	// manifestName is the per-snapshot metadata file. It is written last so a
	// snapshot directory without one is treated as incomplete.
	manifestName = "manifest.json"
	// filesDirName holds the captured file contents, one file per manifest
	// entry, named after the entry's index.
	filesDirName = "files"
	// idTimeLayout is fixed width, so IDs sort chronologically as strings.
	idTimeLayout = "20060102T150405.000Z"

	dirPerm      = fs.FileMode(0o755)
	manifestPerm = fs.FileMode(0o644)

	// idAttempts bounds the retries used to claim an unused snapshot ID.
	idAttempts = 16
)

// FileEntry describes one file captured in a snapshot.
//
// Existed reports whether the file was present when the snapshot was taken. A
// missing file still gets an entry so that a restore can delete whatever was
// created in its place.
type FileEntry struct {
	Path    string      `json:"path"`
	Existed bool        `json:"existed"`
	Mode    fs.FileMode `json:"mode"`
	Size    int64       `json:"size"`
}

// Snapshot is the set of files captured by a single call to [Store.Save].
type Snapshot struct {
	ID        string      `json:"id"`
	CreatedAt time.Time   `json:"createdAt"`
	Note      string      `json:"note"`
	Files     []FileEntry `json:"files"`
}

// Store keeps snapshots under a single directory. The zero value is not
// usable; construct one with [New] or [Default].
type Store struct {
	dir string

	mu   sync.Mutex
	last time.Time // timestamp of the most recent Save, for monotonic IDs
}

// New returns a Store rooted at dir. The directory is created on first write.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Default returns a Store under the user's configuration directory, at
// regtool/history.
func Default() (*Store, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("history: locate user config dir: %w", err)
	}
	return New(filepath.Join(cfg, "regtool", "history")), nil
}

// Dir reports the directory the store writes to.
func (s *Store) Dir() string { return s.dir }

// Save captures the current contents of paths and returns the resulting
// snapshot. Paths that do not exist are recorded as missing rather than
// treated as an error. Relative paths are resolved against the working
// directory and stored absolute; duplicates are captured once.
//
// The manifest is written after every file has been copied, so an interrupted
// save leaves a directory that [Store.List] skips.
func (s *Store) Save(ctx context.Context, note string, paths []string) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("history: save: %w", err)
	}

	abs, err := absUnique(paths)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return nil, fmt.Errorf("history: create store dir %s: %w", s.dir, err)
	}

	now := s.nextTimestamp()
	id, snapDir, err := s.claimID(now)
	if err != nil {
		return nil, err
	}

	snap, err := s.fill(ctx, id, now, note, snapDir, abs)
	if err != nil {
		// A failed save must not leave a directory behind that a later prune
		// would have to reason about.
		_ = os.RemoveAll(snapDir)
		return nil, err
	}
	return snap, nil
}

// fill copies every file into snapDir and writes the manifest last.
func (s *Store) fill(ctx context.Context, id string, now time.Time, note, snapDir string, abs []string) (*Snapshot, error) {
	filesDir := filepath.Join(snapDir, filesDirName)
	if err := os.MkdirAll(filesDir, dirPerm); err != nil {
		return nil, fmt.Errorf("history: create snapshot files dir %s: %w", filesDir, err)
	}

	snap := &Snapshot{
		ID:        id,
		CreatedAt: now,
		Note:      note,
		Files:     make([]FileEntry, 0, len(abs)),
	}

	for i, path := range abs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("history: save %s: %w", id, err)
		}
		entry, err := captureFile(path, filepath.Join(filesDir, strconv.Itoa(i)))
		if err != nil {
			return nil, err
		}
		snap.Files = append(snap.Files, entry)
	}

	manifest, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("history: encode manifest for %s: %w", id, err)
	}
	if err := atomicWrite(filepath.Join(snapDir, manifestName), bytes.NewReader(manifest), manifestPerm); err != nil {
		return nil, fmt.Errorf("history: write manifest for %s: %w", id, err)
	}
	return snap, nil
}

// captureFile copies src into dst and describes what it found. A missing src
// yields an entry with Existed false and nothing is copied.
func captureFile(src, dst string) (FileEntry, error) {
	entry := FileEntry{Path: src}

	in, err := os.Open(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return entry, nil
		}
		return entry, fmt.Errorf("history: open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return entry, fmt.Errorf("history: stat %s: %w", src, err)
	}
	if info.IsDir() {
		return entry, fmt.Errorf("history: %s: %w", src, errIsDir)
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, manifestPerm)
	if err != nil {
		return entry, fmt.Errorf("history: create snapshot copy %s: %w", dst, err)
	}
	n, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return entry, fmt.Errorf("history: copy %s to %s: %w", src, dst, copyErr)
	}
	if closeErr != nil {
		return entry, fmt.Errorf("history: close snapshot copy %s: %w", dst, closeErr)
	}

	entry.Existed = true
	entry.Mode = info.Mode().Perm()
	entry.Size = n
	return entry, nil
}

// nextTimestamp returns a UTC time that is strictly later, at the ID layout's
// millisecond resolution, than the one handed to the previous Save on this
// Store. Two saves in the same millisecond would otherwise get IDs whose
// order depends only on their random suffix.
func (s *Store) nextTimestamp() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if !now.After(s.last) {
		now = s.last.Add(time.Millisecond)
	}
	s.last = now
	return now
}

// claimID reserves an unused snapshot directory and returns its ID and path.
// Creating the directory exclusively is what keeps concurrent saves apart.
func (s *Store) claimID(now time.Time) (string, string, error) {
	stamp := now.Format(idTimeLayout)
	for range idAttempts {
		suffix, err := randomSuffix()
		if err != nil {
			return "", "", err
		}
		id := stamp + "-" + suffix
		dir := filepath.Join(s.dir, id)
		switch err := os.Mkdir(dir, dirPerm); {
		case err == nil:
			return id, dir, nil
		case errors.Is(err, fs.ErrExist):
			continue
		default:
			return "", "", fmt.Errorf("history: create snapshot dir %s: %w", dir, err)
		}
	}
	return "", "", fmt.Errorf("history: allocate snapshot id after %d attempts", idAttempts)
}

func randomSuffix() (string, error) {
	var buf [3]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("history: read random bytes: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// List returns every complete snapshot, newest first. Directories without a
// manifest are skipped: they are saves that did not finish.
func (s *Store) List(ctx context.Context) ([]Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("history: list: %w", err)
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: read store dir %s: %w", s.dir, err)
	}

	snaps := make([]Snapshot, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		snap, err := s.read(e.Name())
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		snaps = append(snaps, *snap)
	}

	// IDs begin with a fixed-width UTC timestamp, so this is chronological.
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].ID > snaps[j].ID })
	return snaps, nil
}

// Get returns the snapshot with the given ID, or [ErrNotFound].
func (s *Store) Get(id string) (*Snapshot, error) {
	if !validID(id) {
		return nil, fmt.Errorf("history: %q: %w", id, ErrNotFound)
	}
	return s.read(id)
}

// read loads one manifest. A missing manifest reports ErrNotFound so callers
// cannot tell an unknown ID from an unfinished save; neither is restorable.
func (s *Store) read(id string) (*Snapshot, error) {
	path := filepath.Join(s.dir, id, manifestName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("history: %q: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("history: read manifest %s: %w", path, err)
	}

	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("history: decode manifest %s: %w", path, err)
	}
	snap.ID = id
	return &snap, nil
}

// Restore writes every file in the snapshot back to its recorded path and
// deletes the files that did not exist when the snapshot was taken.
//
// Before touching anything it saves a snapshot of the current state, noted
// "before restore <id>", so the restore can itself be undone. Each file is
// written atomically, missing parent directories are recreated, and the
// recorded mode is applied.
func (s *Store) Restore(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("history: restore: %w", err)
	}

	snap, err := s.Get(id)
	if err != nil {
		return err
	}

	paths := make([]string, 0, len(snap.Files))
	for _, f := range snap.Files {
		paths = append(paths, f.Path)
	}
	if _, err := s.Save(ctx, "before restore "+snap.ID, paths); err != nil {
		return fmt.Errorf("history: snapshot before restoring %s: %w", snap.ID, err)
	}

	filesDir := filepath.Join(s.dir, snap.ID, filesDirName)
	for i, f := range snap.Files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("history: restore %s: %w", snap.ID, err)
		}
		if err := restoreFile(f, filepath.Join(filesDir, strconv.Itoa(i))); err != nil {
			return err
		}
	}
	return nil
}

// restoreFile puts one entry back, or removes the path if the file did not
// exist when the snapshot was taken.
func restoreFile(f FileEntry, stored string) error {
	if !f.Existed {
		if err := os.Remove(f.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("history: remove %s: %w", f.Path, err)
		}
		return nil
	}

	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("history: create parent dir %s: %w", dir, err)
	}

	in, err := os.Open(stored)
	if err != nil {
		return fmt.Errorf("history: open snapshot copy %s: %w", stored, err)
	}
	defer func() { _ = in.Close() }()

	mode := f.Mode.Perm()
	if mode == 0 {
		mode = manifestPerm
	}
	if err := atomicWrite(f.Path, in, mode); err != nil {
		return fmt.Errorf("history: restore %s: %w", f.Path, err)
	}
	return nil
}

// Prune deletes all but the newest keep snapshots. keep of zero removes every
// snapshot. Unfinished saves are left alone: they may still be in flight.
func (s *Store) Prune(ctx context.Context, keep int) error {
	if keep < 0 {
		return fmt.Errorf("history: prune: keep must not be negative, got %d", keep)
	}

	snaps, err := s.List(ctx)
	if err != nil {
		return err
	}
	if len(snaps) <= keep {
		return nil
	}

	for _, snap := range snaps[keep:] {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("history: prune: %w", err)
		}
		dir := filepath.Join(s.dir, snap.ID)
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("history: remove snapshot %s: %w", dir, err)
		}
	}
	return nil
}

// absUnique resolves paths to absolute form, dropping repeats while keeping
// the order they were given in.
func absUnique(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("history: resolve %s: %w", p, err)
		}
		abs = filepath.Clean(abs)
		if _, dup := seen[abs]; dup {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out, nil
}

// validID rejects IDs that would escape the store directory.
func validID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.ContainsAny(id, `/\`) {
		return false
	}
	return filepath.Base(id) == id
}

// atomicWrite writes r to path through a temporary file in the same directory
// followed by a rename, so readers never observe a partial file.
func atomicWrite(path string, r io.Reader, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".regtool-history-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		// Windows refuses to rename over some existing files; drop the target
		// and try once more.
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			return fmt.Errorf("replace %s: %w", path, err)
		}
		if err := os.Rename(tmpName, path); err != nil {
			return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
		}
	}
	tmpName = "" // Renamed into place; nothing left to clean up.
	return nil
}
