package backend

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// defaultFileMode is the mode given to a configuration file this package
// creates. An existing file keeps the mode it already has.
const defaultFileMode fs.FileMode = 0o644

// Plan is a computed, not yet written, change to one configuration file.
// The zero value is not useful; plans come from [Backend.Plan].
type Plan struct {
	// Backend is the name of the backend that produced the plan.
	Backend string
	// Path is the absolute path of the file that would be written.
	Path string
	// Existed reports whether Path existed when the plan was computed.
	Existed bool
	// Before is the current content of the file, nil when it does not exist.
	Before []byte
	// After is the content the file would have once the plan is applied.
	After []byte
	// From is the registry URL currently configured, "" when unset.
	From string
	// To is the registry URL the plan configures.
	To string
}

// IsNoop reports whether applying the plan would leave the file unchanged.
func (p *Plan) IsNoop() bool {
	return p.Existed && bytes.Equal(p.Before, p.After)
}

// Diff renders the change as a unified diff with three lines of context. It
// returns "" for a no-op plan.
func (p *Plan) Diff() string {
	if p.IsNoop() {
		return ""
	}
	from, to := p.Path, p.Path
	if !p.Existed {
		from = "/dev/null"
	}
	return unifiedDiff(from, to, splitDiffLines(p.Before), splitDiffLines(p.After))
}

// Apply writes the planned content to disk atomically: the new bytes go to a
// temporary file in the target directory which is then renamed over the target,
// so a concurrent reader never observes a half-written file. Missing parent
// directories are created. A no-op plan is not written at all.
func (p *Plan) Apply() error {
	if p.IsNoop() {
		return nil
	}
	if p.Path == "" {
		return fmt.Errorf("backend %s: empty config path", p.Backend)
	}

	dir := filepath.Dir(p.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	mode := defaultFileMode
	if info, err := os.Stat(p.Path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory, not a config file", p.Path)
		}
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".regtool-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup: after a successful rename the temp file is gone and
	// the Remove simply fails.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(p.After); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, p.Path); err != nil {
		// Windows refuses to rename over some existing files; drop the target
		// and retry once.
		if rmErr := os.Remove(p.Path); rmErr != nil {
			return fmt.Errorf("replace %s: %w", p.Path, err)
		}
		if err := os.Rename(tmpName, p.Path); err != nil {
			return fmt.Errorf("replace %s: %w", p.Path, err)
		}
	}
	return nil
}

// splitDiffLines splits content into lines without their terminators. A file
// that does not end in a newline still yields its trailing fragment.
func splitDiffLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// unifiedDiff renders a unified diff of a and b with three lines of context.
func unifiedDiff(fromFile, toFile string, a, b []string) string {
	const context = 3

	ops := diffOps(a, b)
	if len(ops) == 0 {
		return ""
	}

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n", fromFile)
	fmt.Fprintf(&out, "+++ %s\n", toFile)

	for _, h := range hunks(ops, context) {
		aCount, bCount := 0, 0
		for _, op := range ops[h.start:h.end] {
			switch op.kind {
			case opEqual:
				aCount++
				bCount++
			case opDelete:
				aCount++
			case opInsert:
				bCount++
			}
		}
		first := ops[h.start]
		aStart, bStart := first.aIdx, first.bIdx
		if aCount > 0 {
			aStart++
		}
		if bCount > 0 {
			bStart++
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aCount, bStart, bCount)
		for _, op := range ops[h.start:h.end] {
			switch op.kind {
			case opEqual:
				fmt.Fprintf(&out, " %s\n", op.text)
			case opDelete:
				fmt.Fprintf(&out, "-%s\n", op.text)
			case opInsert:
				fmt.Fprintf(&out, "+%s\n", op.text)
			}
		}
	}
	return out.String()
}

type opKind int

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

// diffOp is one line of the diff together with its position in each side.
// aIdx and bIdx are zero-based indices of the line in a and b respectively.
type diffOp struct {
	kind opKind
	text string
	aIdx int
	bIdx int
}

// diffOps computes a line diff of a and b using a longest-common-subsequence
// table. Configuration files are small, so the quadratic table is fine.
func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// lcs[i][j] is the length of the LCS of a[i:] and b[j:].
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{opEqual, a[i], i, j})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{opDelete, a[i], i, j})
			i++
		default:
			ops = append(ops, diffOp{opInsert, b[j], i, j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{opDelete, a[i], i, j})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{opInsert, b[j], i, j})
	}

	// A diff with no change at all produces no output.
	for _, op := range ops {
		if op.kind != opEqual {
			return ops
		}
	}
	return nil
}

// hunkRange is a half-open range of ops that belongs to one diff hunk.
type hunkRange struct{ start, end int }

// hunks groups the changed ops into hunks, padding each with up to context
// unchanged lines and merging hunks that would overlap.
func hunks(ops []diffOp, context int) []hunkRange {
	var out []hunkRange
	for i := 0; i < len(ops); i++ {
		if ops[i].kind == opEqual {
			continue
		}
		j := i
		for j < len(ops) {
			if ops[j].kind != opEqual {
				j++
				continue
			}
			// Look ahead: keep going if another change is within 2*context.
			k := j
			for k < len(ops) && k-j < 2*context && ops[k].kind == opEqual {
				k++
			}
			if k < len(ops) && k-j < 2*context {
				j = k
				continue
			}
			break
		}
		start := max(i-context, 0)
		end := min(j+context, len(ops))
		if len(out) > 0 && start <= out[len(out)-1].end {
			out[len(out)-1].end = end
		} else {
			out = append(out, hunkRange{start, end})
		}
		i = j
	}
	return out
}
