package collector

import (
	"os"
	"path/filepath"
	"strings"
	"time"
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
	prefix, suffix := stagingNameBounds(path)
	f, err := os.CreateTemp(dir, prefix+"*"+suffix)
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

// stagingNameBounds returns the literal prefix and suffix that os.CreateTemp
// produces around its random component for the staging files writeFileAtomic
// creates for path.
//
// os.CreateTemp substitutes the LAST "*" in the pattern with a non-empty random
// string and copies everything else verbatim, so the pattern prefix+"*"+suffix
// yields exactly prefix+<random>+suffix. writeFileAtomic and
// sweepStaleStagingFiles both derive their names here so the writer and the
// sweeper can never drift apart — the sweep deleting files it did not create,
// or silently matching nothing, would both be caused by that drift.
func stagingNameBounds(path string) (prefix, suffix string) {
	return "." + filepath.Base(path) + ".", ".tmp"
}

// stagingFileMaxAge is how long a staging file must have gone untouched before
// sweepStaleStagingFiles will remove it.
//
// A staging file exists only for the span of one small JSON write, one fsync
// and one rename — microseconds to milliseconds. An hour is therefore several
// orders of magnitude of headroom, which makes it effectively impossible for
// the sweep to delete a CONCURRENT instance's in-flight file. The residual
// case, a second instance stalled mid-save for over an hour, already describes
// a broken process rather than a working one.
//
// An advisory lock on the checkpoint directory was considered and rejected
// (#491): a stale lock left by a hard kill blocks startup outright, which is a
// far worse failure than a leaked few-KiB file, and the Helm chart already pins
// replicaCount: 1 with strategy: Recreate.
const stagingFileMaxAge = time.Hour

// readDirForSweep is os.ReadDir. It is a variable purely so tests can inject
// the unreadable-directory outcome without needing a privileged or
// root-sensitive chmod. Production code never reassigns it.
var readDirForSweep = os.ReadDir

// sweepStaleStagingFiles removes staging files stranded in path's directory by
// a process killed between os.CreateTemp and os.Rename inside writeFileAtomic,
// and reports how many it removed (#491).
//
// Every Go-level error path in writeFileAtomic already removes its own staging
// file, but a SIGKILL or power loss cannot run a defer. Because the names are
// randomized, one such kill strands one more file forever, so a crash-looping
// deployment with a persistent checkpoint volume accumulates them without
// bound. This sweep is the reclaim path.
//
// It is deliberately narrow, because it deletes files:
//   - the name must match stagingNameBounds exactly — the prefix "."+base+"."
//     AND the suffix ".tmp" AND a non-empty random component between them. That
//     excludes the checkpoint file itself, the historical predictable
//     "<path>.tmp" sibling (no leading dot, no random component), the
//     "<path>.corrupt" file the app renames a bad checkpoint to, and any other
//     file's staging name;
//   - the entry must be a REGULAR file by os.Lstat, so a symlink is neither
//     followed nor removed and a directory is left alone. writeFileAtomic opens
//     with O_CREATE|O_EXCL and so can never have produced either;
//   - it must have gone stagingFileMaxAge without modification. A future mtime
//     (clock skew) reads as age zero and is kept, which is the safe direction.
//
// Errors are returned, never fatal. ReadDir hands back the entries it managed
// to read alongside its error, so a partial listing is still swept; a file that
// vanishes mid-sweep, or that cannot be removed, is skipped silently because a
// racing peer or a read-only directory is not this function's problem.
func sweepStaleStagingFiles(path string) (int, error) {
	dir := filepath.Dir(path)
	prefix, suffix := stagingNameBounds(path)

	entries, err := readDirForSweep(dir)
	removed := 0
	for _, entry := range entries {
		random, ok := strings.CutPrefix(entry.Name(), prefix)
		if !ok {
			continue
		}
		random, ok = strings.CutSuffix(random, suffix)
		if !ok || random == "" {
			continue
		}
		full := filepath.Join(dir, entry.Name())
		// Lstat, not Stat: a symlink parked at a matching name must be judged as
		// a symlink, not as whatever it points at.
		info, statErr := os.Lstat(full)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if time.Since(info.ModTime()) < stagingFileMaxAge {
			continue
		}
		if os.Remove(full) == nil {
			removed++
		}
	}
	return removed, err
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
