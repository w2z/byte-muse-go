package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
	"time"
)

const uploadControlKey = "upload-queue-control"

// ErrUploadControl signals a stale or unavailable operation and maps to HTTP 409.
var ErrUploadControl = errors.New("当前任务状态不允许此操作")

type uploadCommand struct {
	action string
	reply  chan error
}

func uploadTerminal(state string) bool {
	return state == "completed" || state == "skipped" || state == "stopped" || state == "discarded"
}

// Control serializes queue commands with discovery and waits for durable acknowledgement.
func (s *UploadService) Control(ctx context.Context, action string) error {
	s.mu.RLock()
	ready, ch := s.ready, s.commands
	s.mu.RUnlock()
	if !ready || ch == nil {
		return ErrUploadControl
	}
	command := uploadCommand{action: action, reply: make(chan error, 1)}
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
		if !uploadTerminal(f.State) {
			active = true
		}
		if f.State == "completed" || f.State == "skipped" {
			completed = true
		}
	}
	return map[string]bool{"pause": s.ready && s.status.Enabled && s.mode == "running" && active, "stop": s.ready && active && s.mode != "stopping" && s.mode != "stopped", "resume": s.ready && s.status.Enabled && s.mode == "paused", "clear_completed": s.ready && completed, "delete_all": s.ready && visible && s.mode != "deleting"}
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
				} else if r.State == "pending" || r.State == "uploaded" {
					r.State = "discarding"
				}
			}
			if action == "delete_all" && uploadTerminal(r.State) {
				r.Hidden = true
			}
			if uploadTerminal(r.State) {
				r.Error = ""
				r.ErrorKind = ""
				r.NextRetry = ""
			}
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
		f.Size = r.Size
		f.DirectoryKey = uploadDirectoryKey(UploadMapping{Kind: r.Kind, ID: r.Root, LocalPath: r.LocalRoot})
		f.Retries = r.Retries
		f.NextRetry = r.NextRetry
		f.Error = r.Error
		if st.Current != r.Key {
			f.Speed = 0
			f.State = r.State
			if r.State == "pending" {
				f.State = "waiting"
				if s.mode == "paused" {
					f.State = "paused"
				} else if !st.Enabled {
					f.State = "stopped"
				}
			}
			if !uploadTerminal(r.State) && r.ErrorKind == "attention" {
				f.State = "attention"
			} else if !uploadTerminal(r.State) && r.ErrorKind == "blocked" {
				f.State = "blocked"
			} else if r.Error != "" && r.State == "pending" {
				f.State = "failed"
			}
			if r.State == "pending" && s.mode == "paused" {
				f.State = "paused"
			}
		}
		if r.State == "completed" || r.State == "cleanup" {
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
		if r.Error != "" {
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
		if !st.Enabled {
			st.State = "disabled"
		}
		if s.mode != "running" {
			st.State = s.mode
		}
	}
	return nil
}

// uploadRetryDue preserves backoff across restarts and never retries identity conflicts blindly.
func uploadRetryDue(r domain.UploadRecord, now time.Time) bool {
	if r.ErrorKind == "attention" {
		return false
	}
	next, err := time.Parse(time.RFC3339Nano, r.NextRetry)
	return err != nil || !now.Before(next)
}
