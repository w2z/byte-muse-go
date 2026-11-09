package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

// Local handoff follows the existing event synchronization switch; it does not enable STRM implicitly.
const pan115LocalUploadEnabledKey = "PAN115_EVENT_ENABLE"

type localUploadSourceKey struct{}

// localUploadRules binds event acknowledgement to the same mapping, formats, root and playback URL.
func (s *StrmService) localUploadRules(values map[string]string) string {
	raw, _ := json.Marshal([]string{s.root, values[strmPathsSettingKey], values[strmPlayBaseSettingKey], values[strmDownloadEnableSettingKey], values[strmDownloadExtensionsSettingKey]})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// BeginLocalUpload defers event polling while local workers are producing verified receipts.
// It avoids fetching details before the upload's own verification and handoff have finished.
func (s *StrmService) BeginLocalUpload() func() {
	s.localUploads.Add(1)
	return func() { s.localUploads.Add(-1) }
}

// ResumeLocalUpload recovers an existing handoff using local storage only. Its existence proves
// remote replacement cleanup preceded handoff; move recovery and legacy tasks retain remote verification.
func (s *StrmService) ResumeLocalUpload(ctx context.Context, record domain.UploadRecord) (bool, error) {
	if s.uploadReceipts == nil || s.managedAccount == nil {
		return false, nil
	}
	account, err := s.managedAccount(ctx)
	if err != nil {
		return false, err
	}
	receipt, err := s.uploadReceipts.Get(ctx, journalKey(account, record.Key, record.StageID))
	if err != nil || receipt == nil {
		return false, err
	}
	var info pan115.FileInfo
	if err = json.Unmarshal([]byte(receipt.Info), &info); err != nil {
		return true, err
	}
	return true, s.ProcessLocalUpload(ctx, account, record, info)
}

// SetUploadReceipts injects the independent durable handoff before any worker starts.
func (s *StrmService) SetUploadReceipts(repository ports.UploadReceiptRepository) {
	s.uploadReceipts = repository
}

// ProcessLocalUpload persists verified identity before touching local outputs. The upload worker
// remains in cleanup until this succeeds, so restarts and failures cannot delete a move source early.
// Only the upload queue writes UploadRecord; this consumer changes its own completion state.
func (s *StrmService) ProcessLocalUpload(ctx context.Context, account string, record domain.UploadRecord, info pan115.FileInfo) error {
	if s.uploadReceipts == nil {
		return errors.New("上传后处理仓储未配置")
	}
	values, err := s.settings(ctx)
	if err != nil {
		return err
	}
	if values[pan115LocalUploadEnabledKey] != "true" {
		receipt, err := s.uploadReceipts.Get(ctx, journalKey(account, record.Key, info.ID))
		if err != nil {
			return err
		}
		if receipt != nil && receipt.State != "completed" {
			return errors.New("事件同步已关闭，保留尚未完成的本地后处理和源文件")
		}
		return nil
	}
	if s.managedAccount == nil {
		return errors.New("上传后处理账号未配置")
	}
	current, err := s.managedAccount(ctx)
	if err != nil {
		return err
	}
	if current != account || account == "" {
		return errors.New("上传后处理账号发生变化")
	}
	if info.ID == "" || info.ParentID == "" || info.PickCode == "" || info.IsDirectory || info.SHA1 == "" || !strings.EqualFold(info.SHA1, record.SHA1) || info.Size != record.Size || (info.ParentID != "0" && (len(info.Path) == 0 || info.Path[len(info.Path)-1].ID != info.ParentID)) {
		return errors.New("上传回执缺少可信文件身份或祖先路径，保留源文件")
	}
	key := journalKey(account, record.Key, info.ID)
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	receipt, err := s.uploadReceipts.Get(ctx, key)
	if err != nil {
		return err
	}
	if receipt == nil {
		raw, err := json.Marshal(info)
		if err != nil {
			return err
		}
		value := ports.UploadReceipt{Key: key, Account: account, FileID: info.ID, ParentID: info.ParentID, Info: string(raw), LocalRoot: record.LocalRoot, Relative: record.Relative, SHA1: record.SHA1, Size: record.Size, Modified: record.Modified, VerifiedAt: time.Now().Unix(), Rules: s.localUploadRules(values)}
		if err = s.uploadReceipts.Insert(ctx, value); err != nil {
			return err
		}
		receipt, err = s.uploadReceipts.Get(ctx, key)
		if err != nil {
			return err
		}
	}
	if receipt == nil {
		return errors.New("上传回执保存后无法读取")
	}
	// A pending receipt is immutable. Do not replay its old location after a remote move/rename.
	var saved pan115.FileInfo
	if err = json.Unmarshal([]byte(receipt.Info), &saved); err != nil {
		return err
	}
	if saved.ParentID != info.ParentID || saved.Name != info.Name || saved.PickCode != info.PickCode {
		return errors.New("上传回执位置发生变化，停止本地后处理")
	}
	if receipt.State == "completed" {
		return nil
	}
	mappings, err := parseStrmMappings(values[strmPathsSettingKey])
	if err != nil {
		return err
	}
	base := strings.TrimRight(strings.TrimSpace(values[strmPlayBaseSettingKey]), "/")
	if base == "" {
		return errors.New("本地上传后处理需要配置 STRM 播放地址")
	}
	formats, err := strmDownloadFormats(values)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, managedAccountKey{}, account)
	ctx = context.WithValue(ctx, localUploadSourceKey{}, receipt)
	for _, mapping := range mappings {
		if mapping.Kind != domain.StrmKindPan115 {
			continue
		}
		walk, matched := s.eventMappingWalk(mapping, info, formats)
		if !matched {
			continue
		}
		if record.Mode == "move" {
			target, _, err := resolveStrmPath(s.root, mapping.LocalPath)
			if err != nil {
				return err
			}
			if err = walk(ctx, func(file strmSourceFile) error {
				if isStrmMedia(file.Name, formats) {
					output := filepath.Join(target, filepath.FromSlash(file.Directory), file.Name)
					sourceInfo, sourceErr := os.Stat(record.Source)
					outputInfo, outputErr := os.Stat(output)
					if filepath.Clean(output) == filepath.Clean(record.Source) || (sourceErr == nil && outputErr == nil && os.SameFile(sourceInfo, outputInfo)) {
						return errors.New("move 来源与附件输出相同，保留源文件")
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		result := s.scanMapping(ctx, s.root, mapping, base, domain.StrmGenerateIdempotent, walk, func() {}, formats)
		if result.Message != "" || result.Failed > 0 || result.DownloadFailed > 0 {
			return fmt.Errorf("本地上传后处理失败：%s", result.Message)
		}
	}
	// Emby refresh remains batch-driven by event synchronization. Refreshing here per file
	// would enqueue media probing repeatedly and recreate the API burst this handoff avoids.
	return s.uploadReceipts.Complete(ctx, key)
}

// localUploadEventCompleted suppresses only the matching upload event in a bounded receipt window.
// Moves, renames, deletes, unknown accounts and pending work always retain normal event processing.
func (s *StrmService) localUploadEventCompleted(ctx context.Context, event pan115.LifeEvent) (bool, error) {
	if s.uploadReceipts == nil || (event.Type != 1 && event.Type != 2) || event.UpdatedAt == 0 {
		return false, nil
	}
	account, _ := ctx.Value(managedAccountKey{}).(string)
	if account == "" {
		return false, nil
	}
	receipt, err := s.uploadReceipts.Find(ctx, account, event.FileID)
	if err != nil || receipt == nil {
		return false, err
	}
	values, err := s.settings(ctx)
	if err != nil {
		return false, err
	}
	if receipt.Rules != s.localUploadRules(values) {
		return false, nil
	}
	if event.ParentID != receipt.ParentID || event.UpdatedAt < receipt.VerifiedAt-600 || event.UpdatedAt > receipt.VerifiedAt+5 {
		return false, nil
	}
	if receipt.State != "completed" {
		return false, errors.New("本地上传后处理尚未完成，保留事件游标等待重试")
	}
	return true, nil
}
