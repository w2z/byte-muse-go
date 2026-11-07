package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/clouddrive"
	"bytemuse/backend/internal/platform/pan115"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"

	"io"

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
	List(context.Context) ([]domain.UploadRecord, error)
}

// UploadStatus exposes discovery and execution separately; total may grow during monitoring.
type UploadStatus struct {
	Enabled   bool            `json:"enabled"`
	State     string          `json:"state"`
	Total     int             `json:"total"`
	Processed int             `json:"processed"`
	Uploaded  int             `json:"uploaded"`
	Skipped   int             `json:"skipped"`
	Failed    int             `json:"failed"`
	Current   string          `json:"current"`
	Error     string          `json:"error"`
	Actions   map[string]bool `json:"actions"`
}

// UploadService owns the scheduler and bounded workers; preparation serializes directory creation and name reservations.
type UploadService struct {
	settings    func(context.Context) (map[string]string, error)
	store       UploadStore
	provider    func(context.Context, string) (uploadRemote, error)
	mu          sync.RWMutex
	preparation sync.Mutex
	active      map[string]bool
	status      UploadStatus
	files       map[string]UploadFileProgress
	mappings    []UploadMapping
	commands    chan uploadCommand
	wake        chan struct{}
	ready       bool
	mode        string
	// scanRequested asks the scheduler for one immediate discovery pass.
	scanRequested bool
	scanning      bool
	// manualUpload finishes that pass while continuous monitoring remains off.
	manualUpload bool
}

// NewUploadService reuses existing provider accounts; it never enables monitoring implicitly.
func NewUploadService(settings func(context.Context) (map[string]string, error), store UploadStore, pan *Pan115Service, cloud *CloudDriveSettings) *UploadService {
	return &UploadService{settings: settings, store: store, provider: uploadProvider(pan, cloud), status: UploadStatus{State: "disabled"}}
}

// Status returns a consistent snapshot for the settings page.
func (s *UploadService) Status() UploadStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	if file, ok := s.files[status.Current]; ok {
		status.Current = file.Path
	}
	status.Actions = s.uploadActionsLocked()
	return status
}
func (s *UploadService) update(f func(*UploadStatus)) { s.mu.Lock(); defer s.mu.Unlock(); f(&s.status) }

// SettingsChanged wakes the scheduler after committed settings without canceling active transfers.
func (s *UploadService) SettingsChanged() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.wake != nil {
		select {
		case s.wake <- struct{}{}:
		default:
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

// uploadMappingRoot resolves a configured 115 directory by its display path
// when the saved directory ID was deleted. The configured mapping remains the
// queue identity; only the effective remote parent ID is replaced.
func uploadMappingRoot(ctx context.Context, remote uploadRemote, mapping UploadMapping) (string, error) {
	if _, err := remote.List(ctx, mapping.ID); err == nil {
		return mapping.ID, nil
	} else if !isUploadRemoteMissingError(err) || mapping.Kind != "115" {
		return "", err
	}
	cleanPath := strings.Trim(strings.TrimSpace(mapping.Path), "/")
	if cleanPath == "" || !strings.HasPrefix(mapping.Path, "/") {
		return "", errors.New("原网盘目录已删除且缺少完整路径，请重新选择上传目录")
	}
	parent := pan115RootDirectoryID
	for _, name := range strings.Split(cleanPath, "/") {
		if name == "" || name == "." || name == ".." {
			return "", errors.New("网盘目录路径无效，请重新选择上传目录")
		}
		entries, err := remote.List(ctx, parent)
		if err != nil {
			return "", err
		}
		matches := 0
		for _, entry := range entries {
			if entry.Name == name {
				matches++
			}
		}
		if matches > 1 {
			return "", fmt.Errorf("%w：恢复目录存在同名对象", errUploadAttention)
		}
		entry, exists := findUploadEntry(entries, name)
		if exists {
			if !entry.Directory {
				return "", fmt.Errorf("网盘路径 %s 已存在同名文件", name)
			}
			parent = entry.ID
			continue
		}
		parent, err = remote.Mkdir(ctx, parent, name)
		if err != nil {
			return "", err
		}
	}
	return parent, nil
}

// process persists its chosen names before any upload and reconciles each commit stage after uncertain failures.
func (s *UploadService) process(ctx context.Context, m UploadMapping, f uploadLocalFile, policy string) (state string, resultErr error) {
	key := uploadKey(m, f, policy)
	record, err := s.store.Get(ctx, key)
	if err != nil {
		return "", err
	}
	if record != nil && !uploadTerminal(record.State) {
		freezeUploadQueueTime(record)
	}
	defer func() {
		if record == nil {
			return
		}
		if record.Intent == "delete" && uploadTerminal(record.State) {
			record.Hidden = true
		}
		if !errors.Is(resultErr, context.Canceled) {
			record.Error, record.ErrorKind, record.NextRetry = "", "", ""
		}
		if resultErr != nil && !errors.Is(resultErr, context.Canceled) {
			if errors.Is(resultErr, errUploadRemoteMissing) {
				record.Parent = ""
				record.Stage = ""
				record.StageID, record.StageSHA1, record.StageSize = "", "", 0
				record.OldID, record.OldSHA1, record.OldSize = "", "", 0
				if record.Intent == "delete" || record.Intent == "stop" {
					record.State = "stopped"
					record.Hidden = record.Intent == "delete"
				} else {
					record.State = "pending"
					record.Error = resultErr.Error()
				}
				if record.Intent == "delete" || record.Intent == "stop" {
					resultErr = nil
				}
			} else {
				record.Error = resultErr.Error()
			}
			if errors.Is(resultErr, errUploadFileLocked) {
				record.ErrorKind = "locked"
				record.NextRetry = time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
			} else if resultErr != nil {
				record.Retries++
				record.ErrorKind = "transient"
				if errors.Is(resultErr, errUploadAttention) || errors.Is(resultErr, clouddrive.ErrUploadFailed) {
					record.ErrorKind = "attention"
				}
			}
			var httpErr *pan115.HTTPError
			var apiErr *pan115.APIError
			if errors.Is(resultErr, ErrPan115NotLinked) || pan115.Unauthorized(resultErr) || errors.Is(resultErr, clouddrive.ErrUnauthorized) || errors.Is(resultErr, clouddrive.ErrNotConfigured) || (errors.As(resultErr, &httpErr) && (httpErr.StatusCode == 401 || httpErr.StatusCode == 403)) || (errors.As(resultErr, &apiErr) && apiErr.Code == 770004) {
				record.ErrorKind = "blocked"
			}
			if record.ErrorKind == "blocked" {
				record.NextRetry = time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339Nano)
			}
			if record.Error != "" && record.ErrorKind != "blocked" && record.ErrorKind != "locked" && record.Retries < uploadMaxAutoRetries {
				record.Error = uploadRetryMessage(record.Retries, record.Error)
			}
		} else if record.CompletedAt == "" && (record.State == "completed" || record.State == "skipped") {
			record.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		state = record.State
		resultErr = errors.Join(resultErr, s.store.Save(persist, *record))
	}()
	if record != nil && uploadTerminal(record.State) {
		return record.State, nil
	}
	if record != nil && record.Intent == "delete" {
		if record.Parent == "" {
			record.State = "stopped"
			record.Hidden = true
			return record.State, nil
		}
		if record.State == "pending" || record.State == "uploaded" || record.State == "uploading" {
			record.State = "discarding"
		}
	}
	if record == nil && !uploadUnchanged(f) {
		return "", fmt.Errorf("文件仍在变化: %s", f.Relative)
	}
	if record != nil && record.Parent == "" && !uploadUnchanged(f) {
		record.State = "discarded"
		return record.State, nil
	}
	sourceGuarded := false
	var releaseSource func()
	if record == nil || record.State == "pending" {
		release, lockErr := uploadSourceGuard(f.Absolute)
		if lockErr != nil {
			return "", lockErr
		}
		releaseSource = release
		defer func() {
			if releaseSource != nil {
				releaseSource()
			}
		}()
		sourceGuarded = true
		if record != nil && record.ErrorKind == "locked" {
			record.Error, record.ErrorKind, record.NextRetry = "", "", ""
			if err = s.store.Save(ctx, *record); err != nil {
				return "", err
			}
		}
	}
	remote, err := s.provider(ctx, m.Kind)
	if err != nil {
		return "", err
	}
	if policy != "skip" && policy != "overwrite" && policy != "keep_both" {
		return "", fmt.Errorf("同名文件处理方式无效")
	}
	s.preparation.Lock()
	preparing := true
	finishPreparation := func() {
		if preparing {
			preparing = false
			s.preparation.Unlock()
		}
	}
	defer finishPreparation()
	if m.Kind == "115" && record != nil && record.Parent != "" && record.Stage != record.Target && record.Intent != "delete" && record.Intent != "stop" {
		entries, listErr := remote.List(ctx, record.Parent)
		if listErr == nil {
			for _, entry := range entries {
				if entry.Name == record.Backup || (record.OldID != "" && entry.ID == record.OldID && entry.Name != record.Target) {
					return record.State, fmt.Errorf("%w：旧任务备份需恢复，保留文件不自动清理", errUploadAttention)
				}
			}
			record.Parent, record.Stage, record.StageID = "", "", ""
			record.OldID, record.OldSHA1, record.OldSize = "", "", 0
			record.State = "pending"
		} else if !isUploadRemoteMissingError(listErr) {
			return record.State, listErr
		}
	}
	if record != nil && record.Kind == "115" && record.Parent != "" {
		if _, listErr := remote.List(ctx, record.Parent); listErr != nil {
			if isUploadRemoteMissingError(listErr) {
				record.Parent, record.StageID, record.StageSHA1, record.StageSize = "", "", "", 0
				record.Stage = ""
				record.OldID, record.OldSHA1, record.OldSize = "", "", 0
				if record.Intent == "delete" || record.Intent == "stop" {
					record.State = "stopped"
					record.Hidden = record.Intent == "delete"
					return record.State, nil
				}
				record.State = "pending"
				record.Error, record.ErrorKind, record.NextRetry = "", "", ""
				if err = s.store.Save(ctx, *record); err != nil {
					return "", err
				}
			} else {
				return "", listErr
			}
		}
	}
	save := func() error {
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return s.store.Save(ctx, *record)
	}
	if record == nil || record.Parent == "" {
		preparationRemote := &uploadPreparationRemote{uploadRemote: remote, entries: map[string][]uploadEntry{}}
		root, e := uploadMappingRoot(ctx, preparationRemote, m)
		if e != nil {
			return "", e
		}
		parent, e := uploadParent(ctx, preparationRemote, root, f.Relative)
		if e != nil {
			return "", e
		}
		entries, e := preparationRemote.List(ctx, parent)
		if e != nil {
			return "", e
		}
		name := path.Base(f.Relative)
		existing, exists := findUploadEntry(entries, name)
		if record == nil {
			record = &domain.UploadRecord{Key: key, Kind: m.Kind, Root: m.ID, LocalRoot: m.LocalPath, Relative: f.Relative, Source: f.Absolute, Size: f.Size, Modified: f.Modified, Policy: policy, State: "pending"}
		}
		if record.QueuedAt == "" {
			record.QueuedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		record.Parent, record.Target = parent, name
		if exists && existing.Directory {
			return "", fmt.Errorf("目标已存在同名目录: %s", name)
		}
		if exists && policy == "skip" {
			record.State = "skipped"
			return record.State, save()
		}
		if policy == "keep_both" {
			if e = s.selectUploadName(ctx, record, entries); e != nil {
				return "", e
			}
		}
		record.Stage, record.Backup = record.Target, ""
		if exists && policy == "overwrite" {
			record.OldID, record.OldSHA1, record.OldSize = existing.ID, existing.SHA1, existing.Size
		}
		if e = save(); e != nil {
			return "", e
		}
	}
	if record.RemotePath == "" {
		record.RemotePath = m.Path
	}
	finishPreparation()
	if record.Stage == record.Target {
		state, err = s.processDirectUpload(ctx, remote, record, m, f, sourceGuarded, save)
		if err != nil || state != "cleanup" || record.Mode != "move" {
			return state, err
		}
		if releaseSource != nil {
			releaseSource()
			releaseSource = nil
		}
		if err = removeUploadedSource(ctx, record); err != nil {
			return record.State, err
		}
		record.State = "completed"
		return record.State, save()
	}
	if record.State == "pending" && !uploadUnchanged(f) {
		record.State = "discarding"
		if err = save(); err != nil {
			return "", err
		}
	}
	if record.State == "discarding" {
		return s.discardUpload(ctx, remote, record, save)
	}
	if record.State == "completed" || record.State == "skipped" || record.State == "stopped" || record.State == "discarded" {
		return record.State, nil
	}
	if record.State == "pending" {
		// Anchor file access to the selected root so replacing a parent with a symlink cannot escape it.
		if !sourceGuarded {
			release, lockErr := uploadSourceGuard(f.Absolute)
			if lockErr != nil {
				return "", lockErr
			}
			defer release()
		}
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
			record.SHA1, e = hashUploadSource(ctx, file, f.Size)
			if e != nil {
				return "", e
			}
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
			if record.StageID != "" && m.Kind == "115" && !matchesUpload(record, existing, false) {
				return "", errUploadAttention
			}
			if record.StageID == "" || (m.Kind == "115" && existing.ID != record.StageID) {
				record.StageID = existing.ID
				record.StageSHA1, record.StageSize = existing.SHA1, existing.Size
				if e = save(); e != nil {
					return "", e
				}
			}
			if cd, ok := remote.(uploadCDRemote); ok && existing.Size == f.Size {
				if e = cd.c.WaitUpload(ctx, path.Join(record.Parent, record.Stage)); e != nil {
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
				if cd, ok := remote.(uploadCDRemote); ok {
					if e = cd.c.ControlUpload(ctx, path.Join(record.Parent, record.Stage), "cancel"); e != nil {
						return "", e
					}
					if existing.SHA1 == "" {
						return "", fmt.Errorf("%w：CD2 临时文件缺少内容校验值", errUploadAttention)
					}
				}
				if e = remote.Delete(ctx, record.Parent, existing); e != nil {
					return "", e
				}
				check, checkErr := remote.List(ctx, record.Parent)
				if checkErr != nil {
					return "", checkErr
				}
				if _, present := findUploadEntry(check, record.Stage); present {
					return "", fmt.Errorf("临时文件删除尚未确认")
				}
				record.StageID = ""
				if e = save(); e != nil {
					return "", e
				}
				exists = false
			}
		}
		if !exists {
			e = remote.Upload(ctx, record.Parent, record.Stage, file, f.Size, record.SHA1)
			if e != nil {
				// Capture an uncertain upload's private object before cancellation returns to the queue.
				inspect, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				if objects, listErr := remote.List(inspect, record.Parent); listErr == nil {
					if owned, found := findUploadEntry(objects, record.Stage); found && !owned.Directory {
						record.StageID, record.StageSHA1, record.StageSize = owned.ID, owned.SHA1, owned.Size
					}
				}
				stop()
				return "", e
			}
		}
		entries, e = remote.List(ctx, record.Parent)
		if e != nil {
			return "", e
		}
		stage, ok := findUploadEntry(entries, record.Stage)
		if ok && !stage.Directory {
			record.StageID = stage.ID
			record.StageSHA1, record.StageSize = stage.SHA1, stage.Size
		}
		if !uploadUnchanged(f) {
			record.State = "discarding"
			if e = save(); e != nil {
				return "", e
			}
			return s.discardUpload(ctx, remote, record, save)
		}
		if !ok || stage.Directory || stage.Size != f.Size || (stage.SHA1 != "" && !strings.EqualFold(stage.SHA1, record.SHA1)) {
			return "", fmt.Errorf("上传后网盘文件回查未通过")
		}
		if m.Kind == "cd2" && stage.SHA1 == "" {
			return "", fmt.Errorf("%w：CD2 尚未提供内容校验值", errUploadAttention)
		}
		record.State = "uploaded"
		record.StageID = stage.ID
		if e = save(); e != nil {
			return "", e
		}
	}
	return s.commitUpload(ctx, remote, record, save)
}

func uploadRetryMessage(retries int, message string) string {
	prefix := fmt.Sprintf("正在重试 (%d/%d)；", retries, uploadMaxAutoRetries)
	if strings.HasPrefix(message, "正在重试 (") {
		if separator := strings.Index(message, "；"); separator >= 0 {
			message = strings.TrimSpace(message[separator+len("；"):])
		}
	}
	return prefix + strings.TrimSpace(message)
}

// hashUploadSource calculates the source SHA1 and publishes absolute verified bytes.
// Progress stays in the verifying phase and never exceeds the selected file size; cancellation leaves the hash unsaved.
func hashUploadSource(ctx context.Context, file io.Reader, size int64) (string, error) {
	domain.ReportUploadProgress(ctx, 0, size, "verifying")
	h := sha1.New()
	buf := make([]byte, 1024*1024)
	var done int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := file.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
			done += int64(n)
			if size >= 0 && done > size {
				return "", io.ErrUnexpectedEOF
			}
			domain.ReportUploadProgress(ctx, done, size, "verifying")
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	if done != size {
		return "", io.ErrUnexpectedEOF
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
