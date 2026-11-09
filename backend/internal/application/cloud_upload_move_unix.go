//go:build !windows

package application

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

// uploadTransferGuard cannot certify immutable content on Unix: flock is advisory.
func uploadTransferGuard(path string, _ bool) (*uploadSourceProtection, error) {
	release, err := uploadSourceGuard(path)
	return &uploadSourceProtection{release: release}, err
}

// removeUploadEmptyDirectory uses directory-only unlink relative to an anchored parent handle.
func removeUploadEmptyDirectory(root *os.Root, directory string) error {
	parent, err := root.Open(filepath.Dir(directory))
	if err != nil {
		return err
	}
	defer parent.Close()
	return unix.Unlinkat(int(parent.Fd()), filepath.Base(directory), unix.AT_REMOVEDIR)
}

// uploadRemovalHandle keeps the source guard through rooted removal; Unix producers must honor advisory locks.
func uploadRemovalHandle(path string, root *os.Root, relative string) (*os.File, func() error, func(), error) {
	release, err := uploadSourceGuard(path)
	if err != nil {
		return nil, nil, nil, err
	}
	file, err := root.Open(relative)
	if err != nil {
		release()
		return nil, nil, nil, err
	}
	return file, func() error { return root.Remove(relative) }, func() { _ = file.Close(); release() }, nil
}
