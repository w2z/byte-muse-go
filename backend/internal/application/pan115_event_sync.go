package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

// syncPan115Events 合并同一对象的事件并查询当前状态；单文件不扫描整个映射。
// 缺少定位字段或事件超窗时仅补偿生成，绝不推断未出现的删除事件。
func (s *StrmService) syncPan115Events(ctx context.Context, mappings []domain.StrmMapping, base string, values map[string]string, events []pan115.LifeEvent, compensate bool) (domain.StrmScanResult, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	result := domain.StrmScanResult{}
	if s.localUploads.Load() > 0 {
		return result, errors.New("本地上传仍在处理，保留事件游标稍后同步")
	}
	// Load pending recovery once per batch, never scan the receipt table per event.
	if account, _ := ctx.Value(managedAccountKey{}).(string); account != "" && s.uploadReceipts != nil {
		pending, err := s.uploadReceipts.Pending(ctx, account)
		if err != nil {
			return result, err
		}
		if pending == nil {
			pending = []ports.UploadReceipt{}
		}
		ctx = context.WithValue(ctx, localUploadPendingKey{}, pending)
	}
	formats, err := strmDownloadFormats(values)
	if err != nil {
		return result, err
	}
	api, hasInfo := s.pan115.(pan115EventFiles)
	seen := map[string]bool{}
	var eventErrors error
	for _, event := range events {
		if event.FileID != "" && seen[event.FileID] {
			continue
		}
		seen[event.FileID] = true
		if event.Type == 22 {
			deleted, err := s.deletePan115Event(ctx, event, mappings, formats)
			result.Deleted += deleted
			if err != nil {
				eventErrors = errors.Join(eventErrors, err)
			}
			continue
		}
		if event.FileID == "" || !hasInfo {
			compensate = true
			continue
		}
		if completed, err := s.localUploadEventCompleted(ctx, event); err != nil {
			return result, err
		} else if completed {
			continue
		}
		info, err := api.EventFileInfo(ctx, event.FileID)
		if err != nil {
			if errors.Is(err, pan115.ErrFileNotFound) {
				// A confirmed deleted orphan has no remaining output to generate.
				if finishErr := s.completeOrphanLocalUpload(ctx, event.FileID); finishErr != nil {
					eventErrors = errors.Join(eventErrors, finishErr)
				}
				continue
			}
			if ctx.Err() != nil || pan115.IsRateLimitError(err) {
				return result, errors.Join(eventErrors, err)
			}
			eventErrors = errors.Join(eventErrors, err)
			continue
		}
		failed := false
		for _, mapping := range mappings {
			walk, matched := s.eventMappingWalk(mapping, info, formats)
			if !matched {
				continue
			}
			entry := s.scanMapping(ctx, s.root, mapping, base, domain.StrmGenerateIdempotent, walk, func() {}, formats)
			if entry.Message != "" || entry.Failed > 0 || entry.DownloadFailed > 0 {
				eventErrors = errors.Join(eventErrors, fmt.Errorf("115 事件生成失败：%s", entry.Message))
				failed = true
				continue
			}
			result.Files += entry.Files
			result.Created += entry.Created
			result.Downloaded += entry.Downloaded
		}
		if !failed && (event.Type == 1 || event.Type == 2) {
			eventErrors = errors.Join(eventErrors, s.completeOrphanLocalUpload(ctx, event.FileID))
		}
	}
	if eventErrors != nil {
		return result, eventErrors
	}
	if compensate {
		generated, err := s.scanPan115EventMappingsLocked(ctx, mappings, base, values)
		generated.Deleted += result.Deleted
		return generated, err
	}
	result.Emby = s.refreshEmby(ctx, values)
	if result.Emby.Attempted && !result.Emby.Refreshed {
		return result, errors.New("本地文件已同步，但 Emby 刷新失败，请检查配置与连接")
	}
	return result, ctx.Err()
}

// eventMappingWalk 使用当前祖先链定位映射，复用统一过滤与写入流程；目录事件只遍历其子树。
func (s *StrmService) eventMappingWalk(mapping domain.StrmMapping, info pan115.FileInfo, formats []string) (strmWalk, bool) {
	filter := newStrmFileFilter(mapping)
	filter.downloadFormats = formats
	matched := mapping.ID == "0" || mapping.ID == info.ID
	var names, ancestors []string
	for _, directory := range info.Path {
		if directory.ID == mapping.ID {
			matched = true
			names = nil
			ancestors = []string{directory.ID}
			continue
		}
		if matched && directory.ID != "0" {
			names = append(names, directory.Name)
			ancestors = append(ancestors, directory.ID)
		}
	}
	if !matched {
		return nil, false
	}
	rootDirectory := info.IsDirectory && info.ID == mapping.ID
	if rootDirectory {
		names = nil
		ancestors = nil
	}
	for _, name := range names {
		if filter.skipName(name) {
			return nil, false
		}
	}
	if !rootDirectory && filter.skipName(info.Name) {
		return nil, false
	}
	relative := strings.Join(names, "/")
	if !info.IsDirectory {
		if !filter.acceptFile(info.Name, info.Size) {
			return nil, false
		}
		return func(ctx context.Context, visit strmFileVisit) error {
			return visit(strmSourceFile{ID: info.ID, PickCode: info.PickCode, Name: info.Name, Directory: relative, ParentID: info.ParentID, Size: info.Size, SizeKnown: info.SizeKnown, Ancestors: ancestors})
		}, true
	}
	if info.ID == mapping.ID {
		relative = ""
		ancestors = nil
	} else {
		relative = joinStrmRelative(relative, info.Name)
	}
	return func(ctx context.Context, visit strmFileVisit) error {
		return walkPan115Files(ctx, s.pan115, info.ID, filter, func(file strmSourceFile) error {
			file.Directory = joinStrmRelative(relative, file.Directory)
			file.Ancestors = append(append([]string(nil), ancestors...), file.Ancestors...)
			return visit(file)
		})
	}, true
}
