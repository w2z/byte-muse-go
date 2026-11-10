package application

import (
	"bytemuse/backend/internal/domain"
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
	QueuedAt      string          `json:"queued_at"`
	CompletedAt   string          `json:"completed_at"`
	MaxRetries    int             `json:"max_retries"`
	updated       time.Time
	sample        time.Time
	sampleBytes   int64
	processed     bool
	failed        bool
}

// UploadDirectoryProgress aggregates one mapping, including skipped counts separately from uploaded counts.
type UploadDirectoryProgress struct {
	Key         string          `json:"key"`
	LocalPath   string          `json:"local_path"`
	RemotePath  string          `json:"remote_path"`
	Kind        string          `json:"kind"`
	Size        int64           `json:"size"`
	Progress    float64         `json:"progress"`
	Speed       float64         `json:"speed"`
	Uploaded    int             `json:"uploaded"`
	Total       int             `json:"total"`
	Skipped     int             `json:"skipped"`
	Failed      int             `json:"failed"`
	Actions     map[string]bool `json:"actions"`
	QueuedAt    string          `json:"queued_at"`
	CompletedAt string          `json:"completed_at"`
}

// UploadDirectoryPage returns filtered directory rows plus unfiltered task counts for the tab title.
type UploadDirectoryPage struct {
	Items     []UploadDirectoryProgress `json:"items"`
	Total     int                       `json:"total"`
	Completed int                       `json:"completed"`
}

// UploadFilePage is server-paginated so large libraries do not produce unbounded polling responses.
type UploadFilePage struct {
	Items     []UploadFileProgress `json:"items"`
	Total     int                  `json:"total"`
	TaskTotal int                  `json:"task_total"`
	Completed int                  `json:"completed"`
	Page      int                  `json:"page"`
	PageSize  int                  `json:"page_size"`
}

func uploadDirectoryKey(m UploadMapping) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(m.Kind+"\x00"+m.ID+"\x00"+m.LocalPath)))
}

// publishDiscoveredUpload exposes a durably queued file immediately, without rereading the entire growing queue.
func (s *UploadService) publishDiscoveredUpload(record domain.UploadRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record = s.effectiveUploadRecordLocked(record)
	if s.files == nil {
		s.files = map[string]UploadFileProgress{}
	}
	if _, exists := s.files[record.Key]; exists || record.Hidden {
		return
	}
	s.files[record.Key] = UploadFileProgress{
		Key: record.Key, DirectoryKey: uploadDirectoryKey(UploadMapping{Kind: record.Kind, ID: record.Root, LocalPath: record.LocalRoot}),
		Path: record.Source, RemotePath: uploadRemoteFilePath(record.RemotePath, record.Relative, record.Target), Size: record.Size,
		QueuedAt: uploadQueueTime(record), MaxRetries: uploadMaxAutoRetries,
		State: uploadPresentationState(record, "", false, s.mode, s.status.Enabled, s.manualUpload),
	}
	s.status.Total++
}

// Files returns one status group in ascending queue time and normalizes idle speeds to zero.
// An empty status returns every file. Filtering happens before sorting and pagination.
func (s *UploadService) Files(page, size int, status string) UploadFilePage {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	s.mu.RLock()
	items := make([]UploadFileProgress, 0, len(s.files))
	taskTotal, completed := 0, 0
	for _, item := range s.files {
		taskTotal++
		if item.State == "completed" || item.State == "skipped" {
			completed++
		}
		if !uploadMatchesStatus(item.State, status) {
			continue
		}
		item = normalizedUploadProgress(item)
		item.MaxRetries = uploadMaxAutoRetries
		item.Actions = uploadProgressActions(item.State)
		items = append(items, item)
	}
	s.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool {
		return uploadQueueBefore(
			domain.UploadRecord{QueuedAt: items[i].QueuedAt, Source: items[i].Path, Key: items[i].Key},
			domain.UploadRecord{QueuedAt: items[j].QueuedAt, Source: items[j].Path, Key: items[j].Key},
		)
	})
	total := len(items)
	start := min((page-1)*size, total)
	end := min(start+size, total)
	return UploadFilePage{Items: items[start:end], Total: total, TaskTotal: taskTotal, Completed: completed, Page: page, PageSize: size}
}

// Directories aggregates mappings that already have upload tasks.
// Saving a mapping alone does not create a row; queued, active, failed, and finished files do.
// Directories returns mappings containing at least one file in status. An empty status returns every active mapping.
// Row totals still describe the whole directory, not only the matching files.
func (s *UploadService) Directories(status string) []UploadDirectoryProgress {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.directoriesLocked(status)
}

// DirectoryPage keeps filtering independent from the completed/total counters.
func (s *UploadService) DirectoryPage(status string) UploadDirectoryPage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := s.directoriesLocked("")
	return UploadDirectoryPage{Items: s.directoriesLocked(status), Total: len(all), Completed: countCompletedDirectories(all)}
}

func countCompletedDirectories(rows []UploadDirectoryProgress) int {
	completed := 0
	for _, row := range rows {
		if row.Total > 0 && row.Uploaded+row.Skipped == row.Total {
			completed++
		}
	}
	return completed
}

func (s *UploadService) directoriesLocked(status string) []UploadDirectoryProgress {
	result := make([]UploadDirectoryProgress, 0, len(s.mappings))
	for _, m := range s.mappings {
		row := UploadDirectoryProgress{Key: uploadDirectoryKey(m), Kind: m.Kind, LocalPath: m.LocalPath, RemotePath: m.Path}
		var processedBytes int64
		processed := 0
		finished := true
		matched := status == "" || status == "all"
		for _, file := range s.files {
			if file.DirectoryKey != row.Key {
				continue
			}
			file = normalizedUploadProgress(file)
			if uploadMatchesStatus(file.State, status) {
				matched = true
			}
			row.Total++
			row.Size += file.Size
			row.Speed += file.Speed
			if file.QueuedAt != "" && (row.QueuedAt == "" || file.QueuedAt < row.QueuedAt) {
				row.QueuedAt = file.QueuedAt
			}
			if file.State == "completed" || file.State == "skipped" {
				if file.CompletedAt > row.CompletedAt {
					row.CompletedAt = file.CompletedAt
				}
			} else {
				finished = false
			}
			if file.State != "verifying" {
				processedBytes += min(file.Size, file.UploadedBytes)
			}
			if file.State == "completed" {
				row.Uploaded++
				processed++
			} else if file.State == "skipped" {
				row.Skipped++
				processed++
				processedBytes += file.Size
			} else if file.failed || file.State == "discarded" || (file.Error != "" && file.State != "waiting") {
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
		if !matched {
			continue
		}
		if row.Actions == nil {
			row.Actions = map[string]bool{}
		}
		for _, name := range []string{"pause", "stop", "start", "retry", "delete"} {
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
		if !finished {
			row.CompletedAt = ""
		}
		result = append(result, row)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].QueuedAt == "" && result[j].QueuedAt == "" {
			return false
		}
		if result[i].QueuedAt != result[j].QueuedAt {
			return result[i].QueuedAt < result[j].QueuedAt
		}
		return result[i].Key < result[j].Key
	})
	return result
}

// uploadQueueBefore orders tasks by their first queue time in ascending order.
// Execution and presentation share timestamp, source path, then key ordering.
// Historical records without QueuedAt use UpdatedAt until their next authorized processing freezes it.
func uploadQueueBefore(left, right domain.UploadRecord) bool {
	leftStamp, rightStamp := uploadQueueTime(left), uploadQueueTime(right)
	leftTime, leftErr := time.Parse(time.RFC3339Nano, leftStamp)
	rightTime, rightErr := time.Parse(time.RFC3339Nano, rightStamp)
	if leftErr == nil && rightErr == nil {
		if !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
	} else if leftStamp != rightStamp {
		return leftStamp < rightStamp
	}
	if left.Source != right.Source {
		return left.Source < right.Source
	}
	return left.Key < right.Key
}

// freezeUploadQueueTime preserves the previously displayed historical timestamp before a mutable update.
func freezeUploadQueueTime(record *domain.UploadRecord) {
	if record.QueuedAt == "" {
		record.QueuedAt = record.UpdatedAt
		if record.QueuedAt == "" {
			record.QueuedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}
}

func uploadQueueTime(record domain.UploadRecord) string {
	if record.QueuedAt != "" {
		return record.QueuedAt
	}
	return record.UpdatedAt
}

// uploadListStatus collapses detailed transport states into the list filter.
func uploadListStatus(state string) string {
	switch state {
	case "uploading", "transferring", "verifying", "retrying", "uploaded", "backing_up", "committing", "restoring", "cleanup", "discarding":
		return "running"
	case "waiting", "locked":
		return "waiting"
	case "paused":
		return "paused"
	case "stopped":
		return "stopped"
	case "failed", "attention", "blocked", "discarded":
		return "failed"
	case "completed", "skipped":
		return "completed"
	default:
		return ""
	}
}

func uploadMatchesStatus(state, status string) bool {
	return status == "" || status == "all" || uploadListStatus(state) == status
}

// UploadListStatusAllowed reports whether a list filter is recognized. Empty and all mean no filter.
func UploadListStatusAllowed(status string) bool {
	switch status {
	case "", "all", "running", "waiting", "paused", "stopped", "failed", "completed":
		return true
	default:
		return false
	}
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
	actions := map[string]bool{"pause": false, "stop": false, "start": false, "retry": false, "delete": true}
	switch state {
	case "completed", "skipped", "discarded":
	case "stopped":
		actions["start"] = true
	case "paused":
		actions["stop"] = true
		actions["start"] = true
	case "failed":
		actions["retry"] = true
	case "blocked":
		actions["pause"] = true
		actions["stop"] = true
		actions["start"] = true
	case "retrying":
		actions["pause"] = true
		actions["stop"] = true
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
	if uploadLivePhase(phase) {
		item.Error, item.NextRetry = "", ""
		if item.failed {
			item.failed = false
			s.status.Failed--
		}
	}
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
