package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
	"strings"
	"time"
)

const uploadControlKey = "upload-queue-control"

// uploadMaxAutoRetries is the number of failed attempts allowed before a task waits for manual reupload.
// The initial transfer counts as the first attempt. Blocked account or permission errors keep their own schedule.
const uploadMaxAutoRetries = 3

// ErrUploadControl signals a stale or unavailable operation and maps to HTTP 409.
var ErrUploadControl = errors.New("当前任务状态不允许此操作")

type uploadCommand struct {
	action string
	scope  string
	key    string
	reply  chan error
}

func uploadTerminal(state string) bool {
	return state == "completed" || state == "skipped" || state == "stopped" || state == "discarded"
}

// uploadBatchActive reports unfinished work that should continue after a manual scan.
// Failures that have used the automatic attempts stay visible but do not keep the manual batch running.
func uploadBatchActive(records []domain.UploadRecord) bool {
	for _, record := range records {
		if !uploadRetriesExhausted(record) {
			return true
		}
	}
	return false
}

// uploadRetriesExhausted reports a non-blocked failure that has used all automatic attempts.
func uploadRetriesExhausted(record domain.UploadRecord) bool {
	return record.Error != "" && record.ErrorKind != "blocked" && record.ErrorKind != "locked" && record.Retries >= uploadMaxAutoRetries
}

// Control serializes queue commands with discovery and waits for durable acknowledgement.
func (s *UploadService) Control(ctx context.Context, action string) error {
	return s.ControlScope(ctx, action, "", "")
}

// ControlScope serializes one queue, directory, or file command.
// An empty scope preserves whole-queue behavior. directory applies one action to every file task in that directory.
func (s *UploadService) ControlScope(ctx context.Context, action, scope, key string) error {
	s.mu.RLock()
	ready, ch := s.ready, s.commands
	s.mu.RUnlock()
	if !ready || ch == nil {
		return ErrUploadControl
	}
	command := uploadCommand{action: action, scope: scope, key: key, reply: make(chan error, 1)}
	select {
	case ch <- command:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-command.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// uploadActionsLocked is shared by HTTP preconditions and button availability.
func (s *UploadService) uploadActionsLocked() map[string]bool {
	active, completed, visible := false, false, false
	for _, f := range s.files {
		visible = true
		if !uploadTerminal(f.State) && f.State != "failed" {
			active = true
		}
		if f.State == "completed" || f.State == "skipped" {
			completed = true
		}
	}
	actions := map[string]bool{
		"pause":           s.ready && s.status.Enabled && s.mode == "running" && active,
		"stop":            s.ready && active && s.mode != "stopping" && s.mode != "stopped",
		"resume":          s.ready && s.status.Enabled && s.mode == "paused",
		"clear_completed": s.ready && completed,
		"delete_all":      s.ready && visible && s.mode != "deleting",
		"retry_all":       s.ready && hasUploadErrors(s.files),
		"scan":            s.ready && len(s.mappings) > 0 && s.mode != "paused" && s.mode != "stopping" && s.mode != "deleting",
	}
	if s.ready {
		for name, enabled := range uploadListClearActions(s.files) {
			actions[name] = enabled
		}
	}
	return actions
}

func hasUploadErrors(files map[string]UploadFileProgress) bool {
	for _, file := range files {
		if file.State == "failed" || file.State == "blocked" || file.State == "attention" {
			return true
		}
	}
	return false
}

// uploadListClearActions reports which tab-scoped clear buttons currently have matching rows.
// A directory is cleared only when every visible file in it belongs to the requested outcome.
func uploadListClearActions(files map[string]UploadFileProgress) map[string]bool {
	type counts struct{ total, done, failed int }
	directories := map[string]*counts{}
	completedFiles, failedFiles := false, false
	for _, file := range files {
		row := directories[file.DirectoryKey]
		if row == nil {
			row = &counts{}
			directories[file.DirectoryKey] = row
		}
		row.total++
		if file.State == "completed" || file.State == "skipped" {
			completedFiles = true
			row.done++
		}
		if file.State == "failed" || file.State == "discarded" {
			failedFiles = true
			row.failed++
		}
	}
	actions := map[string]bool{
		"clear_completed_files":       completedFiles,
		"clear_failed_files":          failedFiles,
		"clear_completed_directories": false,
		"clear_failed_directories":    false,
	}
	for _, row := range directories {
		if row.total > 0 && row.done == row.total {
			actions["clear_completed_directories"] = true
		}
		if row.total > 0 && row.failed == row.total {
			actions["clear_failed_directories"] = true
		}
	}
	return actions
}

// applyUploadControl retains hidden deduplication receipts and unfinished recovery records.
// Queue intent is persisted first so interrupted bulk controls replay before discovery.
func (s *UploadService) applyUploadControl(ctx context.Context, action string) error {
	records, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	mode := s.mode
	switch action {
	case "pause":
		mode = "paused"
	case "resume":
		mode = "running"
	case "stop", "delete_all":
		mode = "stopped"
	case "retry_all":
	case "clear_completed":
	default:
		return ErrUploadControl
	}
	control := domain.UploadRecord{Key: uploadControlKey, State: "control", Intent: mode, Target: action}
	if err = s.store.Save(ctx, control); err != nil {
		return err
	}
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
	for _, r := range records {
		if r.State == "control" {
			continue
		}
		if !uploadTerminal(r.State) {
			freezeUploadQueueTime(&r)
		}
		switch action {
		case "clear_completed":
			if r.State == "completed" || r.State == "skipped" {
				r.Hidden = true
			} else {
				continue
			}
		case "resume":
			if r.ErrorKind == "blocked" {
				r.NextRetry = ""
			}
		case "stop", "delete_all":
			if !uploadTerminal(r.State) {
				r.Intent = "stop"
				if action == "delete_all" {
					r.Intent = "delete"
				}
				if r.Parent == "" && r.State == "pending" {
					r.State = "stopped"
				} else if r.State == "pending" || r.State == "uploaded" || r.State == "failed" || r.State == "retrying" {
					r.State = "discarding"
				}
			}
			if action == "delete_all" {
				r.Hidden = true
			}
		case "retry_all":
			if r.Hidden || uploadTerminal(r.State) || r.ErrorKind == "locked" {
				continue
			}
			s.mu.RLock()
			current := s.active[r.Key] || s.status.Current == r.Key
			s.mu.RUnlock()
			if current {
				continue
			}
			if r.Error == "" && r.ErrorKind == "" && r.State != "failed" && r.State != "blocked" && r.State != "attention" {
				continue
			}
			r.Intent = "run"
			r.Retries = 0
			r.Error, r.ErrorKind, r.NextRetry = "", "", ""
		}
		if action == "delete_all" && uploadTerminal(r.State) {
			r.Hidden = true
			r.Error, r.ErrorKind, r.NextRetry = "", "", ""
		}
		if err = s.store.Save(ctx, r); err != nil {
			return err
		}
	}
	control.Target = ""
	if err = s.store.Save(ctx, control); err != nil {
		return err
	}
	return s.refreshUploadView(ctx)
}

// applyScopedUploadControl changes only the selected file or every file in the selected directory.
// It does not change the queue-wide pause, stop, or monitoring mode.
func (s *UploadService) applyScopedUploadControl(ctx context.Context, action, scope, key string) error {
	if scope == "files" || scope == "directories" {
		return s.applyListClear(ctx, action, scope)
	}
	if (scope != "file" && scope != "directory") || strings.TrimSpace(key) == "" {
		return ErrUploadControl
	}
	records, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, record := range records {
		if record.State == "control" || record.Hidden || !uploadScopeMatch(record, scope, key) {
			continue
		}
		next, ok := transformUploadControl(record, action, s.mode, s.status.Enabled || s.manualUpload)
		if !ok {
			continue
		}
		if err = s.store.Save(ctx, next); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return ErrUploadControl
	}
	return s.refreshUploadView(ctx)
}

// applyListClear hides completed or exhausted tasks in the selected list without deleting cloud files.
// The directory list removes a row only when all of its visible files have that outcome; the file list removes matching rows individually.
func (s *UploadService) applyListClear(ctx context.Context, action, scope string) error {
	if action != "clear_completed" && action != "clear_failed" {
		return ErrUploadControl
	}
	records, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	visible := make([]domain.UploadRecord, 0, len(records))
	grouped := map[string][]domain.UploadRecord{}
	for _, record := range records {
		if record.State == "control" || record.Hidden {
			continue
		}
		visible = append(visible, record)
		directory := uploadDirectoryKey(UploadMapping{Kind: record.Kind, ID: record.Root, LocalPath: record.LocalRoot})
		grouped[directory] = append(grouped[directory], record)
	}
	selected := map[string]bool{}
	if scope == "files" {
		for _, record := range visible {
			if uploadClearMatches(record, action) {
				selected[record.Key] = true
			}
		}
	} else {
		for _, group := range grouped {
			if len(group) == 0 {
				continue
			}
			matched := true
			for _, record := range group {
				if !uploadClearMatches(record, action) {
					matched = false
					break
				}
			}
			if matched {
				for _, record := range group {
					selected[record.Key] = true
				}
			}
		}
	}
	if len(selected) == 0 {
		return ErrUploadControl
	}
	for _, record := range records {
		if !selected[record.Key] {
			continue
		}
		record.Hidden = true
		if err = s.store.Save(ctx, record); err != nil {
			return err
		}
	}
	return s.refreshUploadView(ctx)
}

// uploadClearMatches reports whether one visible record belongs to a clear operation.
func uploadClearMatches(record domain.UploadRecord, action string) bool {
	if action == "clear_completed" {
		return record.State == "completed" || record.State == "skipped"
	}
	return record.State == "failed" || record.State == "discarded" || uploadRetriesExhausted(record)
}

func uploadScopeMatch(record domain.UploadRecord, scope, key string) bool {
	if scope == "file" {
		return record.Key == key
	}
	return uploadDirectoryKey(UploadMapping{Kind: record.Kind, ID: record.Root, LocalPath: record.LocalRoot}) == key
}

// transformUploadControl returns the persisted result of one file action.
// Pause covers files waiting to transfer and files uploaded but not yet replaced.
// Start overrides a queue pause, and also runs the selection while monitoring is off.
// scheduled reports whether a normal pending file would run without an individual run intent.
func transformUploadControl(record domain.UploadRecord, action, mode string, scheduled bool) (domain.UploadRecord, bool) {
	if !uploadTerminal(record.State) || (record.State == "stopped" && (action == "start" || action == "retry")) {
		freezeUploadQueueTime(&record)
	}
	if (action == "pause" || action == "stop") && uploadRetriesExhausted(record) {
		return record, false
	}
	switch action {
	case "pause":
		if record.Intent == "pause" || (record.State != "pending" && record.State != "uploaded") {
			return record, false
		}
		record.Intent = "pause"
		return record, true
	case "stop":
		if uploadTerminal(record.State) {
			return record, false
		}
		record.Intent = "stop"
		if record.Parent == "" && record.State == "pending" {
			record.State = "stopped"
		} else if record.State == "pending" || record.State == "uploaded" {
			record.State = "discarding"
		}
		if uploadTerminal(record.State) {
			record.Error, record.ErrorKind, record.NextRetry = "", "", ""
		}
		return record, true
	case "start":
		if record.ErrorKind == "attention" || uploadRetriesExhausted(record) || !uploadStartable(record, mode, scheduled) {
			return record, false
		}
		if record.State == "stopped" {
			record.State = "pending"
			record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if mode == "paused" || !scheduled {
			record.Intent = "run"
		} else {
			record.Intent = ""
		}
		record.NextRetry = ""
		if record.ErrorKind == "transient" || record.ErrorKind == "blocked" {
			record.Error, record.ErrorKind = "", ""
		}
		return record, true
	case "retry":
		if !uploadRetriesExhausted(record) {
			return record, false
		}
		record.Retries = 0
		record.Error, record.ErrorKind, record.NextRetry = "", "", ""
		if record.State == "stopped" {
			record.State = "pending"
		}
		if mode == "paused" || !scheduled {
			record.Intent = "run"
		} else {
			record.Intent = ""
		}
		return record, true
	case "delete":
		record.Hidden = true
		if !uploadTerminal(record.State) {
			record.Intent = "delete"
			if record.Parent == "" && record.State == "pending" {
				record.State = "stopped"
			} else if record.State == "pending" || record.State == "uploaded" {
				record.State = "discarding"
			}
		}
		if uploadTerminal(record.State) {
			record.Hidden = true
			record.Error, record.ErrorKind, record.NextRetry = "", "", ""
		}
		return record, true
	default:
		return record, false
	}
}

// uploadStartable selects records whose individual start changes scheduling.
// Pending files held only because monitoring is off are startable; identity conflicts are not.
func uploadStartable(record domain.UploadRecord, mode string, scheduled bool) bool {
	if record.Intent == "pause" || record.State == "stopped" {
		return true
	}
	if record.State != "pending" && record.State != "uploaded" {
		return false
	}
	if record.Error != "" || record.NextRetry != "" || (mode == "paused" && record.Intent != "run") {
		return true
	}
	return !scheduled && record.Intent != "run"
}

func uploadIndividuallyPaused(record domain.UploadRecord, mode string) bool {
	return record.Intent == "pause" || (mode == "paused" && record.Intent != "run")
}

// uploadCommandTargetsBusy reports whether pausing, stopping, or deleting would interrupt the active worker.
func uploadCommandTargetsBusy(ctx context.Context, service *UploadService, busy string, command uploadCommand) bool {
	if busy == "" || command.scope == "file" {
		return busy != "" && busy == command.key
	}
	record, err := service.store.Get(ctx, busy)
	return err == nil && record != nil && uploadScopeMatch(*record, command.scope, command.key)
}

// refreshUploadView rebuilds presentation from durable records without replacing live byte samples.
func (s *UploadService) refreshUploadView(ctx context.Context) error {
	records, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = map[string]UploadFileProgress{}
	}
	seen := map[string]bool{}
	st := &s.status
	st.Total = 0
	st.Processed = 0
	st.Uploaded = 0
	st.Skipped = 0
	st.Failed = 0
	for _, r := range records {
		if r.State == "control" || r.Hidden {
			continue
		}
		seen[r.Key] = true
		f := s.files[r.Key]
		f.Key = r.Key
		f.Path = r.Source
		f.QueuedAt = uploadQueueTime(r)
		remoteDir := r.RemotePath
		if remoteDir == "" {
			for _, mapping := range s.mappings {
				if mapping.Kind == r.Kind && mapping.ID == r.Root && mapping.LocalPath == r.LocalRoot {
					remoteDir = mapping.Path
					break
				}
			}
		}
		f.RemotePath = uploadRemoteFilePath(remoteDir, r.Relative, r.Target)
		f.Size = r.Size
		f.DirectoryKey = uploadDirectoryKey(UploadMapping{Kind: r.Kind, ID: r.Root, LocalPath: r.LocalRoot})
		f.Retries = r.Retries
		f.MaxRetries = uploadMaxAutoRetries
		f.NextRetry = r.NextRetry
		f.Error = r.Error
		f.CompletedAt = r.CompletedAt
		if f.CompletedAt == "" && (r.State == "completed" || r.State == "skipped") {
			f.CompletedAt = r.UpdatedAt
		}
		current := s.active[r.Key] || st.Current == r.Key
		f.State = uploadPresentationState(r, f.State, current, s.mode, st.Enabled, s.manualUpload)
		if !current {
			f.Speed = 0
		}
		if r.State == "completed" {
			f.Progress = 100
			f.UploadedBytes = r.Size
			st.Uploaded++
		}
		if r.State == "skipped" {
			f.Progress = 100
			f.UploadedBytes = 0
			st.Skipped++
		}
		if uploadTerminal(r.State) {
			st.Processed++
		}
		if r.Error != "" || r.State == "discarded" {
			st.Failed++
		}
		st.Total++
		s.files[r.Key] = f
	}
	for key := range s.files {
		if !seen[key] {
			delete(s.files, key)
		}
	}
	if st.Current == "" {
		st.State = "watching"
		if !st.Enabled && !s.manualUpload {
			st.State = "disabled"
		}
		if s.mode != "running" {
			st.State = s.mode
		}
	}
	return nil
}

// uploadPresentationState is the single durable-to-UI state mapping. An
// error always outranks the last commit phase, so a failed replacement cannot
// remain displayed as "正在替换" while it waits for retry.
func uploadPresentationState(r domain.UploadRecord, liveState string, current bool, mode string, enabled, manual bool) string {
	if uploadTerminal(r.State) {
		return r.State
	}
	if (r.State == "pending" || r.State == "uploaded") && uploadIndividuallyPaused(r, mode) {
		return "paused"
	}
	if r.ErrorKind == "locked" {
		return "waiting"
	}
	if r.ErrorKind == "blocked" {
		return "blocked"
	}
	if r.Error != "" {
		if r.Retries < uploadMaxAutoRetries {
			return "retrying"
		}
		return "failed"
	}
	if current && (liveState == "verifying" || liveState == "uploading" || liveState == "transferring") && r.State == "pending" {
		return liveState
	}
	if r.State == "pending" {
		if uploadIndividuallyPaused(r, mode) {
			return "paused"
		}
		if !enabled && !manual && r.Intent != "run" {
			return "stopped"
		}
		return "waiting"
	}
	if (r.State == "pending" || r.State == "uploaded") && uploadIndividuallyPaused(r, mode) {
		return "paused"
	}
	return r.State
}

// uploadRetryDue retries ordinary failures immediately and stops after the automatic attempt budget.
// Identity conflicts use the same budget; blocked account errors remain separately scheduled.
func uploadRetryDue(r domain.UploadRecord, now time.Time) bool {
	if r.ErrorKind == "locked" {
		return true
	}
	if uploadRetriesExhausted(r) {
		return false
	}
	if r.ErrorKind != "blocked" {
		return true
	}
	next, err := time.Parse(time.RFC3339Nano, r.NextRetry)
	return err != nil || !now.Before(next)
}
