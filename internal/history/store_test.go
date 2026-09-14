package history

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
)

// writeFile creates path with the given contents, making parent dirs as needed.
func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readFile returns the contents of path, failing the test if it is unreadable.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// newStore returns a store rooted in a fresh temp dir, plus that dir.
func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	return New(filepath.Join(root, "history")), root
}

func TestNewAndDefault(t *testing.T) {
	t.Parallel()

	if got := New(filepath.Join("a", "b")).Dir(); got != filepath.Join("a", "b") {
		t.Errorf("Dir() = %q, want %q", got, filepath.Join("a", "b"))
	}

	s, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}
	if s == nil || s.Dir() == "" {
		t.Fatalf("Default() = %v, want a store with a directory", s)
	}
	want := filepath.Join("regtool", "history")
	if !strings.HasSuffix(s.Dir(), want) {
		t.Errorf("Default().Dir() = %q, want suffix %q", s.Dir(), want)
	}
}

func TestSaveRecordsFiles(t *testing.T) {
	t.Parallel()

	s, root := newStore(t)
	present := filepath.Join(root, "cfg", "present.conf")
	writeFile(t, present, "registry=old\n")
	absent := filepath.Join(root, "cfg", "absent.conf")

	tests := []struct {
		name    string
		paths   []string
		want    []FileEntry
		wantLen int
	}{
		{
			name:  "existing and missing",
			paths: []string{present, absent},
			want: []FileEntry{
				{Path: present, Existed: true, Size: int64(len("registry=old\n"))},
				{Path: absent, Existed: false, Size: 0},
			},
		},
		{
			name:  "duplicates collapse",
			paths: []string{present, present},
			want: []FileEntry{
				{Path: present, Existed: true, Size: int64(len("registry=old\n"))},
			},
		},
		{
			name:  "no paths",
			paths: nil,
			want:  []FileEntry{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := s.Save(context.Background(), tc.name, tc.paths)
			if err != nil {
				t.Fatalf("Save() error: %v", err)
			}
			if snap.Note != tc.name {
				t.Errorf("Note = %q, want %q", snap.Note, tc.name)
			}
			if snap.CreatedAt.IsZero() {
				t.Error("CreatedAt is zero")
			}
			if len(snap.Files) != len(tc.want) {
				t.Fatalf("len(Files) = %d, want %d", len(snap.Files), len(tc.want))
			}
			for i, want := range tc.want {
				got := snap.Files[i]
				if got.Path != want.Path || got.Existed != want.Existed || got.Size != want.Size {
					t.Errorf("Files[%d] = %+v, want path=%q existed=%v size=%d",
						i, got, want.Path, want.Existed, want.Size)
				}
			}

			// The manifest must be readable back through Get.
			round, err := s.Get(snap.ID)
			if err != nil {
				t.Fatalf("Get(%q) error: %v", snap.ID, err)
			}
			if len(round.Files) != len(snap.Files) || round.Note != snap.Note {
				t.Errorf("Get() = %+v, want equivalent to %+v", round, snap)
			}
		})
	}
}

func TestSaveStoresAbsolutePaths(t *testing.T) {
	s, root := newStore(t)

	t.Chdir(root)
	writeFile(t, filepath.Join(root, "rel.conf"), "x")

	snap, err := s.Save(context.Background(), "relative", []string{"rel.conf"})
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if !filepath.IsAbs(snap.Files[0].Path) {
		t.Errorf("stored path %q is not absolute", snap.Files[0].Path)
	}
}

func TestSaveRejectsDirectory(t *testing.T) {
	t.Parallel()

	s, root := newStore(t)
	dir := filepath.Join(root, "adir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := s.Save(context.Background(), "dir", []string{dir}); !errors.Is(err, errIsDir) {
		t.Fatalf("Save(dir) error = %v, want errIsDir", err)
	}

	// The failed save must not leave a snapshot directory behind.
	snaps, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("List() = %d snapshots, want 0 after a failed save", len(snaps))
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("store dir holds %d entries, want 0", len(entries))
	}
}

func TestSaveCanceledContext(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.Save(ctx, "canceled", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save() error = %v, want context.Canceled", err)
	}
	if _, err := s.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("List() error = %v, want context.Canceled", err)
	}
	if err := s.Restore(ctx, "whatever"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Restore() error = %v, want context.Canceled", err)
	}
	if err := s.Prune(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Prune() error = %v, want context.Canceled", err)
	}
}

func TestListOrderingAndEmpty(t *testing.T) {
	t.Parallel()

	s, root := newStore(t)
	ctx := context.Background()

	// A store whose directory does not exist yet lists nothing.
	snaps, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List() on empty store error: %v", err)
	}
	if len(snaps) != 0 {
		t.Fatalf("List() = %d, want 0", len(snaps))
	}

	target := filepath.Join(root, "f.conf")
	writeFile(t, target, "v0")

	var ids []string
	for _, note := range []string{"first", "second", "third"} {
		snap, err := s.Save(ctx, note, []string{target})
		if err != nil {
			t.Fatalf("Save(%q) error: %v", note, err)
		}
		ids = append(ids, snap.ID)
	}

	snaps, err = s.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(snaps) != len(ids) {
		t.Fatalf("List() = %d snapshots, want %d", len(snaps), len(ids))
	}

	// Newest first: the listing is the reverse of the sorted IDs.
	wantOrder := append([]string(nil), ids...)
	sort.Sort(sort.Reverse(sort.StringSlice(wantOrder)))
	for i, snap := range snaps {
		if snap.ID != wantOrder[i] {
			t.Errorf("List()[%d].ID = %q, want %q", i, snap.ID, wantOrder[i])
		}
	}
	if snaps[0].Note != "third" {
		t.Errorf("newest note = %q, want %q", snaps[0].Note, "third")
	}
}

func TestListIgnoresIncompleteSnapshots(t *testing.T) {
	t.Parallel()

	s, root := newStore(t)
	ctx := context.Background()
	target := filepath.Join(root, "f.conf")
	writeFile(t, target, "v0")

	good, err := s.Save(ctx, "good", []string{target})
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// A directory with no manifest: a save that died halfway through.
	orphan := filepath.Join(s.Dir(), "20990101T000000.000Z-abcdef")
	if err := os.MkdirAll(filepath.Join(orphan, filesDirName), 0o755); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}
	// A stray regular file in the store dir must not be mistaken for one.
	writeFile(t, filepath.Join(s.Dir(), "stray.txt"), "noise")

	snaps, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(snaps) != 1 || snaps[0].ID != good.ID {
		t.Fatalf("List() = %+v, want only %q", snaps, good.ID)
	}
}

func TestListMalformedManifest(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	broken := filepath.Join(s.Dir(), "20260101T000000.000Z-aaaaaa")
	writeFile(t, filepath.Join(broken, manifestName), "{not json")

	if _, err := s.List(context.Background()); err == nil {
		t.Fatal("List() error = nil, want a decode error")
	}
}

func TestGetUnknown(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	tests := []struct {
		name string
		id   string
	}{
		{"missing", "20260101T000000.000Z-aaaaaa"},
		{"empty", ""},
		{"dot", "."},
		{"parent", ".."},
		{"traversal slash", "../escape"},
		{"traversal backslash", `..\escape`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Get(tc.id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get(%q) error = %v, want ErrNotFound", tc.id, err)
			}
		})
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	t.Parallel()

	s, root := newStore(t)
	ctx := context.Background()

	modified := filepath.Join(root, "npmrc")
	created := filepath.Join(root, "created.conf")
	nested := filepath.Join(root, "deep", "nest", "pip.ini")

	writeFile(t, modified, "registry=https://registry.npmjs.org/\n")
	writeFile(t, nested, "index-url=https://pypi.org/simple\n")

	snap, err := s.Save(ctx, "before change", []string{modified, created, nested})
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Simulate RegTool changing things: edit one file, create another that did
	// not exist, and blow away a whole parent directory.
	writeFile(t, modified, "registry=https://mirror.example/\n")
	writeFile(t, created, "created by regtool\n")
	if err := os.RemoveAll(filepath.Join(root, "deep")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	if err := s.Restore(ctx, snap.ID); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	if got, want := readFile(t, modified), "registry=https://registry.npmjs.org/\n"; got != want {
		t.Errorf("modified file = %q, want %q", got, want)
	}
	if _, err := os.Stat(created); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(created) error = %v, want fs.ErrNotExist", err)
	}
	if got, want := readFile(t, nested), "index-url=https://pypi.org/simple\n"; got != want {
		t.Errorf("nested file = %q, want %q", got, want)
	}
}

func TestRestorePreservesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not carry Unix permission bits")
	}
	t.Parallel()

	s, root := newStore(t)
	ctx := context.Background()
	target := filepath.Join(root, "secret.conf")
	writeFile(t, target, "token=abc\n")
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	snap, err := s.Save(ctx, "modes", []string{target})
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if got := snap.Files[0].Mode.Perm(); got != 0o600 {
		t.Fatalf("recorded mode = %v, want 0600", got)
	}

	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := s.Restore(ctx, snap.ID); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("restored mode = %v, want 0600", got)
	}
}

func TestRestoreTakesItsOwnSnapshot(t *testing.T) {
	t.Parallel()

	s, root := newStore(t)
	ctx := context.Background()
	target := filepath.Join(root, "gemrc")
	writeFile(t, target, "v1")

	first, err := s.Save(ctx, "v1", []string{target})
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	writeFile(t, target, "v2")
	if err := s.Restore(ctx, first.ID); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}

	snaps, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("List() = %d snapshots, want 2", len(snaps))
	}
	pre := snaps[0]
	if want := "before restore " + first.ID; pre.Note != want {
		t.Errorf("pre-restore note = %q, want %q", pre.Note, want)
	}

	// The undo is itself undoable: restoring the pre-restore snapshot brings
	// back the state from just before the first restore.
	if got := readFile(t, target); got != "v1" {
		t.Fatalf("after restore = %q, want %q", got, "v1")
	}
	if err := s.Restore(ctx, pre.ID); err != nil {
		t.Fatalf("Restore(pre) error: %v", err)
	}
	if got := readFile(t, target); got != "v2" {
		t.Errorf("after undoing the restore = %q, want %q", got, "v2")
	}
}

func TestRestoreUnknown(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	err := s.Restore(context.Background(), "20260101T000000.000Z-aaaaaa")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Restore() error = %v, want ErrNotFound", err)
	}
}

func TestRestoreMissingStoredCopy(t *testing.T) {
	t.Parallel()

	s, root := newStore(t)
	ctx := context.Background()
	target := filepath.Join(root, "npmrc")
	writeFile(t, target, "v1")

	snap, err := s.Save(ctx, "v1", []string{target})
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if err := os.Remove(filepath.Join(s.Dir(), snap.ID, filesDirName, "0")); err != nil {
		t.Fatalf("remove stored copy: %v", err)
	}

	if err := s.Restore(ctx, snap.ID); err == nil {
		t.Fatal("Restore() error = nil, want an open error for the missing copy")
	}
}

func TestPrune(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		saves int
		keep  int
		want  int
	}{
		{name: "keep fewer than present", saves: 5, keep: 2, want: 2},
		{name: "keep more than present", saves: 2, keep: 5, want: 2},
		{name: "keep exactly present", saves: 3, keep: 3, want: 3},
		{name: "keep none", saves: 3, keep: 0, want: 0},
		{name: "empty store", saves: 0, keep: 3, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, root := newStore(t)
			ctx := context.Background()
			target := filepath.Join(root, "f.conf")
			writeFile(t, target, "v")

			var ids []string
			for range tc.saves {
				snap, err := s.Save(ctx, "s", []string{target})
				if err != nil {
					t.Fatalf("Save() error: %v", err)
				}
				ids = append(ids, snap.ID)
			}

			if err := s.Prune(ctx, tc.keep); err != nil {
				t.Fatalf("Prune(%d) error: %v", tc.keep, err)
			}

			snaps, err := s.List(ctx)
			if err != nil {
				t.Fatalf("List() error: %v", err)
			}
			if len(snaps) != tc.want {
				t.Fatalf("after Prune(%d): %d snapshots, want %d", tc.keep, len(snaps), tc.want)
			}

			// Whatever survived must be the newest ones, and their directories
			// must be gone from disk for the rest.
			sort.Sort(sort.Reverse(sort.StringSlice(ids)))
			for i, snap := range snaps {
				if snap.ID != ids[i] {
					t.Errorf("survivor[%d] = %q, want %q", i, snap.ID, ids[i])
				}
			}
			for _, id := range ids[len(snaps):] {
				if _, err := os.Stat(filepath.Join(s.Dir(), id)); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("pruned snapshot %q still on disk (err = %v)", id, err)
				}
			}
		})
	}
}

func TestPruneNegativeKeep(t *testing.T) {
	t.Parallel()

	s, _ := newStore(t)
	if err := s.Prune(context.Background(), -1); err == nil {
		t.Fatal("Prune(-1) error = nil, want an error")
	}
}

func TestConcurrentSavesProduceDistinctIDs(t *testing.T) {
	t.Parallel()

	s, root := newStore(t)
	ctx := context.Background()
	target := filepath.Join(root, "f.conf")
	writeFile(t, target, "shared")

	const n = 24
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		ids = make(map[string]struct{}, n)
	)
	errs := make(chan error, n)

	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			snap, err := s.Save(ctx, "concurrent "+strconv.Itoa(i), []string{target})
			if err != nil {
				errs <- err
				return
			}
			mu.Lock()
			ids[snap.ID] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent Save() error: %v", err)
	}
	if len(ids) != n {
		t.Fatalf("got %d distinct IDs from %d saves, want %d", len(ids), n, n)
	}

	snaps, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(snaps) != n {
		t.Fatalf("List() = %d snapshots, want %d", len(snaps), n)
	}
}

func TestAtomicWriteReplacesExisting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	writeFile(t, path, "old contents that are longer")

	if err := atomicWrite(path, strings.NewReader("new"), 0o644); err != nil {
		t.Fatalf("atomicWrite() error: %v", err)
	}
	if got := readFile(t, path); got != "new" {
		t.Errorf("contents = %q, want %q", got, "new")
	}

	// No temporary files may be left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("dir holds %d entries, want 1", len(entries))
	}
}

func TestAtomicWriteMissingDir(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nope", "target")
	if err := atomicWrite(path, strings.NewReader("x"), 0o644); err == nil {
		t.Fatal("atomicWrite() error = nil, want a failure creating the temp file")
	}
}

func TestAtomicWriteCopyError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	boom := errors.New("boom")

	if err := atomicWrite(path, iotest.ErrReader(boom), 0o644); !errors.Is(err, boom) {
		t.Fatalf("atomicWrite() error = %v, want %v", err, boom)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("dir holds %d entries, want the temp file cleaned up", len(entries))
	}
}

// A rename that cannot replace the target exercises the remove-then-retry path
// that Windows needs. An empty directory can be removed, so the retry wins; a
// non-empty one cannot, so the write fails.
func TestAtomicWriteRenameFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		nonEmpty bool
		wantErr  bool
	}{
		{name: "removable target", nonEmpty: false, wantErr: false},
		{name: "unremovable target", nonEmpty: true, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "target")
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if tc.nonEmpty {
				writeFile(t, filepath.Join(path, "child"), "blocker")
			}

			err := atomicWrite(path, strings.NewReader("new"), 0o644)
			if tc.wantErr {
				if err == nil {
					t.Fatal("atomicWrite() error = nil, want a failure replacing the target")
				}
				return
			}
			if err != nil {
				t.Fatalf("atomicWrite() error: %v", err)
			}
			if got := readFile(t, path); got != "new" {
				t.Errorf("contents = %q, want %q", got, "new")
			}
		})
	}
}

func TestDefaultWithoutConfigDir(t *testing.T) {
	// os.UserConfigDir fails when the variable it reads is empty.
	if runtime.GOOS == "windows" {
		t.Setenv("AppData", "")
	} else {
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "")
	}

	if _, err := Default(); err == nil {
		t.Fatal("Default() error = nil, want a failure locating the config dir")
	}
}

func TestSaveStoreDirIsAFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	blocker := filepath.Join(root, "history")
	writeFile(t, blocker, "not a directory")

	s := New(blocker)
	if _, err := s.Save(context.Background(), "blocked", nil); err == nil {
		t.Fatal("Save() error = nil, want a failure creating the store dir")
	}
}

func TestRestoreParentIsAFile(t *testing.T) {
	t.Parallel()

	s, root := newStore(t)
	ctx := context.Background()
	parent := filepath.Join(root, "cfg")
	target := filepath.Join(parent, "pip.ini")
	writeFile(t, target, "v1")

	snap, err := s.Save(ctx, "v1", []string{target})
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Replace the parent directory with a regular file, so recreating it fails.
	if err := os.RemoveAll(parent); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	writeFile(t, parent, "now a file")

	if err := s.Restore(ctx, snap.ID); err == nil {
		t.Fatal("Restore() error = nil, want a failure recreating the parent dir")
	}
}

func TestRestoreDeleteBlocked(t *testing.T) {
	t.Parallel()

	s, root := newStore(t)
	ctx := context.Background()
	absent := filepath.Join(root, "created.conf")

	snap, err := s.Save(ctx, "absent", []string{absent})
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if snap.Files[0].Existed {
		t.Fatalf("Files[0].Existed = true, want false")
	}

	// A non-empty directory now occupies the path the restore must delete.
	writeFile(t, filepath.Join(absent, "child"), "blocker")

	if err := s.Restore(ctx, snap.ID); err == nil {
		t.Fatal("Restore() error = nil, want a failure removing the path")
	}
}

func TestValidID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id   string
		want bool
	}{
		{"20260101T000000.000Z-aaaaaa", true},
		{"", false},
		{".", false},
		{"..", false},
		{"a/b", false},
		{`a\b`, false},
	}
	for _, tc := range tests {
		if got := validID(tc.id); got != tc.want {
			t.Errorf("validID(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}
