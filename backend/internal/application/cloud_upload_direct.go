package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// processDirectUpload replans overwrite from a fresh listing at each attempt, then freezes
// that replacement set until the attempt ends and verifies the final object before deletion.
// Stage equals Target marks direct receipts; legacy stages are never inferred to be owned or deleted.
// attemptStarted excludes pre-retry snapshots without requiring a new read at every branch.
func (s *UploadService) processDirectUpload(ctx context.Context, remote uploadRemote, record *domain.UploadRecord, mapping UploadMapping, source uploadLocalFile, protection *uploadSourceProtection, attemptStarted time.Time, save func() error) (string, error) {
	if record.Intent == "stop" || record.Intent == "delete" || record.State == "discarding" {
		record.State = "stopped"
		record.Hidden = record.Intent == "delete"
		return record.State, save()
	}
	scope := record.Kind
	if cached, ok := remote.(uploadDirectoryRemote); ok {
		scope = cached.kind
	}
	unlock, err := s.directoryCache.reserve(ctx, scope+"\x00"+record.Parent+"\x00"+record.Target)
	if err != nil {
		return record.State, err
	}
	defer func() { unlock() }()
	ctx = pan115.WithUploadStage(ctx, "prepare")
	var entries []uploadEntry
	fresh := record.State != "pending" || record.OldID != "" || record.StageID != "" || record.Error != "" || record.Overwrite != nil
	refresh := func(after time.Time) error {
		var readErr error
		entries, readErr = uploadFreshList(ctx, remote, record.Parent, after)
		if readErr == nil {
			fresh = true
		}
		return readErr
	}
	var file *os.File
	if record.State == "pending" {
		if protection == nil {
			release, lockErr := uploadSourceGuard(source.Absolute)
			if lockErr != nil {
				return record.State, lockErr
			}
			defer release()
		}
		root, openErr := os.OpenRoot(mapping.LocalPath)
		if openErr != nil {
			return record.State, openErr
		}
		defer root.Close()
		file, err = root.Open(filepath.FromSlash(source.Relative))
		if err != nil {
			return record.State, err
		}
		defer file.Close()
		info, statErr := file.Stat()
		if statErr != nil {
			return record.State, statErr
		}
		if !info.Mode().IsRegular() || info.Size() != source.Size || info.ModTime().UnixNano() != source.Modified || !uploadUnchanged(source) {
			return record.State, fmt.Errorf("文件仍在变化: %s", source.Relative)
		}
		if record.SHA1 == "" {
			record.SHA1, err = hashUploadSource(ctx, file, source.Size)
			if err != nil {
				return record.State, err
			}
			if protection != nil && protection.file != nil {
				guarded, statErr := protection.file.Stat()
				if statErr != nil {
					return record.State, statErr
				}
				if !os.SameFile(info, guarded) {
					return record.State, fmt.Errorf("%w：受保护文件身份改变，保留源文件", errLocalUploadConflict)
				}
				protection.verifiedInfo = info
			}
			if err = save(); err != nil {
				return record.State, err
			}
		}
		domain.ReportUploadProgress(ctx, 0, source.Size, "uploading")
	}
	// Hash first, then obtain one sufficiently fresh snapshot for conflict/move checks.
	if fresh {
		barrier := attemptStarted
		if record.Mode == "move" {
			barrier = time.Now()
		}
		err = refresh(barrier)
	} else {
		entries, err = remote.List(ctx, record.Parent)
	}
	if err != nil {
		if isUploadRemoteMissingError(err) {
			return record.State, fmt.Errorf("%w：%w", errUploadRemoteMissing, err)
		}
		return record.State, err
	}
	verified := func(objects []uploadEntry) (uploadEntry, bool) {
		entry, found, _ := uploadFinalEntry(record, objects, false)
		return entry, found
	}
	installed, found := verified(entries)
	transferred := false
	_, hasTarget := findUploadEntry(entries, record.Target)
	// A fresh read above already satisfies the identity barrier; do not use the total
	// hashing time to force a second read immediately after it completes.
	if (!fresh || (record.Mode != "move" && time.Since(attemptStarted) >= uploadDirectoryTTL)) && (found || (record.Kind == "115" && record.Policy == "overwrite" && record.Overwrite == nil && hasTarget)) {
		if err = refresh(time.Now()); err != nil {
			return record.State, err
		}
		installed, found = verified(entries)
	}
	// Rebind a lost cleanup receipt only from this attempt's fresh listing. A surviving
	// changed receipt or multiple matching replacements is ambiguous and must stay protected.
	// Later deletion checks remain pinned to the durably saved identity for this attempt.
	rebound := false
	if !found && record.State == "cleanup" && record.StageID != "" {
		installed, found, err = uploadFinalEntry(record, entries, true)
		if err != nil {
			return record.State, err
		}
		rebound = found
	}
	if record.Kind == "115" && record.Policy == "overwrite" {
		// Overwrite authorizes replacement of current same-name files, not a permanent
		// historical set. Retry/restart must replan too, including cleanup receipts.
		// Renamed files leave the scope; the verified final object is never a deletion candidate.
		snapshot := &domain.UploadOverwriteSnapshot{Files: []domain.UploadOldFile{}}
		for _, entry := range entries {
			if entry.Name != record.Target || (found && entry.ID == installed.ID) {
				continue
			}
			if entry.Directory || entry.ID == "" || entry.SHA1 == "" {
				return record.State, fmt.Errorf("%w：同名旧文件缺少身份信息或存在同名目录", errUploadAttention)
			}
			snapshot.Files = append(snapshot.Files, domain.UploadOldFile{ID: entry.ID, SHA1: entry.SHA1, Size: entry.Size})
		}
		previous := record.Overwrite
		record.Overwrite = snapshot
		if err = save(); err != nil {
			record.Overwrite = previous
			return record.State, err
		}
	}
	if err = validateUploadOverwrite(record, entries, installed.ID); err != nil {
		return record.State, err
	}
	if !found && record.State == "pending" {
		if !uploadUnchanged(source) {
			return record.State, fmt.Errorf("文件校验期间发生变化，停止本次上传")
		}
		if record.Policy == "keep_both" {
			if _, occupied := findUploadEntry(entries, record.Target); occupied {
				if record.Multipart != nil {
					if err = s.abortMultipartRecord(ctx, remote, record); err != nil {
						return record.State, err
					}
					if err = save(); err != nil {
						return record.State, err
					}
				}
				s.preparation.Lock()
				err = s.selectUploadName(ctx, record, entries)
				if err == nil {
					record.Stage = record.Target
					err = save()
				}
				s.preparation.Unlock()
				if err != nil {
					return record.State, err
				}
				unlock()
				unlock, err = s.directoryCache.reserve(ctx, scope+"\x00"+record.Parent+"\x00"+record.Target)
				if err != nil {
					unlock = func() {}
					return record.State, err
				}
				entries, err = remote.List(ctx, record.Parent)
				if err != nil {
					return record.State, err
				}
			}
		}
		for _, entry := range entries {
			if entry.Name == record.Target && !entry.Directory && record.Policy == "skip" {
				if record.Multipart != nil {
					if err = s.abortMultipartRecord(ctx, remote, record); err != nil {
						return record.State, err
					}
				}
				record.State = "skipped"
				return record.State, save()
			}
			if entry.Name == record.Target && (entry.Directory || (record.Overwrite == nil && entry.ID != record.OldID)) {
				return record.State, fmt.Errorf("%w：同名文件不属于已记录的旧文件", errUploadAttention)
			}
		}
		if record.Kind == "cd2" && record.OldID != "" {
			return record.State, fmt.Errorf("CloudDrive2 无法按独立文件 ID 保留旧文件，原名直传不执行破坏性覆盖；请选择保留两者")
		}
		var receipt uploadEntry
		uploadCtx := pan115.WithUploadStage(ctx, "upload")
		if record.Kind == "115" {
			uploadCtx = domain.WithUploadMultipart(uploadCtx, &domain.UploadMultipartJournal{
				Session: record.Multipart, Modified: record.Modified,
				Save: func(saveCtx context.Context, session *domain.UploadMultipart) error {
					// Never mutate the cache's shared pointer before the durable write succeeds.
					next := *record
					if session == nil {
						next.Multipart = nil
					} else {
						copy := *session
						next.Multipart = &copy
					}
					next.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
					if err := s.store.Save(saveCtx, next); err != nil {
						return err
					}
					*record = next
					return nil
				},
			})
		}
		if api, ok := remote.(uploadReceiptRemote); ok && record.Kind == "115" {
			receipt, err = api.UploadWithReceipt(uploadCtx, record.Parent, record.Target, file, source.Size, record.SHA1)
		} else {
			err = remote.Upload(uploadCtx, record.Parent, record.Target, file, source.Size, record.SHA1)
		}
		if journal := domain.UploadMultipartFromContext(uploadCtx); journal != nil && journal.Session != nil && record.Multipart == nil {
			// If the immediate checkpoint write failed, the worker's final uncancelled save still retains cleanup identity.
			copy := *journal.Session
			record.Multipart = &copy
		}
		if err != nil && strings.Contains(err.Error(), "OSS 上传回调") && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			// OSS may have committed the object before the callback response was lost or
			// rejected. Reconcile the exact target before retrying, otherwise every retry
			// uploads the same bytes again even though progress already reached 100%.
			if objects, listErr := uploadFreshList(pan115.WithUploadStage(ctx, "verify"), remote, record.Parent, time.Now()); listErr == nil {
				if recovered, ok := verified(objects); ok {
					receipt, err = recovered, nil
				}
			}
		}
		if errors.Is(err, pan115.ErrUploadSessionMissing) {
			// Completion also removes an OSS session: reconcile before allowing a fresh upload.
			objects, listErr := uploadFreshList(ctx, remote, record.Parent, time.Now())
			if listErr != nil {
				return record.State, listErr
			}
			if recovered, ok := verified(objects); ok {
				receipt, err = recovered, nil
			} else {
				if abortErr := s.abortMultipartRecord(ctx, remote, record); abortErr != nil {
					return record.State, abortErr
				}
				if saveErr := save(); saveErr != nil {
					return record.State, saveErr
				}
				return record.State, fmt.Errorf("%w：OSS 断点已失效且目录未找到完成文件，旧会话已清理，准备重新上传", errUploadSessionReset)
			}
		}
		if err != nil {
			return record.State, err
		}
		transferred = true
		uploadedAt := time.Now()
		domain.ReportUploadProgress(ctx, source.Size, source.Size, "uploading")
		if validUploadReceipt(receipt, record.Parent, record.Target, source.Size, record.SHA1) {
			pan115.TraceUploadCache(ctx, "receipt")
			installed, found = receipt, true
			entries = mergeUploadEntry(entries, receipt)
		} else {
			pan115.TraceUploadCache(ctx, "reconcile")
			entries, err = uploadFreshList(pan115.WithUploadStage(ctx, "verify"), remote, record.Parent, uploadedAt)
			if err != nil {
				return record.State, err
			}
			installed, found = verified(entries)
		}
		if !uploadUnchanged(source) {
			return record.State, fmt.Errorf("%w：文件上传期间发生变化，保留本地与远端文件", errLocalUploadConflict)
		}
	}
	if !found {
		return record.State, fmt.Errorf("%w：原名上传后大小或 SHA1 回查未通过", errUploadAttention)
	}
	previousIdentity := *record
	record.StageID, record.StageSHA1, record.StageSize = installed.ID, installed.SHA1, installed.Size
	record.State = "cleanup"
	ctx = pan115.WithUploadStage(ctx, "cleanup")
	if err = save(); err != nil {
		if rebound {
			// A failed rebind must not leak into the worker's deferred checkpoint save.
			*record = previousIdentity
		}
		return record.State, err
	}
	// The verified final identity must be durable before discarding the resume checkpoint.
	if record.Multipart != nil {
		if !transferred {
			// A prior completed call may have lost its response, or a matching file may predate this session.
			// Abort is idempotent for an already completed session and cleans any still unfinished parts.
			if err = s.abortMultipartRecord(ctx, remote, record); err != nil {
				return record.State, err
			}
		}
		previous := record.Multipart
		record.Multipart = nil
		if err = save(); err != nil {
			record.Multipart = previous
			return record.State, err
		}
	}
	if err = validateUploadOverwrite(record, entries, installed.ID); err != nil {
		return record.State, err
	}
	for _, entry := range entries {
		old, recorded := uploadRecordedOldFile(record, entry.ID)
		if !recorded || entry.ID == installed.ID {
			continue
		}
		current, readErr := uploadFreshList(ctx, remote, record.Parent, time.Now())
		if readErr != nil {
			return record.State, readErr
		}
		if _, ok := verified(current); !ok {
			return record.State, errUploadAttention
		}
		if err = validateUploadOverwrite(record, current, installed.ID); err != nil {
			return record.State, err
		}
		present := false
		for _, candidate := range current {
			if candidate.ID == entry.ID {
				entry = candidate
				present = true
				break
			}
		}
		if !present {
			continue
		}
		if entry.Name != record.Target || entry.Directory || entry.Size != old.Size || old.SHA1 == "" || !strings.EqualFold(entry.SHA1, old.SHA1) {
			return record.State, fmt.Errorf("%w：旧文件已改变，保留新旧文件", errUploadAttention)
		}
		if err = remote.Delete(ctx, record.Parent, entry); err != nil {
			return record.State, err
		}
		deletedAt := time.Now()
		check, checkErr := uploadFreshList(ctx, remote, record.Parent, deletedAt)
		if checkErr != nil {
			return record.State, checkErr
		}
		if _, ok := verified(check); !ok {
			return record.State, errUploadAttention
		}
		if err = validateUploadOverwrite(record, check, installed.ID); err != nil {
			return record.State, err
		}
		for _, remaining := range check {
			if remaining.ID == entry.ID {
				return record.State, fmt.Errorf("旧文件删除尚未确认")
			}
		}
	}
	// Keep cleanup durable until the local consumer has succeeded. A failed handoff
	// retains the move source and resumes here without uploading the file again.
	if record.Kind == "115" && s.postprocess != nil {
		if installed.Info == nil {
			return record.State, fmt.Errorf("上传回查未返回后处理所需的文件身份")
		}
		account := strings.TrimPrefix(scope, "115\x00")
		if account == scope || account == "" {
			return record.State, fmt.Errorf("无法确认上传回执所属账号")
		}
		if err = s.postprocess(ctx, account, *record, *installed.Info); err != nil {
			return record.State, &uploadLocalPostprocessError{err}
		}
	}
	if record.Mode != "move" {
		record.State = "completed"
	}
	return record.State, save()
}

// uploadFinalEntry verifies the installed receipt; recovery may select a unique replacement
// only when the recorded ID is absent. Callers must supply a fresh listing and persist a
// changed identity before cleanup, and must not reuse the old identity's local handoff.
func uploadFinalEntry(record *domain.UploadRecord, entries []uploadEntry, recoverIdentity bool) (uploadEntry, bool, error) {
	matches := func(entry uploadEntry) bool {
		return entry.ID != "" && entry.Name == record.Target && !entry.Directory && entry.Size == record.Size && entry.SHA1 != "" && strings.EqualFold(entry.SHA1, record.SHA1)
	}
	for _, entry := range entries {
		if record.StageID != "" && entry.ID != record.StageID {
			continue
		}
		if matches(entry) {
			return entry, true, nil
		}
		if recoverIdentity && record.StageID != "" {
			return uploadEntry{}, false, fmt.Errorf("%w：已记录的成品文件已改变，保留文件", errUploadAttention)
		}
	}
	if !recoverIdentity || record.StageID == "" {
		return uploadEntry{}, false, nil
	}
	var installed uploadEntry
	found := false
	for _, entry := range entries {
		if matches(entry) {
			if found && installed.ID != entry.ID {
				return uploadEntry{}, false, fmt.Errorf("%w：存在多个匹配成品，无法重新绑定身份", errUploadAttention)
			}
			installed, found = entry, true
		}
	}
	return installed, found, nil
}

// uploadRecordedOldFile reads the frozen multi-file receipt, retaining single-file legacy recovery.
func uploadRecordedOldFile(record *domain.UploadRecord, id string) (domain.UploadOldFile, bool) {
	if record.Overwrite != nil {
		for _, old := range record.Overwrite.Files {
			if old.ID == id {
				return old, true
			}
		}
		return domain.UploadOldFile{}, false
	}
	return domain.UploadOldFile{ID: record.OldID, SHA1: record.OldSHA1, Size: record.OldSize}, id != "" && id == record.OldID
}

// validateUploadOverwrite rejects changes within one attempt; the next attempt replans
// from fresh entries under the persisted overwrite policy before any remote mutation.
func validateUploadOverwrite(record *domain.UploadRecord, entries []uploadEntry, installedID string) error {
	if record.Overwrite == nil {
		return nil
	}
	for _, entry := range entries {
		if entry.ID == installedID {
			continue
		}
		old, recorded := uploadRecordedOldFile(record, entry.ID)
		if recorded {
			if entry.Name != record.Target || entry.Directory || entry.Size != old.Size || old.SHA1 == "" || !strings.EqualFold(entry.SHA1, old.SHA1) {
				return fmt.Errorf("%w：快照中的旧文件已改变，保留文件", errUploadAttention)
			}
		} else if entry.Name == record.Target {
			return fmt.Errorf("%w：覆盖快照之后出现新的同名文件，保留文件", errUploadAttention)
		}
	}
	return nil
}
