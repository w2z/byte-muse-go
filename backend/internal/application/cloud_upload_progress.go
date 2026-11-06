package application

import (
	"crypto/sha256"
	"fmt"
	"path"
	"sort"
	"time"
)

// UploadFileProgress describes a discovered file version; speed is a recent byte delta, never a fabricated estimate.
type UploadFileProgress struct {
	Retries       int             `json:"retries"`
	NextRetry     string          `json:"next_retry"`
	Key           string          `json:"key"`
	DirectoryKey  string          `json:"directory_key"`
	Path          string          `json:"path"`
	RemotePath    string          `json:"remote_path"`
	Size          int64           `json:"size"`
	UploadedBytes int64           `json:"uploaded_bytes"`
	Progress      float64         `json:"progress"`
	Speed         float64         `json:"speed"`
	State         string          `json:"state"`
	Error         string          `json:"error"`
	Actions       map[string]bool `json:"actions"`
	updated       time.Time
	sample        time.Time
	sampleBytes   int64
}

// UploadDirectoryProgress aggregates one mapping, including skipped counts separately from uploaded counts.
type UploadDirectoryProgress struct {
	Key        string          `json:"key"`
	LocalPath  string          `json:"local_path"`
	RemotePath string          `json:"remote_path"`
	Kind       string          `json:"kind"`
	Size       int64           `json:"size"`
	Progress   float64         `json:"progress"`
	Speed      float64         `json:"speed"`
	Uploaded   int             `json:"uploaded"`
	Total      int             `json:"total"`
	Skipped    int             `json:"skipped"`
	Failed     int             `json:"failed"`
	Actions    map[string]bool `json:"actions"`
}

// UploadFilePage is server-paginated so large libraries do not produce unbounded polling responses.
type UploadFilePage struct {
	Items    []UploadFileProgress `json:"items"`
	Total    int                  `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
}

func uploadDirectoryKey(m UploadMapping) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(m.Kind+"\x00"+m.ID+"\x00"+m.LocalPath)))
}

// Files returns a stable path-sorted page while normalizing idle speeds to zero.
func (s *UploadService) Files(page, size int) UploadFilePage {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]UploadFileProgress, 0, len(s.files))
	for _, item := range s.files {
		item = normalizedUploadProgress(item)
		item.Actions = uploadProgressActions(item.State)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Path == items[j].Path {
			return items[i].Key < items[j].Key
		}
		return items[i].Path < items[j].Path
	})
	total := len(items)
	start := min((page-1)*size, total)
	end := min(start+size, total)
	return UploadFilePage{Items: items[start:end], Total: total, Page: page, PageSize: size}
}

// Directories aggregates mappings that already have upload tasks.
// Saving a mapping alone does not create a row; queued, active, failed, and finished files do.
func (s *UploadService) Directories() []UploadDirectoryProgress {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]UploadDirectoryProgress, 0, len(s.mappings))
	for _, m := range s.mappings {
		row := UploadDirectoryProgress{Key: uploadDirectoryKey(m), Kind: m.Kind, LocalPath: m.LocalPath, RemotePath: m.Path}
		var processedBytes int64
		processed := 0
		for _, file := range s.files {
			if file.DirectoryKey != row.Key {
				continue
			}
			file = normalizedUploadProgress(file)
			row.Total++
			row.Size += file.Size
			row.Speed += file.Speed
			processedBytes += min(file.Size, file.UploadedBytes)
			if file.State == "completed" || file.State == "cleanup" {
				row.Uploaded++
				processed++
			} else if file.State == "skipped" {
				row.Skipped++
				processed++
				processedBytes += file.Size
			} else if file.Error != "" {
				row.Failed++
			}
			for name, enabled := range uploadProgressActions(file.State) {
				if enabled {
					if row.Actions == nil {
						row.Actions = map[string]bool{}
					}
					row.Actions[name] = true
				}
			}
		}
		if row.Total == 0 {
			continue
		}
		if row.Actions == nil {
			row.Actions = map[string]bool{}
		}
		for _, name := range []string{"pause", "stop", "start", "delete"} {
			if _, ok := row.Actions[name]; !ok {
				row.Actions[name] = false
			}
		}
		if row.Size > 0 {
			row.Progress = float64(processedBytes) * 100 / float64(row.Size)
		} else if row.Total > 0 {
			row.Progress = float64(processed) * 100 / float64(row.Total)
		}
		row.Progress = min(100, row.Progress)
		if processed < row.Total {
			row.Progress = min(99.9, row.Progress)
		}
		result = append(result, row)
	}
	return result
}

// uploadRemoteFilePath builds the cloud display path from the mapped directory and relative file.
// target replaces only the final file name after a keep-both reservation; an empty target keeps the local name.
func uploadRemoteFilePath(remoteDir, relative, target string) string {
	name := target
	if name == "" {
		name = path.Base(relative)
	}
	if name == "." || name == "/" {
		name = ""
	}
	dir := path.Dir(relative)
	if dir == "." {
		dir = ""
	}
	result := path.Join(remoteDir, dir, name)
	if result == "." {
		return ""
	}
	return result
}

// uploadProgressActions is the row-button contract shared by files and their parent directory.
// A directory button is enabled when any contained file accepts that action.
func uploadProgressActions(state string) map[string]bool {
	actions := map[string]bool{"pause": false, "stop": false, "start": false, "delete": true}
	switch state {
	case "completed", "skipped", "discarded":
	case "stopped":
		actions["start"] = true
	case "paused":
		actions["stop"] = true
		actions["start"] = true
	case "failed", "blocked":
		actions["pause"] = true
		actions["stop"] = true
		actions["start"] = true
	case "attention":
		actions["stop"] = true
	case "waiting", "uploading", "transferring", "verifying", "uploaded":
		actions["pause"] = true
		actions["stop"] = true
	default:
		actions["stop"] = true
	}
	return actions
}

func normalizedUploadProgress(item UploadFileProgress) UploadFileProgress {
	if time.Since(item.updated) > 3*time.Second || item.State == "completed" || item.State == "skipped" || item.State == "failed" || item.State == "stopped" {
		item.Speed = 0
	}
	return item
}

// reportBytes clamps counters and resets speed sampling when CD2 changes transport phases.
func (s *UploadService) reportBytes(key string, done, total int64, phase string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.files[key]
	if !ok {
		return
	}
	now := time.Now()
	done = max(0, min(done, item.Size))
	if item.State != phase || item.sample.IsZero() || done < item.sampleBytes {
		item.sample = now
		item.sampleBytes = done
		item.Speed = 0
	}
	elapsed := now.Sub(item.sample).Seconds()
	if elapsed >= 0.25 {
		item.Speed = float64(done-item.sampleBytes) / elapsed
		item.sample = now
		item.sampleBytes = done
	}
	item.updated = now
	item.State = phase
	item.UploadedBytes = done
	if total > 0 {
		item.Progress = min(99.9, float64(done)*100/float64(total))
	}
	s.files[key] = item
}
func (s *UploadService) finishFile(key, state string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.files[key]
	if !ok {
		return
	}
	item.State = state
	item.Speed = 0
	item.Error = ""
	if err != nil {
		item.State = "failed"
		item.Error = err.Error()
	} else if state == "completed" {
		item.Progress = 100
		item.UploadedBytes = item.Size
	} else if state == "skipped" {
		item.Progress = 100
		item.UploadedBytes = 0
	}
	s.files[key] = item
}
