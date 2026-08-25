//go:build linux || darwin

package sqlitestore

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openDatabaseGuard(path string, existed bool) (*os.File, error) {
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOFOLLOW
	if !existed {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fd, err := unix.Open(path, flags, 0o600)
	if err != nil {
		return nil, &os.PathError{Op: "open database", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	if err := verifyDatabaseGuard(path, f); err != nil {
		_ = f.Close()
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("sqlitestore: inspect database %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("sqlitestore: %s is not a regular database file", path)
	}
	return f, nil
}
