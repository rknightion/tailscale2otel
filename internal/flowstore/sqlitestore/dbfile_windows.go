//go:build windows

package sqlitestore

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func openDatabaseGuard(path string, existed bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open database", Path: path, Err: err}
	}
	disposition := uint32(windows.OPEN_EXISTING)
	if !existed {
		disposition = windows.CREATE_NEW
	}
	h, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		disposition,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open database", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = f.Close()
		return nil, &os.PathError{Op: "inspect database", Path: path, Err: err}
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("sqlitestore: %s is not a regular database file", path)
	}
	if err := verifyDatabaseGuard(path, f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
