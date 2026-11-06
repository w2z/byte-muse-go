package application

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// uploadSourceGuard holds an advisory lock and checks visible Linux writers,
// including ordinary copy processes that do not acquire flock themselves.
// Writers outside the process namespace cannot be observed through /proc.
func uploadSourceGuard(path string) (func(), error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	release := func() { _ = file.Close() }
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		release()
		if err == unix.EWOULDBLOCK || err == unix.EAGAIN {
			return nil, errUploadFileLocked
		}
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		release()
		return nil, err
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		release()
		return nil, err
	}
	for _, process := range processes {
		if _, err := strconv.Atoi(process.Name()); err != nil {
			continue
		}
		base := filepath.Join("/proc", process.Name())
		descriptors, _ := os.ReadDir(filepath.Join(base, "fd"))
		for _, descriptor := range descriptors {
			other, err := os.Stat(filepath.Join(base, "fd", descriptor.Name()))
			if err != nil || !os.SameFile(info, other) {
				continue
			}
			data, _ := os.ReadFile(filepath.Join(base, "fdinfo", descriptor.Name()))
			for _, line := range strings.Split(string(data), "\n") {
				if !strings.HasPrefix(line, "flags:") {
					continue
				}
				flags, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "flags:")), 8, 64)
				if err == nil && flags&unix.O_ACCMODE != unix.O_RDONLY {
					release()
					return nil, errUploadFileLocked
				}
			}
		}
	}
	return release, nil
}
