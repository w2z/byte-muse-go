package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/clouddrive"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// UploadStore persists source versions and commit stages across service restarts.
type UploadStore interface {
	Get(context.Context, string) (*domain.UploadRecord, error)
	Save(context.Context, domain.UploadRecord) error
	PendingCommits(context.Context) ([]domain.UploadRecord, error)
}

// UploadStatus exposes discovery and execution separately; total may grow during monitoring.
type UploadStatus struct {
	Enabled   bool   `json:"enabled"`
	State     string `json:"state"`
	Total     int    `json:"total"`
	Processed int    `json:"processed"`
	Uploaded  int    `json:"uploaded"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
	Current   string `json:"current"`
	Error     string `json:"error"`
}

// UploadService owns one cancellable watcher/worker and serializes remote writes for all mappings.
type UploadService struct {
	settings func(context.Context) (map[string]string, error)
	store    UploadStore
	provider func(context.Context, string) (uploadRemote, error)
	mu       sync.RWMutex
	status   UploadStatus
	files    map[string]UploadFileProgress
	mappings []UploadMapping
}

// NewUploadService reuses existing provider accounts; it never enables monitoring implicitly.
func NewUploadService(settings func(context.Context) (map[string]string, error), store UploadStore, pan *Pan115Service, cloud *CloudDriveSettings) *UploadService {
	return &UploadService{settings: settings, store: store, provider: uploadProvider(pan, cloud), status: UploadStatus{State: "disabled"}}
}

// Status returns a consistent snapshot for the settings page.
func (s *UploadService) Status() UploadStatus         { s.mu.RLock(); defer s.mu.RUnlock(); return s.status }
func (s *UploadService) update(f func(*UploadStatus)) { s.mu.Lock(); defer s.mu.Unlock(); f(&s.status) }

// Run reconciles saved configuration every second. Changing mappings or disabling cancels the active run.
func (s *UploadService) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var cancel context.CancelFunc
	var done chan struct{}
	signature := ""
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			cancel = nil
		}
	}
	defer stop()
	for {
		values, err := s.settings(ctx)
		if err == nil {
			next := values["CLOUD_UPLOAD_ENABLE"] + "\x00" + values["CLOUD_UPLOAD_PATHS"] + "\x00" + values["CLOUD_UPLOAD_CONFLICT"]
			if next != signature {
				stop()
				signature = next
				mappings, e := parseUploadMappings(values["CLOUD_UPLOAD_PATHS"])
				enabled := values["CLOUD_UPLOAD_ENABLE"] == "true"
				policy := values["CLOUD_UPLOAD_CONFLICT"]
				if policy == "" {
					policy = "skip"
				}
				if enabled && e == nil && len(mappings) == 0 {
					e = fmt.Errorf("请先添加上传目录映射")
				}
				s.update(func(st *UploadStatus) {
					if enabled {
						s.files = map[string]UploadFileProgress{}
						*st = UploadStatus{}
					} else {
						for key, file := range s.files {
							if file.State != "completed" && file.State != "skipped" && file.State != "failed" {
								file.State, file.Speed = "stopped", 0
								s.files[key] = file
							}
						}
					}
					s.mappings = append([]UploadMapping(nil), mappings...)
					st.Enabled, st.State, st.Current = enabled, "disabled", ""
					if e != nil {
						st.Error = e.Error()
						st.State = "failed"
					}
				})
				if enabled && e == nil && len(mappings) > 0 {
					worker, stopWorker := context.WithCancel(ctx)
					cancel = stopWorker
					done = make(chan struct{})
					go func() { defer close(done); defer stopWorker(); s.monitor(worker, mappings, policy) }()
				}
			}
		} else {
			s.update(func(st *UploadStatus) { st.Error = err.Error() })
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// monitor combines native notifications with a periodic reconciliation for NAS mounts and missed events.
// Discovery continues independently of slow uploads; stable snapshots are processed by one worker.
func (s *UploadService) monitor(ctx context.Context, mappings []UploadMapping, policy string) {
	watch, err := fsnotify.NewWatcher()
	if err != nil {
		s.update(func(st *UploadStatus) { st.State = "failed"; st.Error = err.Error() })
		return
	}
	defer watch.Close()
	wake := make(chan struct{}, 1)
	notify := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	eventDone := make(chan struct{})
	go func() {
		defer close(eventDone)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-watch.Events:
				if !ok {
					return
				}
				notify()
			case e, ok := <-watch.Errors:
				if !ok {
					return
				}
				s.update(func(st *UploadStatus) { st.Error = e.Error() })
				notify()
			}
		}
	}()
	defer func() { watch.Close(); <-eventDone }()
	knownDirs := map[string]bool{}
	addWatches := func() {
		for _, m := range mappings {
			_ = filepath.WalkDir(m.LocalPath, func(p string, e fs.DirEntry, err error) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err != nil {
					return err
				}
				if e.IsDir() && !knownDirs[p] {
					if err = watch.Add(p); err == nil {
						knownDirs[p] = true
					}
				}
				return nil
			})
		}
	}
	addWatches()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	type candidate struct {
		mapping UploadMapping
		file    uploadLocalFile
		since   time.Time
		policy  string
	}
	observed := map[string]candidate{}
	finished := map[string]bool{}
	retry := map[string]time.Time{}
	type result struct {
		key, state string
		err        error
	}
	results := make(chan result, 1)
	busy := ""
	workerDone := make(chan struct{}, 1)
	workerDone <- struct{}{}
	defer func() { <-workerDone }()
	dirty := true
	nextReconcile := time.Time{}
	for {
		now := time.Now()
		candidates := []candidate{}
		if dirty || !now.Before(nextReconcile) {
			dirty = false
			nextReconcile = now.Add(30 * time.Second)
			addWatches()
			seen := map[string]bool{}
			committingSources := map[string]bool{}
			// Commit recovery must survive source edits/deletions after the old target was backed up.
			commits, commitErr := s.store.PendingCommits(ctx)
			if commitErr != nil {
				s.update(func(st *UploadStatus) { st.Error = commitErr.Error() })
			}
			for _, record := range commits {
				for _, m := range mappings {
					if record.Kind != m.Kind || record.Root != m.ID || record.LocalRoot != m.LocalPath {
						continue
					}
					seen[record.Key] = true
					committingSources[record.Source] = true
					if _, exists := observed[record.Key]; !exists {
						f := uploadLocalFile{Absolute: record.Source, Relative: record.Relative, Size: record.Size, Modified: record.Modified}
						observed[record.Key] = candidate{m, f, now.Add(-time.Minute), record.Policy}
						s.mu.Lock()
						if s.files == nil {
							s.files = map[string]UploadFileProgress{}
						}
						s.files[record.Key] = UploadFileProgress{Key: record.Key, DirectoryKey: uploadDirectoryKey(m), Path: f.Absolute, Size: f.Size, State: "verifying"}
						s.mu.Unlock()
					}
				}
			}
			for _, m := range mappings {
				files, e := uploadSnapshot(ctx, m)
				if e != nil {
					s.update(func(st *UploadStatus) { st.Error = e.Error() })
					continue
				}
				for _, f := range files {
					if committingSources[f.Absolute] {
						continue
					}
					key := uploadKey(m, f, policy)
					seen[key] = true
					c, ok := observed[key]
					if !ok {
						c = candidate{m, f, now, policy}
						observed[key] = c
						s.mu.Lock()
						if s.files == nil {
							s.files = map[string]UploadFileProgress{}
						}
						s.files[key] = UploadFileProgress{Key: key, DirectoryKey: uploadDirectoryKey(m), Path: f.Absolute, Size: f.Size, State: "waiting"}
						s.mu.Unlock()
					}
				}
			}
			for key := range observed {
				if !seen[key] && key != busy {
					delete(observed, key)
					delete(finished, key)
					delete(retry, key)
					s.mu.Lock()
					delete(s.files, key)
					s.mu.Unlock()
				}
			}
		}
		for key, c := range observed {
			if !finished[key] {
				candidates = append(candidates, c)
			}
		}
		s.update(func(st *UploadStatus) {
			st.Total = len(observed)
			st.Processed = len(finished)
			st.Uploaded, st.Skipped = 0, 0
			for key := range finished {
				if s.files[key].State == "skipped" {
					st.Skipped++
				} else {
					st.Uploaded++
				}
			}
			st.Enabled = true
			if busy == "" {
				st.State = "watching"
			}
		})
		if busy == "" {
			for _, c := range candidates {
				key := uploadKey(c.mapping, c.file, c.policy)
				if now.Sub(c.since) < 5*time.Second || now.Before(retry[key]) {
					continue
				}
				busy = key
				<-workerDone
				s.update(func(st *UploadStatus) { st.State = "uploading"; st.Current = c.file.Relative })
				go func(c candidate, key string) {
					defer func() { workerDone <- struct{}{} }()
					progressCtx := domain.WithUploadProgress(ctx, func(done, total int64, phase string) { s.reportBytes(key, done, total, phase) })
					state, e := s.process(progressCtx, c.mapping, c.file, c.policy)
					results <- result{key, state, e}
				}(c, key)
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case r := <-results:
			s.finishFile(r.key, r.state, r.err)
			busy = ""
			if r.err != nil {
				retry[r.key] = time.Now().Add(time.Minute)
				s.update(func(st *UploadStatus) { st.Failed++; st.Error = r.err.Error(); st.Current = "" })
			} else {
				finished[r.key] = true
				s.update(func(st *UploadStatus) {
					if r.state == "skipped" {
						st.Skipped++
					} else {
						st.Uploaded++
					}
					st.Current = ""
					st.Error = ""
				})
			}
		case <-wake:
			dirty = true
		case <-ticker.C:
		}
	}
}

// uploadKey binds an attempt to its source version, destination and conflict policy.
func uploadKey(m UploadMapping, f uploadLocalFile, policy string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s", m.Kind, m.ID, m.LocalPath, f.Absolute, f.Size, f.Modified, policy))))
}
func findUploadEntry(entries []uploadEntry, name string) (uploadEntry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return uploadEntry{}, false
}
func uploadUnchanged(f uploadLocalFile) bool {
	info, err := os.Lstat(f.Absolute)
	return err == nil && info.Mode().IsRegular() && info.Size() == f.Size && info.ModTime().UnixNano() == f.Modified
}

// uploadParent recreates only missing relative subdirectories, checking for file/folder conflicts.
func uploadParent(ctx context.Context, remote uploadRemote, root, relative string) (string, error) {
	parent := root
	dir := path.Dir(relative)
	if dir == "." {
		return parent, nil
	}
	for _, name := range strings.Split(dir, "/") {
		entries, err := remote.List(ctx, parent)
		if err != nil {
			return "", err
		}
		entry, exists := findUploadEntry(entries, name)
		if exists {
			if !entry.Directory {
				return "", fmt.Errorf("网盘路径 %s 已存在同名文件", name)
			}
			parent = entry.ID
			continue
		}
		id, err := remote.Mkdir(ctx, parent, name)
		if err != nil {
			return "", err
		}
		parent = id
	}
	return parent, nil
}

// process persists its chosen names before any upload and reconciles each commit stage after uncertain failures.
func (s *UploadService) process(ctx context.Context, m UploadMapping, f uploadLocalFile, policy string) (string, error) {
	key := uploadKey(m, f, policy)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		return "", err
	}
	if record != nil && (record.State == "completed" || record.State == "skipped") {
		return record.State, nil
	}
	if (record == nil || record.State == "pending") && !uploadUnchanged(f) {
		return "", fmt.Errorf("文件仍在变化: %s", f.Relative)
	}
	remote, err := s.provider(ctx, m.Kind)
	if err != nil {
		return "", err
	}
	if policy != "skip" && policy != "overwrite" && policy != "keep_both" {
		return "", fmt.Errorf("同名文件处理方式无效")
	}
	save := func() error {
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return s.store.Save(ctx, *record)
	}
	if record == nil {
		parent, e := uploadParent(ctx, remote, m.ID, f.Relative)
		if e != nil {
			return "", e
		}
		entries, e := remote.List(ctx, parent)
		if e != nil {
			return "", e
		}
		name := path.Base(f.Relative)
		existing, exists := findUploadEntry(entries, name)
		record = &domain.UploadRecord{Key: key, Kind: m.Kind, Root: m.ID, LocalRoot: m.LocalPath, Relative: f.Relative, Source: f.Absolute, Size: f.Size, Modified: f.Modified, Policy: policy, Parent: parent, Target: name, Stage: ".bytemuse-upload-" + key, Backup: ".bytemuse-backup-" + key, State: "pending"}
		if exists && existing.Directory {
			return "", fmt.Errorf("目标已存在同名目录: %s", name)
		}
		if exists && policy == "skip" {
			record.State = "skipped"
			return record.State, save()
		}
		if exists && policy == "keep_both" {
			ext := path.Ext(name)
			base := strings.TrimSuffix(name, ext)
			for n := 1; ; n++ {
				candidate := fmt.Sprintf("%s (%d)%s", base, n, ext)
				if _, found := findUploadEntry(entries, candidate); !found {
					record.Target = candidate
					break
				}
			}
		}
		if e = save(); e != nil {
			return "", e
		}
	}
	if record.State == "pending" {
		// Anchor file access to the selected root so replacing a parent with a symlink cannot escape it.
		root, e := os.OpenRoot(m.LocalPath)
		if e != nil {
			return "", e
		}
		defer root.Close()
		file, e := root.Open(filepath.FromSlash(f.Relative))
		if e != nil {
			return "", e
		}
		defer file.Close()
		opened, e := file.Stat()
		if e != nil {
			return "", e
		}
		if !opened.Mode().IsRegular() || opened.Size() != f.Size || opened.ModTime().UnixNano() != f.Modified || !uploadUnchanged(f) {
			return "", fmt.Errorf("文件仍在变化: %s", f.Relative)
		}
		if record.SHA1 == "" {
			domain.ReportUploadProgress(ctx, 0, f.Size, "verifying")
			h := sha1.New()
			buf := make([]byte, 1024*1024)
			for {
				if e = ctx.Err(); e != nil {
					return "", e
				}
				n, readErr := file.Read(buf)
				if n > 0 {
					_, _ = h.Write(buf[:n])
				}
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					return "", readErr
				}
			}
			record.SHA1 = fmt.Sprintf("%x", h.Sum(nil))
			if e = save(); e != nil {
				return "", e
			}
		}
		entries, e := remote.List(ctx, record.Parent)
		if e != nil {
			return "", e
		}
		existing, exists := findUploadEntry(entries, record.Stage)
		if exists {
			if cd, ok := remote.(uploadCDRemote); ok && existing.Size == f.Size {
				if e = cd.c.WaitUpload(ctx, path.Join(record.Parent, record.Stage)); e != nil {
					if errors.Is(e, clouddrive.ErrUploadFailed) {
						if cleanupErr := remote.Delete(ctx, record.Parent, existing); cleanupErr != nil {
							return "", errors.Join(e, cleanupErr)
						}
					}
					return "", e
				}
			}
			entries, e = remote.List(ctx, record.Parent)
			if e != nil {
				return "", e
			}
			existing, exists = findUploadEntry(entries, record.Stage)
			if !exists {
				return "", fmt.Errorf("上传临时文件丢失")
			}
			if existing.Directory {
				return "", fmt.Errorf("上传临时路径出现目录，停止处理")
			}
			if existing.Size != f.Size || (existing.SHA1 != "" && !strings.EqualFold(existing.SHA1, record.SHA1)) {
				// Only this persisted attempt's private staging object may be replaced.
				if e = remote.Delete(ctx, record.Parent, existing); e != nil {
					return "", e
				}
				exists = false
			}
		}
		if !exists {
			e = remote.Upload(ctx, record.Parent, record.Stage, file, f.Size)
			if e != nil {
				return "", e
			}
		}
		if !uploadUnchanged(f) {
			return "", fmt.Errorf("上传期间源文件变化: %s", f.Relative)
		}
		entries, e = remote.List(ctx, record.Parent)
		if e != nil {
			return "", e
		}
		stage, ok := findUploadEntry(entries, record.Stage)
		if !ok || stage.Directory || stage.Size != f.Size || (stage.SHA1 != "" && !strings.EqualFold(stage.SHA1, record.SHA1)) {
			return "", fmt.Errorf("上传后网盘文件回查未通过")
		}
		record.State = "uploaded"
		if e = save(); e != nil {
			return "", e
		}
	}
	// Re-read on every phase: a timeout may have applied the remote rename already.
	entries, err := remote.List(ctx, record.Parent)
	if err != nil {
		return "", err
	}
	stage, hasStage := findUploadEntry(entries, record.Stage)
	target, hasTarget := findUploadEntry(entries, record.Target)
	_, hasBackup := findUploadEntry(entries, record.Backup)
	if record.State == "uploaded" {
		if !hasStage {
			return "", fmt.Errorf("已上传临时文件丢失")
		}
		if hasTarget {
			if policy != "overwrite" {
				return "", fmt.Errorf("上传期间目标出现同名文件，请重新选择策略")
			}
			if target.Directory {
				return "", fmt.Errorf("拒绝覆盖目录")
			}
			if hasBackup {
				return "", fmt.Errorf("备份与目标同时存在，停止覆盖")
			}
			if err = remote.Rename(ctx, record.Parent, target, record.Backup); err != nil {
				return "", err
			}
		}
		record.State = "committing"
		if err = save(); err != nil {
			return "", err
		}
	}
	if record.State == "committing" {
		entries, err = remote.List(ctx, record.Parent)
		if err != nil {
			return "", err
		}
		stage, hasStage = findUploadEntry(entries, record.Stage)
		target, hasTarget = findUploadEntry(entries, record.Target)
		if hasStage {
			if hasTarget {
				return "", fmt.Errorf("目标并发变更，保留上传文件及备份")
			}
			if err = remote.Rename(ctx, record.Parent, stage, record.Target); err != nil {
				return "", err
			}
			entries, err = remote.List(ctx, record.Parent)
			if err != nil {
				return "", err
			}
			target, hasTarget = findUploadEntry(entries, record.Target)
		}
		if !hasTarget || target.Directory || target.Size != record.Size || (target.SHA1 != "" && !strings.EqualFold(target.SHA1, record.SHA1)) {
			return "", fmt.Errorf("最终文件回查未通过，保留备份")
		}
		if backup, ok := findUploadEntry(entries, record.Backup); ok {
			if err = remote.Delete(ctx, record.Parent, backup); err != nil {
				return "", err
			}
		}
		record.State = "completed"
		if err = save(); err != nil {
			return "", err
		}
	}
	return record.State, nil
}
