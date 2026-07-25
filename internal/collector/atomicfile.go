package collector

import (
	"os"
	"path/filepath"
)

// writeAndSync writes data to f and flushes it to stable storage. It is a
// variable purely so tests can inject the torn-write / failed-fsync outcome a
// real crash produces, without killing the test process. Production code never
// reassigns it.
var writeAndSync = func(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// writeFileAtomic replaces path with data atomically, without ever following a
// symlink planted by another local principal.
//
// The replacement is staged with os.CreateTemp in path's OWN directory. That
// opens with O_CREATE|O_EXCL, so it refuses to follow — or clobber — anything
// already sitting at the name it picks, and the name is randomized per call.
// The historical scheme (a fixed "<path>.tmp" sibling opened O_CREATE|O_TRUNC)
// had neither property: on a shared or over-permissive checkpoint directory an
// attacker could pre-place a symlink at that predictable path and redirect the
// write, and two concurrent savers would fight over one temp file
// (security:SEC-08, #471).
//
// The temp file deliberately stays in the destination directory rather than
// os.TempDir, so the rename never crosses a filesystem and stays atomic. It is
// fsynced before the rename and the parent directory is fsynced after it, so a
// crash leaves either the previous complete file or the new complete one, never
// a partial one. Every failure path removes exactly the temp file this call
// created and nothing else; after a successful rename there is nothing to
// remove.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	closed, renamed := false, false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()

	// os.CreateTemp creates the file 0600 *before* umask, so a strict umask can
	// still narrow it (e.g. 0177 would leave 0400). Restate the intended mode so
	// the file always lands at perm.
	if err := f.Chmod(perm); err != nil {
		return err
	}
	if err := writeAndSync(f, data); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	renamed = true
	syncDir(dir)
	return nil
}

// syncDir fsyncs a directory so a rename into it survives a crash or power
// loss. Best effort: a filesystem that refuses to open or sync a directory is
// not a reason to fail a write that already landed.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
