//go:build !windows

package application

import "os"

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
