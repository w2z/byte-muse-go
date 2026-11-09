package application

import (
	"bytemuse/backend/internal/platform/clouddrive"
	"bytemuse/backend/internal/platform/pan115"
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"
)

// uploadDirectoryRemote shares account-scoped preparation snapshots; verification uses uploadFreshList.
type uploadDirectoryRemote struct {
	uploadRemote
	service *UploadService
	kind    string
}

func (remote uploadDirectoryRemote) List(ctx context.Context, parent string) ([]uploadEntry, error) {
	if remote.kind == "cd2" {
		return remote.uploadRemote.List(ctx, parent)
	}
	return remote.service.directoryCache.list(ctx, uploadDirectoryCacheKey{remote.kind, parent}, time.Time{}, remote.uploadRemote.List)
}

// Upload invalidates pre-write contents on success or uncertainty, but preserves preparation on rejection.
func (remote uploadDirectoryRemote) Upload(ctx context.Context, parent, name string, file *os.File, size int64, hash string) error {
	err := remote.uploadRemote.Upload(ctx, parent, name, file, size, hash)
	if err == nil {
		remote.service.directoryCache.written(remote.kind, parent)
	} else if !uploadDirectoryCooldown(err) {
		remote.service.directoryCache.invalidateParent(remote.kind, parent)
	}
	return err
}

// Delete invalidates cached identities before a post-delete verification can be rate limited.
func (remote uploadDirectoryRemote) Delete(ctx context.Context, parent string, entry uploadEntry) error {
	err := remote.uploadRemote.Delete(ctx, parent, entry)
	if !uploadDirectoryCooldown(err) {
		remote.service.directoryCache.invalidateParent(remote.kind, parent)
	}
	return err
}

func (remote uploadDirectoryRemote) Mkdir(ctx context.Context, parent, name string) (string, error) {
	release, err := remote.service.directoryCache.reserve(ctx, "directory\x00"+remote.kind+"\x00"+parent+"\x00"+name)
	if err != nil {
		return "", err
	}
	defer release()
	entries, err := remote.List(ctx, parent)
	if err != nil {
		return "", err
	}
	if entry, exists := findUploadEntry(entries, name); exists {
		if !entry.Directory {
			return "", fmt.Errorf("网盘路径 %s 已存在同名文件", name)
		}
		return entry.ID, nil
	}
	id, err := remote.uploadRemote.Mkdir(ctx, parent, name)
	if err != nil {
		if isUploadRemoteMissingError(err) {
			remote.service.directoryCache.invalidate(remote.kind)
		} else if !uploadDirectoryCooldown(err) {
			remote.service.directoryCache.invalidateParent(remote.kind, parent)
		}
		return "", err
	}
	if remote.kind != "cd2" {
		remote.service.directoryCache.created(remote.kind, parent, name, id)
	}
	return id, nil
}

// uploadEntry is the provider-neutral identity used by conflict handling and post-write verification.
type uploadEntry struct {
	ID, Name  string
	Size      int64
	Directory bool
	SHA1      string
	Info      *pan115.FileInfo
}

// uploadRemote keeps conflict and retry rules in the application service, outside provider adapters.
type uploadRemote interface {
	List(context.Context, string) ([]uploadEntry, error)
	Mkdir(context.Context, string, string) (string, error)
	Upload(context.Context, string, string, *os.File, int64, string) error
	Rename(context.Context, string, uploadEntry, string) error
	Delete(context.Context, string, uploadEntry) error
}

// uploadReceiptRemote returns provider-confirmed identities, checked before replacing verification reads.
type uploadReceiptRemote interface {
	UploadWithReceipt(context.Context, string, string, *os.File, int64, string) (uploadEntry, error)
}

// UploadWithReceipt merges confirmed identities; uncertain writes invalidate preparation snapshots.
func (remote uploadDirectoryRemote) UploadWithReceipt(ctx context.Context, parent, name string, file *os.File, size int64, hash string) (uploadEntry, error) {
	api, ok := remote.uploadRemote.(uploadReceiptRemote)
	if !ok {
		return uploadEntry{}, remote.Upload(ctx, parent, name, file, size, hash)
	}
	entry, err := api.UploadWithReceipt(ctx, parent, name, file, size, hash)
	if err == nil && validUploadReceipt(entry, parent, name, size, hash) {
		remote.service.directoryCache.uploaded(remote.kind, parent, entry)
	} else if !uploadDirectoryCooldown(err) {
		remote.service.directoryCache.invalidateParent(remote.kind, parent)
	}
	return entry, err
}

// validUploadReceipt requires exact content, provider identity and usable ancestry for local consumers.
func validUploadReceipt(entry uploadEntry, parent, name string, size int64, hash string) bool {
	return entry.ID != "" && !entry.Directory && entry.Name == name && entry.Size == size && entry.SHA1 != "" && strings.EqualFold(entry.SHA1, hash) && entry.Info != nil && !entry.Info.IsDirectory && entry.Info.ID == entry.ID && entry.Info.ParentID == parent && entry.Info.PickCode != "" && entry.Info.SizeKnown && entry.Info.Size == size && entry.Info.Name == name && strings.EqualFold(entry.Info.SHA1, hash) && (parent == "0" || (len(entry.Info.Path) > 0 && entry.Info.Path[len(entry.Info.Path)-1].ID == parent))
}

// uploadPreparationRemote caches only one preparation pass. Shared creation does not imply an empty directory;
// the worker discards this snapshot before transfer and always verifies the final remote object afresh.
type uploadPreparationRemote struct {
	uploadRemote
	entries map[string][]uploadEntry
}

func (remote *uploadPreparationRemote) List(ctx context.Context, parent string) ([]uploadEntry, error) {
	if entries, ok := remote.entries[parent]; ok {
		return entries, nil
	}
	entries, err := remote.uploadRemote.List(ctx, parent)
	if err == nil {
		remote.entries[parent] = entries
	}
	return entries, err
}

func (remote *uploadPreparationRemote) Mkdir(ctx context.Context, parent, name string) (string, error) {
	id, err := remote.uploadRemote.Mkdir(ctx, parent, name)
	if err == nil {
		if _, shared := remote.uploadRemote.(uploadDirectoryRemote); !shared {
			remote.entries[id] = []uploadEntry{}
		}
		if entries, ok := remote.entries[parent]; ok {
			remote.entries[parent] = append(entries, uploadEntry{ID: id, Name: name, Directory: true})
		}
	}
	return id, err
}

type uploadPanAPI interface {
	CreateFolder(context.Context, string, string, string) (string, error)
	Upload(context.Context, string, string, string, *os.File, int64, string) error
	Rename(context.Context, string, string, string) error
	Delete(context.Context, string, string, string) error
}
type uploadPanRemote struct {
	s     *Pan115Service
	api   uploadPanAPI
	paths *uploadDirectoryCache
	scope string
}

// AccountID reads the bound account locally for account-scoped verification and handoff.
func (r uploadPanRemote) AccountID(ctx context.Context) (string, error) {
	return r.s.EventAccountID(ctx)
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
			info := pan115.FileInfo{File: f, Path: append([]pan115.Directory(nil), page.Path...)}
			result = append(result, uploadEntry{ID: f.ID, Name: f.Name, Size: f.Size, Directory: f.IsDirectory, SHA1: f.SHA1, Info: &info})
		}
		if r.paths != nil {
			r.paths.rememberPath(r.scope, parent, page.Path)
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
func (r uploadPanRemote) Upload(ctx context.Context, parent, name string, f *os.File, size int64, sourceSHA1 string) error {
	token, err := r.s.accessToken(ctx, false)
	if err != nil {
		return err
	}
	return r.api.Upload(ctx, token, parent, name, f, size, sourceSHA1)
}

// UploadWithReceipt reuses upload responses and ancestry observed during preparation.
func (r uploadPanRemote) UploadWithReceipt(ctx context.Context, parent, name string, f *os.File, size int64, hash string) (uploadEntry, error) {
	api, ok := r.api.(interface {
		UploadWithReceipt(context.Context, string, string, string, *os.File, int64, string) (pan115.FileInfo, error)
	})
	if !ok {
		return uploadEntry{}, r.Upload(ctx, parent, name, f, size, hash)
	}
	token, err := r.s.accessToken(ctx, false)
	if err != nil {
		return uploadEntry{}, err
	}
	var ancestors []pan115.Directory
	if r.paths != nil {
		ancestors = r.paths.directoryPath(r.scope, parent)
	}
	info, err := api.UploadWithReceipt(ctx, token, parent, name, f, size, hash)
	if err != nil {
		return uploadEntry{}, err
	}
	info.Path = ancestors
	return uploadEntry{ID: info.ID, Name: info.Name, Size: info.Size, SHA1: info.SHA1, Info: &info}, nil
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
		result = append(result, uploadEntry{ID: e.FullPath, Name: e.Name, Size: e.Size, Directory: e.Directory, SHA1: e.SHA1})
	}
	return result, nil
}
func (r uploadCDRemote) Mkdir(ctx context.Context, parent, name string) (string, error) {
	err := r.c.CreateFolder(ctx, parent, name)
	return path.Join(parent, name), err
}
func (r uploadCDRemote) Upload(ctx context.Context, parent, name string, f *os.File, size int64, _ string) error {
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
			return uploadPanRemote{s: pan, api: api}, nil
		}
		c, err := cloud.connection(ctx)
		if err != nil {
			return nil, err
		}
		return uploadCDRemote{c}, nil
	}
}
