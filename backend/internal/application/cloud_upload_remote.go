package application

import (
	"bytemuse/backend/internal/platform/clouddrive"
	"bytemuse/backend/internal/platform/pan115"
	"context"
	"fmt"
	"os"
	"path"
)

// uploadEntry is the provider-neutral identity used by conflict handling and post-write verification.
type uploadEntry struct {
	ID, Name  string
	Size      int64
	Directory bool
	SHA1      string
}

// uploadRemote keeps conflict and retry rules in the application service, outside provider adapters.
type uploadRemote interface {
	List(context.Context, string) ([]uploadEntry, error)
	Mkdir(context.Context, string, string) (string, error)
	Upload(context.Context, string, string, *os.File, int64) error
	Rename(context.Context, string, uploadEntry, string) error
	Delete(context.Context, string, uploadEntry) error
}
type uploadPanAPI interface {
	CreateFolder(context.Context, string, string, string) (string, error)
	Upload(context.Context, string, string, string, *os.File, int64) error
	Rename(context.Context, string, string, string) error
	Delete(context.Context, string, string, string) error
}
type uploadPanRemote struct {
	s   *Pan115Service
	api uploadPanAPI
}

func (r uploadPanRemote) List(ctx context.Context, parent string) ([]uploadEntry, error) {
	var result []uploadEntry
	for offset := 0; ; {
		var page pan115.FilePage
		err := r.s.withToken(ctx, func(token string) error {
			var err error
			page, err = r.s.client.List(ctx, token, parent, offset, pan115FilePageLimit)
			return err
		})
		if err != nil {
			return nil, err
		}
		for _, f := range page.Files {
			result = append(result, uploadEntry{f.ID, f.Name, f.Size, f.IsDirectory, f.SHA1})
		}
		if !page.HasMore {
			return result, nil
		}
		if len(page.Files) == 0 {
			return nil, fmt.Errorf("115 分页未前进")
		}
		offset += len(page.Files)
	}
}
func (r uploadPanRemote) Mkdir(ctx context.Context, parent, name string) (string, error) {
	var id string
	err := r.s.withToken(ctx, func(token string) error { var e error; id, e = r.api.CreateFolder(ctx, token, parent, name); return e })
	return id, err
}
func (r uploadPanRemote) Upload(ctx context.Context, parent, name string, f *os.File, size int64) error {
	token, err := r.s.accessToken(ctx, false)
	if err != nil {
		return err
	}
	return r.api.Upload(ctx, token, parent, name, f, size)
}
func (r uploadPanRemote) Rename(ctx context.Context, parent string, e uploadEntry, name string) error {
	return r.s.withToken(ctx, func(token string) error { return r.api.Rename(ctx, token, e.ID, name) })
}
func (r uploadPanRemote) Delete(ctx context.Context, parent string, e uploadEntry) error {
	return r.s.withToken(ctx, func(token string) error { return r.api.Delete(ctx, token, parent, e.ID) })
}

type uploadCDRemote struct{ c *clouddrive.Client }

func (r uploadCDRemote) List(ctx context.Context, parent string) ([]uploadEntry, error) {
	entries, err := r.c.ListSubFiles(ctx, parent)
	if err != nil {
		return nil, err
	}
	result := make([]uploadEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, uploadEntry{e.FullPath, e.Name, e.Size, e.Directory, e.SHA1})
	}
	return result, nil
}
func (r uploadCDRemote) Mkdir(ctx context.Context, parent, name string) (string, error) {
	err := r.c.CreateFolder(ctx, parent, name)
	return path.Join(parent, name), err
}
func (r uploadCDRemote) Upload(ctx context.Context, parent, name string, f *os.File, size int64) error {
	return r.c.Upload(ctx, parent, name, f, size)
}
func (r uploadCDRemote) Rename(ctx context.Context, parent string, e uploadEntry, name string) error {
	return r.c.Rename(ctx, path.Join(parent, e.Name), name)
}
func (r uploadCDRemote) Delete(ctx context.Context, parent string, e uploadEntry) error {
	return r.c.Delete(ctx, path.Join(parent, e.Name))
}

// uploadProvider builds an adapter using the same account/session configuration as directory browsing.
func uploadProvider(pan *Pan115Service, cloud *CloudDriveSettings) func(context.Context, string) (uploadRemote, error) {
	return func(ctx context.Context, kind string) (uploadRemote, error) {
		if kind == "115" {
			if pan == nil {
				return nil, ErrPan115NotLinked
			}
			api, ok := pan.client.(uploadPanAPI)
			if !ok {
				return nil, fmt.Errorf("115 客户端不支持上传")
			}
			return uploadPanRemote{pan, api}, nil
		}
		c, err := cloud.connection(ctx)
		if err != nil {
			return nil, err
		}
		return uploadCDRemote{c}, nil
	}
}
