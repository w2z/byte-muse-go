package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// removeUploadedSource runs only after fresh remote ID/size/SHA1 verification. It never removes directories or changed sources.
// A missing source is an idempotent cleanup success after a crash between removal and receipt persistence.
func removeUploadedSource(ctx context.Context, record *domain.UploadRecord) error {
	if record.Mode != "move" || record.SHA1 == "" || record.StageID == "" {
		return errors.New("移动任务缺少已验证的上传凭据")
	}
	relative := filepath.FromSlash(record.Relative)
	if !filepath.IsLocal(relative) || filepath.Clean(filepath.Join(record.LocalRoot, relative)) != filepath.Clean(record.Source) {
		return errors.New("本地文件路径不属于上传目录")
	}
	root, err := os.OpenRoot(record.LocalRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	parts := strings.Split(relative, string(filepath.Separator))
	for index := range parts {
		info, statErr := root.Lstat(filepath.Join(parts[:index+1]...))
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("本地路径已变为符号链接，保留文件")
		}
	}
	file, remove, closeFile, err := uploadRemovalHandle(record.Source, root, relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer closeFile()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || opened.Size() != record.Size || opened.ModTime().UnixNano() != record.Modified {
		return errors.New("本地文件已改变，保留文件")
	}
	hash, err := hashUploadSource(ctx, file, record.Size)
	if err != nil {
		return err
	}
	current, err := root.Lstat(relative)
	if err != nil {
		return err
	}
	if !os.SameFile(opened, current) || !current.Mode().IsRegular() || current.Size() != record.Size || current.ModTime().UnixNano() != record.Modified || !strings.EqualFold(hash, record.SHA1) {
		return errors.New("本地文件内容或身份已改变，保留文件")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = remove(); err != nil {
		return fmt.Errorf("上传成功，本地文件删除失败: %w", err)
	}
	return nil
}
