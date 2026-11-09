//go:build windows

package application

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"sync"
)

// uploadTransferGuard retains a deletion-capable, writer-exclusive handle for move
// attempts, so cleanup does not release protection and reopen a potentially changed file.
func uploadTransferGuard(path string, move bool) (*uploadSourceProtection, error) {
	if !move {
		release, err := uploadSourceGuard(path)
		return &uploadSourceProtection{release: release}, err
	}
	file, remove, release, err := uploadRemovalHandle(path, nil, "")
	if err == windows.ERROR_SHARING_VIOLATION || err == windows.ERROR_LOCK_VIOLATION {
		return nil, errUploadFileLocked
	}
	if err != nil {
		return nil, err
	}
	return &uploadSourceProtection{file: file, remove: remove, release: sync.OnceFunc(release)}, nil
}

// removeUploadEmptyDirectory deletes only an identity-checked directory handle; nonempty directories remain intact.
func removeUploadEmptyDirectory(root *os.Root, directory string) error {
	name, err := windows.UTF16PtrFromString(filepath.Join(root.Name(), directory))
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	folder := os.NewFile(uintptr(handle), directory)
	defer folder.Close()
	opened, err := folder.Stat()
	if err != nil {
		return err
	}
	current, err := root.Lstat(directory)
	if err != nil {
		return err
	}
	if !opened.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) {
		return os.ErrInvalid
	}
	deleteDirectory := byte(1)
	return windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &deleteDirectory, 1)
}

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
