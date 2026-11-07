package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// processDirectUpload uploads the final name and verifies content before deleting a recorded old ID.
// Stage equals Target marks direct receipts; legacy stages are never inferred to be owned or deleted.
func (s *UploadService) processDirectUpload(ctx context.Context, remote uploadRemote, record *domain.UploadRecord, mapping UploadMapping, source uploadLocalFile, sourceGuarded bool, save func() error) (string, error) {
	if record.Intent == "stop" || record.Intent == "delete" || record.State == "discarding" {
		record.State = "stopped"
		record.Hidden = record.Intent == "delete"
		return record.State, save()
	}
	entries, err := remote.List(ctx, record.Parent)
	if err != nil {
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
			}
		}
		for _, entry := range entries {
			if entry.Name == record.Target && (entry.Directory || entry.ID != record.OldID) {
				return record.State, fmt.Errorf("%w：同名文件不属于已记录的旧文件", errUploadAttention)
			}
		}
		if record.Kind == "cd2" && record.OldID != "" {
			return record.State, fmt.Errorf("CloudDrive2 无法按独立文件 ID 保留旧文件，原名直传不执行破坏性覆盖；请选择保留两者")
		}
		if err = remote.Upload(ctx, record.Parent, record.Target, file, source.Size, record.SHA1); err != nil {
			return record.State, err
		}
		domain.ReportUploadProgress(ctx, source.Size, source.Size, "uploading")
		entries, err = remote.List(ctx, record.Parent)
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
	if err = save(); err != nil {
		return record.State, err
	}
	for _, entry := range entries {
		if record.OldID == "" || entry.ID != record.OldID || entry.ID == installed.ID {
			continue
		}
		if entry.Name != record.Target || entry.Directory || entry.Size != record.OldSize || record.OldSHA1 == "" || !strings.EqualFold(entry.SHA1, record.OldSHA1) {
			return record.State, fmt.Errorf("%w：旧文件已改变，保留新旧文件", errUploadAttention)
		}
		if err = remote.Delete(ctx, record.Parent, entry); err != nil {
			return record.State, err
		}
		check, checkErr := remote.List(ctx, record.Parent)
		if checkErr != nil {
			return record.State, checkErr
		}
		if _, ok := verified(check); !ok {
			return record.State, errUploadAttention
		}
		for _, remaining := range check {
			if remaining.ID == record.OldID {
				return record.State, fmt.Errorf("旧文件删除尚未确认")
			}
		}
	}
	if record.Mode != "move" {
		record.State = "completed"
	}
	return record.State, save()
}
