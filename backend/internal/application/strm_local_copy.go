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

	"bytemuse/backend/internal/ports"
)

// copyUploadedMedia validates the immutable source version while copying into a rooted temporary
// output. Hash failure never installs a partial file; destination errors remain retryable locally.
func (s *StrmService) copyUploadedMedia(ctx context.Context, dir *os.Root, target, relative string, file strmSourceFile, receipt *ports.UploadReceipt) error {
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
	if err = s.recordManagedFileAt(ctx, file, filepath.Join(target, temporary), filepath.Join(target, relative)); err != nil {
		return err
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
