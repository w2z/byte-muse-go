package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// processDirectUpload verifies the final object before deleting any identity in the frozen replacement set.
// Stage equals Target marks direct receipts; legacy stages are never inferred to be owned or deleted.
// attemptStarted excludes pre-retry snapshots without requiring a new read at every branch.
func (s *UploadService) processDirectUpload(ctx context.Context, remote uploadRemote, record *domain.UploadRecord, mapping UploadMapping, source uploadLocalFile, sourceGuarded bool, attemptStarted time.Time, save func() error) (string, error) {
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
	if fresh {
		err = refresh(attemptStarted)
	} else {
		entries, err = remote.List(ctx, record.Parent)
	}
	if err != nil {
		if isUploadRemoteMissingError(err) {
			return record.State, fmt.Errorf("%w：%w", errUploadRemoteMissing, err)
		}
		return record.State, err
	}
	var file *os.File
	if record.State == "pending" {
		if !sourceGuarded {
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
			if err = save(); err != nil {
				return record.State, err
			}
		}
		domain.ReportUploadProgress(ctx, 0, source.Size, "uploading")
	}
	verified := func(objects []uploadEntry) (uploadEntry, bool) {
		for _, entry := range objects {
			if entry.Name == record.Target && !entry.Directory && entry.Size == record.Size && entry.SHA1 != "" && strings.EqualFold(entry.SHA1, record.SHA1) && (record.StageID == "" || entry.ID == record.StageID) {
				return entry, true
			}
		}
		return uploadEntry{}, false
	}
	installed, found := verified(entries)
	_, hasTarget := findUploadEntry(entries, record.Target)
	if (found || (record.Kind == "115" && record.Policy == "overwrite" && record.Overwrite == nil && hasTarget)) && (!fresh || time.Since(attemptStarted) >= uploadDirectoryTTL || record.Mode == "move") {
		barrier := attemptStarted
		if time.Since(attemptStarted) >= uploadDirectoryTTL || record.Mode == "move" {
			barrier = time.Now()
		}
		if err = refresh(barrier); err != nil {
			return record.State, err
		}
		installed, found = verified(entries)
	}
	if record.Kind == "115" && record.Policy == "overwrite" && record.Overwrite == nil {
		snapshot := &domain.UploadOverwriteSnapshot{Files: []domain.UploadOldFile{}}
		if record.State != "pending" {
			if record.OldID != "" {
				snapshot.Files = append(snapshot.Files, domain.UploadOldFile{ID: record.OldID, SHA1: record.OldSHA1, Size: record.OldSize})
			}
		} else {
			for _, entry := range entries {
				if entry.Name != record.Target || (found && entry.ID == installed.ID) {
					continue
				}
				if entry.Directory || entry.ID == "" || entry.SHA1 == "" {
					return record.State, fmt.Errorf("%w：同名旧文件缺少身份信息或存在同名目录", errUploadAttention)
				}
				if entry.ID == record.OldID && (entry.Size != record.OldSize || !strings.EqualFold(entry.SHA1, record.OldSHA1)) {
					return record.State, fmt.Errorf("%w：已记录的旧文件发生变化", errUploadAttention)
				}
				snapshot.Files = append(snapshot.Files, domain.UploadOldFile{ID: entry.ID, SHA1: entry.SHA1, Size: entry.Size})
			}
		}
		record.Overwrite = snapshot
		if err = save(); err != nil {
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
		if err = remote.Upload(pan115.WithUploadStage(ctx, "upload"), record.Parent, record.Target, file, source.Size, record.SHA1); err != nil {
			return record.State, err
		}
		uploadedAt := time.Now()
		domain.ReportUploadProgress(ctx, source.Size, source.Size, "uploading")
		entries, err = uploadFreshList(pan115.WithUploadStage(ctx, "verify"), remote, record.Parent, uploadedAt)
		if err != nil {
			return record.State, err
		}
		installed, found = verified(entries)
		if !uploadUnchanged(source) {
			return record.State, fmt.Errorf("文件上传期间发生变化，保留远端文件等待核对")
		}
	}
	if !found {
		return record.State, fmt.Errorf("%w：原名上传后大小或 SHA1 回查未通过", errUploadAttention)
	}
	record.StageID, record.StageSHA1, record.StageSize = installed.ID, installed.SHA1, installed.Size
	record.State = "cleanup"
	ctx = pan115.WithUploadStage(ctx, "cleanup")
	if err = save(); err != nil {
		return record.State, err
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
			return record.State, fmt.Errorf("上传已核验，本地后处理待重试：%w", err)
		}
	}
	if record.Mode != "move" {
		record.State = "completed"
	}
	return record.State, save()
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

// validateUploadOverwrite rejects newly arrived or changed objects without expanding the deletion set.
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
