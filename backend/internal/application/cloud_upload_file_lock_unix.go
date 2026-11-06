//go:build !windows && !linux

package application

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// uploadFileLocked detects an advisory lock when the Unix producer uses one.
// Unix has no portable way to observe every process copying a file; the queue
// also rechecks size and mtime immediately before hashing/uploading.
func uploadSourceGuard(path string) (func(), error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
		return func() { _ = unix.Close(fd) }, nil
	}
	_ = unix.Close(fd)
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		return nil, errUploadFileLocked
	}
	return nil, err
}
