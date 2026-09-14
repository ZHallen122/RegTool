// Package history stores point-in-time snapshots of configuration files so a
// change made by RegTool can be rolled back later.
//
// A Store owns a single directory. Every call to [Store.Save] copies the
// current bytes of the requested files into a new snapshot directory named by
// a UTC timestamp, together with a manifest describing what was captured:
//
//	<dir>/20260914T041530.123Z-3f9a1c/
//	    manifest.json
//	    files/0
//	    files/1
//
// The manifest is written last, so a crash midway through a save leaves a
// directory that [Store.List] ignores rather than a half-restorable snapshot.
// Files that did not exist when the snapshot was taken are recorded as such,
// which lets [Store.Restore] delete them again instead of leaving behind a
// file RegTool created.
//
// Snapshot IDs are fixed width and start with a UTC timestamp, so sorting them
// lexicographically sorts them chronologically. [Store.List] returns the newest
// snapshot first and [Store.Prune] keeps the newest N.
//
// [Store.Restore] takes its own snapshot before writing anything back, so an
// undo can itself be undone. Every file is written atomically through a
// temporary file in the destination directory followed by a rename, missing
// parent directories are recreated, and the recorded file mode is preserved.
//
// Paths are stored absolute and all path handling goes through path/filepath,
// so the store works on Windows as well as on Unix. A Store holds no global
// state; several may coexist in one process.
package history
