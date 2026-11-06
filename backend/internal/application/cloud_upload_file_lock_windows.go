//go:build windows

package application

import "golang.org/x/sys/windows"

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
