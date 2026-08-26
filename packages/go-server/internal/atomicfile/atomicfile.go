// Package atomicfile is the one write-a-file-atomically helper for go-server.
//
// It exists because there were two of them, and each one had the bug the other
// one fixed:
//
//   - internal/store's copy called tmp.Sync() but never created the parent
//     directory, so the first write into a not-yet-existing store directory
//     failed with ENOENT.
//   - internal/handlers/tts.go's copy called os.MkdirAll but never called
//     Sync, so the rename could become visible with the file's contents still
//     only in the page cache. A crash between the two left a correctly-named
//     file holding nothing — the exact failure "atomic write" is supposed to
//     rule out.
//
// Write is the union: create the directory, write, fsync the data, set the
// mode, then rename. Neither divergence can come back, because there is now
// only one implementation to diverge from.
//
// # What this deliberately does NOT do
//
// It does not fsync the parent directory after the rename. That would make the
// rename itself durable across a power loss, and it is the one remaining gap
// between this helper and a fully crash-safe write. It is left out on purpose:
// neither original copy had it, on darwin File.Sync is F_FULLFSYNC (a real
// device-level flush, not a cheap syscall), and every settings/draft/message
// write in the server goes through here. Paying a second full flush per write
// is a performance decision, not a cleanup, so it is called out here rather
// than smuggled in — if a lost rename is ever observed, this is the fix.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write replaces the file at path with data, atomically: readers see either the
// old contents or the new ones, never a partial write. perm is the mode of the
// resulting file. Parent directories are created if missing.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	// Before the rename, not after: a rename that lands ahead of the data is
	// how an "atomic" write ends up publishing an empty file.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	// Chmod the handle rather than the path — the path is a temp name that
	// nothing else should be touching, but going through the fd means there is
	// no window in which the name could be swapped for something else.
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
