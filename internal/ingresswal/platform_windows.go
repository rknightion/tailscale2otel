//go:build windows

package ingresswal

import "os"

func platformSupported() error { return ErrUnsupported }

func platformOpenDirectory(string) (*os.File, error) { return nil, ErrUnsupported }

func platformOpenDirectoryAt(*os.File, string) (*os.File, error) {
	return nil, ErrUnsupported
}

func platformMkdirAt(*os.File, string, os.FileMode) error { return ErrUnsupported }

func platformOpenAt(*os.File, string, int, os.FileMode) (*os.File, error) {
	return nil, ErrUnsupported
}

func platformCreateExclusiveAt(*os.File, string, os.FileMode) (*os.File, error) {
	return nil, ErrUnsupported
}

func platformPublishNoReplace(*os.File, string, string) error { return ErrUnsupported }

func platformRemoveAt(*os.File, string) error { return ErrUnsupported }

func platformModeAt(*os.File, string) (os.FileMode, error) { return 0, ErrUnsupported }

func platformLockExclusive(*os.File) error { return ErrUnsupported }

func platformUnlock(*os.File) error { return ErrUnsupported }
