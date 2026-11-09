package application

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"bytemuse/backend/internal/ports"
)

// installUploadedMedia applies the admitted upload policy to local attachments.
// Matching content is reused after interrupted completion; keep-both never replaces an occupied name.
func (s *StrmService) installUploadedMedia(ctx context.Context, dir *os.Root, target, relative string, file strmSourceFile, receipt *ports.UploadReceipt) (bool, error) {
	policy, _ := ctx.Value(localUploadPolicyKey{}).(string)
	conflict := func() (bool, error) {
		if marker, ok := ctx.Value(localUploadConflictKey{}).(*atomic.Bool); ok {
			marker.Store(true)
		}
		return false, fmt.Errorf("%w：附件目标 %s", errLocalUploadConflict, relative)
	}
	base, ext := relative, filepath.Ext(relative)
	for index := 0; ; index++ {
		if err := scanCheckpoint(ctx); err != nil {
			return false, err
		}
		if index > 0 {
			relative = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), index, ext)
		}
		stat, err := dir.Lstat(relative)
		if errors.Is(err, os.ErrNotExist) {
			err = s.copyUploadedMedia(ctx, dir, target, relative, file, receipt, false)
			if errors.Is(err, os.ErrExist) {
				index--
				continue
			}
			return false, err
		}
		if err != nil {
			return false, err
		}
		if !stat.Mode().IsRegular() {
			return conflict()
		}
		if policy == "skip" {
			return true, nil
		}
		if stat.Size() == receipt.Size {
			existing, err := dir.Open(relative)
			if err != nil {
				return false, err
			}
			hash := sha1.New()
			count, readErr := io.Copy(hash, &localCopyReader{ctx: ctx, reader: existing})
			closeErr := existing.Close()
			if err = errors.Join(readErr, closeErr); err != nil {
				return false, err
			}
			if count == receipt.Size && strings.EqualFold(fmt.Sprintf("%x", hash.Sum(nil)), receipt.SHA1) {
				// Recover ownership if installation succeeded but receipt/ownership persistence failed.
				return true, s.recordManagedFileAt(ctx, file, filepath.Join(target, relative), filepath.Join(target, relative))
			}
		}
		switch policy {
		case "overwrite":
			return false, s.copyUploadedMedia(ctx, dir, target, relative, file, receipt, true)
		case "keep_both":
			continue
		default:
			// Old records without a policy cannot authorize replacing existing data.
			return conflict()
		}
	}
}

// copyUploadedMedia validates the immutable source version while copying into a rooted temporary
// output. Hash failure never installs a partial file; destination errors remain retryable locally.
func (s *StrmService) copyUploadedMedia(ctx context.Context, dir *os.Root, target, relative string, file strmSourceFile, receipt *ports.UploadReceipt, overwrite bool) error {
	if !filepath.IsLocal(receipt.Relative) || receipt.FileID != file.ID || receipt.SHA1 == "" {
		return errors.New("本地上传来源无效")
	}
	root, err := os.OpenRoot(receipt.LocalRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	// Reject symlinks even within the allowed root; a receipt identifies the original regular file.
	part := ""
	for _, name := range strings.Split(filepath.ToSlash(receipt.Relative), "/") {
		part = filepath.Join(part, name)
		info, err := root.Lstat(part)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("本地上传来源包含符号链接")
		}
	}
	input, err := root.Open(receipt.Relative)
	if err != nil {
		return err
	}
	defer input.Close()
	stat, err := input.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() || stat.Size() != receipt.Size || stat.ModTime().UnixNano() != receipt.Modified {
		return errors.New("本地上传来源已变化，保留后处理任务")
	}
	if err = dir.MkdirAll(filepath.Dir(relative), 0755); err != nil {
		return err
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(relative), fmt.Sprintf(".bytemuse-local-%x.part", nonce))
	output, err := dir.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer dir.Remove(temporary)
	hash := sha1.New()
	n, copyErr := io.Copy(io.MultiWriter(output, hash), &localCopyReader{ctx: ctx, reader: input})
	syncErr := output.Sync()
	closeErr := output.Close()
	if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	if n != receipt.Size || !strings.EqualFold(fmt.Sprintf("%x", hash.Sum(nil)), receipt.SHA1) {
		return errors.New("本地上传来源 SHA1 校验失败")
	}
	current, err := input.Stat()
	if err != nil {
		return err
	}
	if current.Size() != stat.Size() || current.ModTime() != stat.ModTime() {
		return errors.New("复制期间本地上传来源发生变化")
	}
	if err = scanCheckpoint(ctx); err != nil {
		return err
	}
	if !overwrite {
		// Link publishes the complete temporary file without clobbering a concurrently created target.
		if err = dir.Link(temporary, relative); err != nil {
			return err
		}
	} else {
		// An existing directory or symlink never becomes an overwrite candidate.
		current, err := dir.Lstat(relative)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && !current.Mode().IsRegular() {
			return errLocalUploadConflict
		}
	}
	if err = s.recordManagedFileAt(ctx, file, filepath.Join(target, temporary), filepath.Join(target, relative)); err != nil {
		if !overwrite {
			_ = dir.Remove(relative)
		}
		return err
	}
	if !overwrite {
		return nil
	}
	return dir.Rename(temporary, relative)
}

type localCopyReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *localCopyReader) Read(p []byte) (int, error) {
	if err := scanCheckpoint(r.ctx); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
