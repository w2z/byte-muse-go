package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/logging"
	"bytemuse/backend/internal/platform/pan115"
	"bytemuse/backend/internal/ports"
)

type managedScopeKey struct{}
type managedAccountKey struct{}

// writeManagedStrm 先登记完整临时文件，再原子替换；未改写的历史文件不会被接管。
func (s *StrmService) writeManagedStrm(ctx context.Context, source strmSourceFile, absolute, content string) (bool, bool, error) {
	if _, ok := ctx.Value(managedScopeKey{}).(string); !ok {
		return writeStrmFile(absolute, content)
	}
	relative, err := filepath.Rel(s.root, absolute)
	if err != nil || !filepath.IsLocal(relative) {
		return false, false, errors.New("STRM 目标超出根目录")
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return false, false, err
	}
	defer root.Close()
	_, statErr := managedDigest(root, relative)
	missing := errors.Is(statErr, fs.ErrNotExist)
	if statErr != nil && !missing {
		return false, false, statErr
	}
	if !missing {
		existing, err := root.ReadFile(relative)
		if err != nil {
			return false, false, err
		}
		if string(existing) == content {
			return false, false, nil
		}
	}
	if err := root.MkdirAll(filepath.Dir(relative), 0755); err != nil {
		return false, false, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return false, false, err
	}
	temporary := filepath.Join(filepath.Dir(relative), fmt.Sprintf(".bytemuse-strm-%x.part", random))
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return false, false, err
	}
	defer root.Remove(temporary)
	_, writeErr := io.WriteString(file, content)
	closeErr := file.Close()
	if writeErr != nil {
		return false, false, writeErr
	}
	if closeErr != nil {
		return false, false, closeErr
	}
	if err := s.recordManagedFileAt(ctx, source, filepath.Join(s.root, temporary), absolute); err != nil {
		return false, false, err
	}
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if err := root.Rename(temporary, relative); err != nil {
		return false, false, err
	}
	return missing, true, nil
}

// SetManagedFiles 注入持久归属仓储和账号读取器，必须在启动任务前调用。
func (s *StrmService) SetManagedFiles(repository ports.StrmFileRepository, account func(context.Context) (string, error)) {
	s.managedFiles, s.managedAccount = repository, account
}

// managedContext 将账号、根路径和完整过滤规则冻结为归属范围；规则变化不追溯删除历史文件。
func (s *StrmService) managedContext(ctx context.Context, mapping domain.StrmMapping, formats []string) (context.Context, error) {
	if mapping.Kind != domain.StrmKindPan115 || s.managedFiles == nil {
		return ctx, nil
	}
	account, err := s.managedAccount(ctx)
	if err != nil {
		return ctx, err
	}
	if account == "" {
		return ctx, errors.New("无法确认 STRM 文件所属账号")
	}
	frozenAccount := account
	if err := taskInput(ctx, "strm-account", &frozenAccount); err != nil {
		return ctx, err
	}
	if frozenAccount != account {
		return ctx, errors.New("任务断点属于另一个 115 账号，停止生成")
	}
	if expected, _ := ctx.Value(managedAccountKey{}).(string); expected != "" && expected != account {
		return ctx, errors.New("115 账号发生变化，停止文件同步")
	}
	raw, err := json.Marshal([]any{account, s.root, mapping, formats})
	if err != nil {
		return ctx, err
	}
	ctx = context.WithValue(ctx, managedAccountKey{}, account)
	return context.WithValue(ctx, managedScopeKey{}, fmt.Sprintf("%x", sha256.Sum256(raw))), nil
}

// recordManagedFileAt 在原子替换前持久化临时文件摘要，避免写盘成功但落库失败后永久丢失归属。
func (s *StrmService) recordManagedFileAt(ctx context.Context, source strmSourceFile, input, absolute string) error {
	scope, _ := ctx.Value(managedScopeKey{}).(string)
	if scope == "" {
		return nil
	}
	account, err := s.managedAccount(ctx)
	if err != nil {
		return err
	}
	if expected, _ := ctx.Value(managedAccountKey{}).(string); expected != account {
		return errors.New("115 账号发生变化，停止记录文件")
	}
	relative, err := filepath.Rel(s.root, absolute)
	if err != nil || !filepath.IsLocal(relative) {
		return errors.New("受管文件超出 STRM 根目录")
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	inputRelative, err := filepath.Rel(s.root, input)
	if err != nil {
		return err
	}
	digest, err := managedDigest(root, inputRelative)
	if err != nil {
		return err
	}
	ancestors, err := json.Marshal(source.Ancestors)
	if err != nil {
		return err
	}
	return s.managedFiles.Save(ctx, ports.StrmFileRecord{Key: journalKey(s.root, relative), Scope: scope, FileID: source.ID, ParentID: source.ParentID, Ancestors: string(ancestors), RelativePath: relative, SHA256: digest})
}

// managedDigest 拒绝任何一级符号链接，并在受限根内读取普通文件摘要。
func managedDigest(root *os.Root, relative string) (string, error) {
	if !filepath.IsLocal(relative) {
		return "", errors.New("受管文件路径无效")
	}
	part := ""
	for _, segment := range strings.Split(filepath.ToSlash(relative), "/") {
		part = filepath.Join(part, segment)
		info, err := root.Lstat(part)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("受管路径包含符号链接，停止清理")
		}
	}
	file, err := root.Open(relative)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("受管路径不是普通文件")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// removeManagedFile 仅移除摘要匹配的文件，保留外部改动；数据库失败可在下轮幂等重试。
func (s *StrmService) removeManagedFile(ctx context.Context, item ports.StrmFileRecord, mapping domain.StrmMapping) (bool, error) {
	target, _, err := resolveStrmPath(s.root, mapping.LocalPath)
	if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(target, filepath.Join(s.root, item.RelativePath))
	if err != nil || !filepath.IsLocal(relative) || !filepath.IsLocal(item.RelativePath) {
		return false, errors.New("清理路径超出映射目录")
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return false, err
	}
	defer root.Close()
	digest, err := managedDigest(root, item.RelativePath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, s.managedFiles.Delete(ctx, item.Key)
	}
	if err != nil {
		return false, err
	}
	if digest != item.SHA256 {
		logging.Default.Info(logging.CategoryPan115Event, "本地文件已被修改，保留文件", "path", item.RelativePath)
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if expected, _ := ctx.Value(managedAccountKey{}).(string); expected != "" {
		current, err := s.managedAccount(ctx)
		if err != nil {
			return false, err
		}
		if current != expected {
			return false, errors.New("115 账号发生变化，停止清理")
		}
	}
	if err := root.Remove(item.RelativePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := s.managedFiles.Delete(ctx, item.Key); err != nil {
		return true, err
	}
	boundary, _ := filepath.Rel(s.root, target)
	for directory := filepath.Dir(item.RelativePath); directory != boundary && directory != "."; directory = filepath.Dir(directory) {
		if err := root.Remove(directory); err != nil {
			break
		}
	}
	return true, nil
}

// EventFileInfo 读取当前网盘状态；不存在或无权限的结果不能单独作为本地删除依据。
func (s *Pan115Service) EventFileInfo(ctx context.Context, id string) (pan115.FileInfo, error) {
	var info pan115.FileInfo
	err := s.withToken(ctx, func(token string) error { var err error; info, err = s.client.Info(ctx, token, id); return err })
	return info, err
}

type pan115EventFiles interface {
	EventFileInfo(context.Context, string) (pan115.FileInfo, error)
}

// confirmDeleted 同时要求删除事件、详情不存在以及父目录完整列举不含目标；网络/权限错误不当作删除。
func (s *StrmService) confirmDeleted(ctx context.Context, event pan115.LifeEvent) (bool, error) {
	api, ok := s.pan115.(pan115EventFiles)
	if !ok {
		return false, errors.New("115 客户端不支持删除状态核验")
	}
	_, err := api.EventFileInfo(ctx, event.FileID)
	if err == nil {
		return false, errors.New("删除事件对应对象仍存在，保留文件并稍后核验")
	}
	if !errors.Is(err, pan115.ErrFileNotFound) {
		return false, err
	}
	if event.ParentID == "" || event.ParentID == event.FileID {
		return false, errors.New("删除事件缺少有效父目录，保留本地文件")
	}
	for offset := 0; ; {
		page, err := s.pan115.Files(ctx, event.ParentID, offset, strmListLimit)
		if err != nil {
			return false, err
		}
		for _, file := range page.Files {
			if file.ID == event.FileID {
				return false, errors.New("删除事件对应对象仍在父目录，稍后重试")
			}
		}
		if !page.HasMore {
			return true, nil
		}
		if len(page.Files) == 0 {
			return false, errors.New("删除核验分页不完整")
		}
		offset += len(page.Files)
	}
}

// deletePan115Event 只处理当前映射内已登记的目标及其后代；不会根据文件名连带清理附件。
func (s *StrmService) deletePan115Event(ctx context.Context, event pan115.LifeEvent, mappings []domain.StrmMapping, formats []string) (int, error) {
	if s.managedFiles == nil {
		return 0, errors.New("STRM 文件归属仓储未配置")
	}
	if event.FileID == "" {
		return 0, errors.New("删除事件缺少文件 ID，保留本地文件")
	}
	type candidate struct {
		item    ports.StrmFileRecord
		mapping domain.StrmMapping
	}
	var candidates []candidate
	for _, mapping := range mappings {
		scoped, err := s.managedContext(ctx, mapping, formats)
		if err != nil {
			return 0, err
		}
		scope, _ := scoped.Value(managedScopeKey{}).(string)
		items, err := s.managedFiles.List(ctx, scope)
		if err != nil {
			return 0, err
		}
		for _, item := range items {
			var ancestors []string
			if err := json.Unmarshal([]byte(item.Ancestors), &ancestors); err != nil {
				return 0, err
			}
			match := item.FileID == event.FileID
			for _, id := range ancestors {
				if id == event.FileID {
					match = true
				}
			}
			if match {
				candidates = append(candidates, candidate{item, mapping})
			}
		}
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	confirmed, err := s.confirmDeleted(ctx, event)
	if err != nil || !confirmed {
		return 0, err
	}
	deleted := 0
	for _, candidate := range candidates {
		if candidate.item.FileID != event.FileID {
			api := s.pan115.(pan115EventFiles)
			_, err := api.EventFileInfo(ctx, candidate.item.FileID)
			if err == nil {
				continue
			}
			if !errors.Is(err, pan115.ErrFileNotFound) {
				return deleted, err
			}
		}
		removed, err := s.removeManagedFile(ctx, candidate.item, candidate.mapping)
		if removed {
			deleted++
		}
		if err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}
