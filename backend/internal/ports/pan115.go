package ports

import (
	"context"
	"time"
)

// Pan115Account 是已绑定的 115 账号及其令牌快照。
// 令牌字段在写入仓储前已由应用层加密，仓储只做原样存取，不解释其内容。
type Pan115Account struct {
	UserID          string
	UserName        string
	AccessToken     string
	RefreshToken    string
	AccessExpiresAt time.Time
	UpdatedAt       time.Time
}

// Pan115AccountRepository 持久化唯一的 115 账号绑定关系。
// 全表最多一行：绑定即整行覆盖，解绑即删除，不保留历史令牌。
type Pan115AccountRepository interface {
	Load(context.Context) (Pan115Account, bool, error)
	Save(context.Context, Pan115Account) error
	Clear(context.Context) error
}
