package application

import (
	"errors"
	"runtime"
	"time"
)

// uploadObservation is process-local evidence, never inferred from queue age or restored after restart.
type uploadObservation struct {
	size, modified int64
	since          time.Time
}

// uploadSourceAccess distinguishes an exclusive Windows handle from advisory Unix evidence.
// Permission and inspection failures are unknown, not proof that the file is free.
func uploadSourceAccess(path string) string {
	release, err := uploadSourceGuard(path)
	if errors.Is(err, errUploadFileLocked) {
		return "busy"
	}
	if err != nil {
		return "unknown"
	}
	release()
	if runtime.GOOS == "windows" {
		return "idle"
	}
	return "unknown"
}

// observeUploadSource uses two seconds of unchanged samples only when writers cannot be ruled out.
// This is a heuristic, not an exclusive lock; workers still guard and recheck before and after transfer.
func observeUploadSource(observations map[string]uploadObservation, file uploadLocalFile, now time.Time, access string) string {
	if access == "busy" {
		delete(observations, file.Absolute)
		return "该文件正在被占用"
	}
	if access == "idle" {
		delete(observations, file.Absolute)
		return ""
	}
	previous, exists := observations[file.Absolute]
	if !exists || previous.size != file.Size || previous.modified != file.Modified {
		observations[file.Absolute] = uploadObservation{file.Size, file.Modified, now}
		return "无法完全确认文件占用，正在观察大小和修改时间"
	}
	if now.Sub(previous.since) < 2*time.Second {
		return "无法完全确认文件占用，正在观察大小和修改时间"
	}
	return ""
}
