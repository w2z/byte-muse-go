package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

func hasRunnableUploadView(files map[string]UploadFileProgress) bool {
	for _, file := range files {
		if file.State == "waiting" || file.State == "uploading" || file.State == "verifying" || file.State == "transferring" {
			return true
		}
	}
	return false
}

// uploadDirectiveKey gives each scope one durable control row, independent of its number of tasks.
func uploadDirectiveKey(scope, target string) string {
	if scope == "" {
		return uploadControlKey
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte("upload-control\x00"+scope+"\x00"+target)))
}

// effectiveUploadRecordLocked resolves the latest committed command without changing durable file receipts.
// The caller holds s.mu; failures, deleted tasks and completed results never become runnable through start.
func (s *UploadService) effectiveUploadRecordLocked(record domain.UploadRecord) domain.UploadRecord {
	if record.State == "control" || record.Hidden || (uploadTerminal(record.State) && record.State != "stopped") || record.Intent == "delete" {
		return record
	}
	selected := domain.UploadDirective{Sequence: record.ControlSequence}
	startSequence := uint64(0)
	directory := uploadDirectoryKey(UploadMapping{Kind: record.Kind, ID: record.Root, LocalPath: record.LocalRoot})
	for _, key := range []string{uploadControlKey, uploadDirectiveKey("directory", directory), uploadDirectiveKey("file", record.Key)} {
		if command, ok := s.directives[key]; ok {
			if command.StartSequence > startSequence {
				startSequence = command.StartSequence
			}
			if command.Sequence > selected.Sequence {
				selected = command
			}
		}
	}
	if selected.Sequence <= record.ControlSequence {
		return record
	}
	if selected.Action == "pause" {
		if uploadRetriesExhausted(record) {
			return record
		}
		if record.State == "stopped" && startSequence > record.ControlSequence {
			record.State = "pending"
		}
		if record.State == "pending" || record.State == "uploaded" {
			record.Intent = "pause"
		}
	} else if selected.Action == "start" || selected.Action == "resume" {
		if record.ErrorKind == "attention" || uploadRetriesExhausted(record) {
			return record
		}
		if record.State == "pending" || record.State == "uploaded" || (record.State == "stopped" && (selected.Action == "start" || startSequence > record.ControlSequence)) {
			record.Intent = "run"
			if record.State == "stopped" {
				record.State = "pending"
			}
		}
	}
	record.ControlSequence = selected.Sequence
	return record
}

func (s *UploadService) effectiveUploadRecord(record domain.UploadRecord) domain.UploadRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.effectiveUploadRecordLocked(record)
}

// restoreUploadDirectives rebuilds command ordering from persisted rows without backfilling file records.
func (s *UploadService) restoreUploadDirectives(records []domain.UploadRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.directives = make(map[string]domain.UploadDirective)
	for _, record := range records {
		if record.ControlSequence > s.controlSequence {
			s.controlSequence = record.ControlSequence
		}
		if record.Directive != nil {
			s.directives[record.Key] = *record.Directive
			if record.Directive.Sequence > s.controlSequence {
				s.controlSequence = record.Directive.Sequence
			}
		}
	}
}

// nextUploadControlSequence is serialized by the scheduler and survives clock changes and restarts.
func (s *UploadService) nextUploadControlSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.controlSequence++
	return s.controlSequence
}

// applyUploadDirective acknowledges only after one control row commits; no file record is rewritten.
func (s *UploadService) applyUploadDirective(ctx context.Context, action, scope, key string) error {
	records, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	if scope != "" {
		allowed := false
		for _, record := range records {
			if record.State == "control" || record.Hidden || !uploadScopeMatch(record, scope, key) {
				continue
			}
			effective := s.effectiveUploadRecord(record)
			if _, ok := transformUploadControl(effective, action, s.mode, s.status.Enabled || s.manualUpload); ok {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrUploadControl
		}
	}
	sequence := s.nextUploadControlSequence()
	command := domain.UploadDirective{Scope: scope, Target: key, Action: action, Sequence: sequence}
	s.mu.RLock()
	command.StartSequence = s.directives[uploadDirectiveKey(scope, key)].StartSequence
	s.mu.RUnlock()
	if action == "start" {
		command.StartSequence = sequence
	}
	row := domain.UploadRecord{Key: uploadDirectiveKey(scope, key), State: "control", Directive: &command, ControlSequence: sequence, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if scope == "" {
		row.Intent = "running"
		if action == "pause" {
			row.Intent = "paused"
		}
	}
	if err = s.store.Save(ctx, row); err != nil {
		return err
	}
	s.mu.Lock()
	if s.directives == nil {
		s.directives = make(map[string]domain.UploadDirective)
	}
	s.directives[row.Key] = command
	if scope == "" {
		s.mode = row.Intent
	}
	s.mu.Unlock()
	s.mergeUploadView(records, true)
	return nil
}
