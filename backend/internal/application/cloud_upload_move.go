package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// removeUploadedSource verifies the source before deletion and prunes only its empty parents below the mapping root.
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
			return pruneUploadedParents(ctx, root, relative)
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
			return pruneUploadedParents(ctx, root, relative)
		}
		return err
	}
	closeSource := sync.OnceFunc(closeFile)
	defer closeSource()
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
	closeSource()
	return pruneUploadedParents(ctx, root, relative)
}

// pruneUploadedParents never traverses outside the rooted mapping or deletes nonempty directories, files or symlinks.
func pruneUploadedParents(ctx context.Context, root *os.Root, relative string) error {
	parts := strings.Split(filepath.Dir(relative), string(filepath.Separator))
	for index := range parts {
		info, err := root.Lstat(filepath.Join(parts[:index+1]...))
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
	}
	for directory := filepath.Dir(relative); directory != "."; directory = filepath.Dir(directory) {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := root.Lstat(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if err := pruneUploadEmptyChildren(ctx, root, directory); err != nil {
			return err
		}
		folder, err := root.Open(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		entries, readErr := folder.ReadDir(1)
		folder.Close()
		if len(entries) > 0 {
			return nil
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if err = removeUploadEmptyDirectory(root, directory); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			folder, openErr := root.Open(directory)
			if openErr == nil {
				entries, _ = folder.ReadDir(1)
				folder.Close()
				if len(entries) > 0 {
					return nil
				}
			}
			return fmt.Errorf("上传成功，空目录清理失败: %w", err)
		}
	}
	return nil
}

// pruneUploadEmptyChildren removes only empty descendants of the uploaded file's parent, never file contents.
func pruneUploadEmptyChildren(ctx context.Context, root *os.Root, directory string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := root.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	folder, err := root.Open(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, err := folder.ReadDir(-1)
	folder.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		child := filepath.Join(directory, entry.Name())
		if err := pruneUploadEmptyChildren(ctx, root, child); err != nil {
			return err
		}
		folder, err := root.Open(child)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		remaining, readErr := folder.ReadDir(1)
		folder.Close()
		if len(remaining) > 0 {
			continue
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if err := removeUploadEmptyDirectory(root, child); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("上传成功，空子目录清理失败: %w", err)
		}
	}
	return nil
}
