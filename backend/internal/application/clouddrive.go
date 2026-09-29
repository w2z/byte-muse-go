package application

import (
	"context"
	"strings"
	"sync"

	"bytemuse/backend/internal/platform/clouddrive"
)

// CloudDriveSettings 把设置里的 CloudDrive2 连接参数适配成 strm 服务需要的客户端。
// 连接参数可以在运行期修改，因此这里按当前设置懒构造并缓存客户端：
// 参数不变时复用同一个客户端，令牌缓存继续有效；参数变化时立即切换到新连接。
type CloudDriveSettings struct {
	settings func(context.Context) (map[string]string, error)

	mu       sync.Mutex
	base     string
	username string
	password string
	client   *clouddrive.Client
}

// NewCloudDriveSettings 组装一个按设置读取 CloudDrive2 连接参数的适配器。
func NewCloudDriveSettings(settings func(context.Context) (map[string]string, error)) *CloudDriveSettings {
	return &CloudDriveSettings{settings: settings}
}

// connection 读取当前设置并返回可用客户端；未填写地址时返回 ErrNotConfigured。
func (c *CloudDriveSettings) connection(ctx context.Context) (*clouddrive.Client, error) {
	values, err := c.settings(ctx)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSpace(values["CLOUDNAS_URL"])
	if base == "" {
		return nil, clouddrive.ErrNotConfigured
	}
	username := strings.TrimSpace(values["CLOUDNAS_USERNAME"])
	password := values["CLOUDNAS_PASSWORD"]

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil && c.base == base && c.username == username && c.password == password {
		return c.client, nil
	}
	client := clouddrive.New(clouddrive.Config{BaseURL: base, Username: username, Password: password})
	c.client, c.base, c.username, c.password = client, base, username, password
	return client, nil
}

// Configured 报告当前设置是否已具备完整的 CloudDrive2 连接参数。
func (c *CloudDriveSettings) Configured(ctx context.Context) bool {
	client, err := c.connection(ctx)
	return err == nil && client.Configured()
}

// ListSubFiles 列出 CloudDrive2 中某个目录的直接子项。
func (c *CloudDriveSettings) ListSubFiles(ctx context.Context, path string) ([]clouddrive.Entry, error) {
	client, err := c.connection(ctx)
	if err != nil {
		return nil, err
	}
	return client.ListSubFiles(ctx, path)
}

// DownloadURL 把一个文件路径换成可直接播放的地址。
func (c *CloudDriveSettings) DownloadURL(ctx context.Context, path string, direct bool) (clouddrive.Download, error) {
	client, err := c.connection(ctx)
	if err != nil {
		return clouddrive.Download{}, err
	}
	return client.DownloadURL(ctx, path, direct)
}
