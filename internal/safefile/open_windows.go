//go:build windows

package safefile

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func platformOpen(path string, allowSymlink bool) (*os.File, error) {
	if allowSymlink {
		return os.Open(path)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = f.Close()
		return nil, &os.PathError{Op: "inspect", Path: path, Err: err}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("safefile: %s is a reparse point: %w", path, ErrNotRegular)
	}
	return f, nil
}
