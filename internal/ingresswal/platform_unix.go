//go:build linux || darwin

package ingresswal

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func platformSupported() error { return nil }

func platformOpenDirectory(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open directory", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

func platformOpenDirectoryAt(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(
		int(parent.Fd()),
		name,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open directory at", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), filepath.Join(parent.Name(), name)), nil
}

func platformMkdirAt(parent *os.File, name string, mode os.FileMode) error {
	if err := unix.Mkdirat(int(parent.Fd()), name, uint32(mode.Perm())); err != nil {
		return &os.PathError{Op: "mkdirat", Path: name, Err: err}
	}
	return nil
}

func platformOpenAt(directory *os.File, name string, flags int, mode os.FileMode) (*os.File, error) {
	fd, err := unix.Openat(
		int(directory.Fd()),
		name,
		flags|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		uint32(mode.Perm()),
	)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

func platformCreateExclusiveAt(
	directory *os.File,
	name string,
	mode os.FileMode,
) (*os.File, error) {
	return platformOpenAt(directory, name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
}

func platformPublishNoReplace(directory *os.File, stage, destination string) error {
	if err := unix.Linkat(
		int(directory.Fd()),
		stage,
		int(directory.Fd()),
		destination,
		0,
	); err != nil {
		return &os.LinkError{Op: "linkat", Old: stage, New: destination, Err: err}
	}
	return nil
}

func platformRemoveAt(directory *os.File, name string) error {
	if err := unix.Unlinkat(int(directory.Fd()), name, 0); err != nil {
		return &os.PathError{Op: "unlinkat", Path: name, Err: err}
	}
	return nil
}

func platformModeAt(directory *os.File, name string) (os.FileMode, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return 0, &os.PathError{Op: "fstatat", Path: name, Err: err}
	}
	mode := os.FileMode(stat.Mode & 0o777)
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		mode |= os.ModeDir
	case unix.S_IFLNK:
		mode |= os.ModeSymlink
	case unix.S_IFIFO:
		mode |= os.ModeNamedPipe
	case unix.S_IFSOCK:
		mode |= os.ModeSocket
	case unix.S_IFCHR:
		mode |= os.ModeCharDevice
	case unix.S_IFBLK:
		mode |= os.ModeDevice
	}
	return mode, nil
}

func platformLockExclusive(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func platformUnlock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
