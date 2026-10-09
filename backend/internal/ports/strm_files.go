package ports

import "context"

// StrmFileRecord 登记本程序实际写入的文件；路径相对固定根目录，摘要用于拒绝清理外部修改。
// Scope 包含账号与完整映射规则的散列，规则改变后旧记录不会自动参与清理。
type StrmFileRecord struct {
	Key          string
	Scope        string
	FileID       string
	ParentID     string
	Ancestors    string
	RelativePath string
	SHA256       string
}

// StrmFileRepository 持久化文件归属；删除记录必须发生在本地文件处理成功之后。
type StrmFileRepository interface {
	Save(context.Context, StrmFileRecord) error
	List(context.Context, string) ([]StrmFileRecord, error)
	Delete(context.Context, string) error
}
