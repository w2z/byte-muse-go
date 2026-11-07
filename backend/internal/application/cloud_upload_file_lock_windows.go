//go:build windows

package application

import (
	"golang.org/x/sys/windows"
	"os"
)

// uploadRemovalHandle holds a writer/delete-exclusive handle and deletes that exact file, never a replacement path.
func uploadRemovalHandle(path string, _ *os.Root, _ string) (*os.File, func() error, func(), error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil, nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.DELETE, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	remove := func() error {
		deleteFile := byte(1)
		return windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &deleteFile, 1)
	}
	return file, remove, func() { _ = file.Close() }, nil
}

// uploadSourceGuard denies writers for the entire transfer while allowing rooted reads.
func uploadSourceGuard(path string) (func(), error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err == nil {
		return func() { _ = windows.CloseHandle(handle) }, nil
	}
	if err == windows.ERROR_SHARING_VIOLATION || err == windows.ERROR_LOCK_VIOLATION {
		return nil, errUploadFileLocked
	}
	return nil, err
}
