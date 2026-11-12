package domain

import "context"

// UploadMultipart is one resumable OSS session bound to an immutable source and account.
// Nil means no saved session (including legacy tasks). Access keys and STS tokens are never stored.
// Uploaded parts are queried from OSS on recovery rather than accumulated in this snapshot.
type UploadMultipart struct {
	Account     string `json:"account"`      // 115 账号，必须与当前登录账号一致。
	Parent      string `json:"parent"`       // 目标目录 ID。
	Name        string `json:"name"`         // 初始化时确定的文件名。
	SHA1        string `json:"sha1"`         // 源文件完整摘要。
	Size        int64  `json:"size"`         // 源文件字节数。
	Modified    int64  `json:"modified"`     // 源文件修改时间，Unix 纳秒。
	Bucket      string `json:"bucket"`       // OSS 桶名。
	Object      string `json:"object"`       // 本次上传的 OSS 对象键。
	UploadID    string `json:"upload_id"`    // 未完成分片会话标识。
	PartSize    int64  `json:"part_size"`    // 固定分片字节数，恢复时不得改变。
	Callback    string `json:"callback"`     // 115 签发的完成回调参数，不输出到接口或日志。
	CallbackVar string `json:"callback_var"` // 回调变量，仅供提交原会话使用。
	CreatedAt   int64  `json:"created_at"`   // 会话创建时间，Unix 秒。
}

// UploadMultipartJournal connects the protocol adapter to the owning task's durable snapshot.
// Save must commit before sending file bytes; clearing is owned by the application after a receipt or abort.
type UploadMultipartJournal struct {
	Session  *UploadMultipart
	Account  string
	Modified int64
	Save     func(context.Context, *UploadMultipart) error
}

type uploadMultipartKey struct{}

// WithUploadMultipart binds one task's checkpoint without changing the public upload API.
func WithUploadMultipart(ctx context.Context, journal *UploadMultipartJournal) context.Context {
	return context.WithValue(ctx, uploadMultipartKey{}, journal)
}

// UploadMultipartFromContext returns nil for callers that do not persist upload tasks.
func UploadMultipartFromContext(ctx context.Context) *UploadMultipartJournal {
	journal, _ := ctx.Value(uploadMultipartKey{}).(*UploadMultipartJournal)
	return journal
}

type uploadProgressKey struct{}

// UploadDirective is a durable pause/start command. Sequence is scheduler-ordered, not wall-clock time.
// Empty Scope targets the queue; directory/file Target contains the existing progress key.
type UploadDirective struct {
	Scope         string `json:"scope"`
	Target        string `json:"target"`
	Action        string `json:"action"`
	Sequence      uint64 `json:"sequence"`
	StartSequence uint64 `json:"start_sequence,omitempty"`
}

// UploadOldFile records the immutable identity of one file approved for replacement.
type UploadOldFile struct {
	ID   string `json:"id"`
	SHA1 string `json:"sha1"`
	Size int64  `json:"size"`
}

// UploadOverwriteSnapshot freezes the replacement set for one direct-upload attempt.
// A nil snapshot on UploadRecord denotes a legacy record; an empty Files list means no old files.
type UploadOverwriteSnapshot struct {
	Files []UploadOldFile `json:"files"`
}

// WithUploadProgress attaches a per-file byte reporter; phase distinguishes service-to-CD2 from CD2-to-cloud.
func WithUploadProgress(ctx context.Context, report func(int64, int64, string)) context.Context {
	return context.WithValue(ctx, uploadProgressKey{}, report)
}

// ReportUploadProgress publishes absolute transferred bytes for the current transport phase.
func ReportUploadProgress(ctx context.Context, transferred, total int64, phase string) {
	if report, ok := ctx.Value(uploadProgressKey{}).(func(int64, int64, string)); ok {
		report(transferred, total, phase)
	}
}

// UploadRecord persists one source version and its remote commit stages, making retries and restarts resumable.
// Stage equals Target for direct uploads; StageID identifies the verified final object.
// Distinct Stage and Backup names belong only to legacy replacement recovery.
type UploadRecord struct {
	// Multipart defaults to nil for legacy/completed tasks; only unfinished OSS sessions retain it.
	Multipart *UploadMultipart `json:"multipart,omitempty"`
	// ControlSequence defaults to zero for legacy tasks; ordinary progress updates never advance it.
	ControlSequence uint64 `json:"control_sequence,omitempty"`
	// Directive is present only on control rows, not file receipts; nil preserves legacy semantics.
	Directive *UploadDirective `json:"directive,omitempty"`
	// Optional snapshot fields default to zero for records written before recovery controls.
	RemotePath string `json:"remote_path"`
	StageID    string `json:"stage_id"`
	StageSHA1  string `json:"stage_sha1"`
	StageSize  int64  `json:"stage_size"`
	OldID      string `json:"old_id"`
	OldSHA1    string `json:"old_sha1"`
	OldSize    int64  `json:"old_size"`
	// Overwrite is an optional JSON receipt, never backfilled during startup.
	// Each overwrite attempt refreshes it from current same-name identities before remote mutations.
	Overwrite *UploadOverwriteSnapshot `json:"overwrite_snapshot,omitempty"`
	Retries   int                      `json:"retries"`
	NextRetry string                   `json:"next_retry"`
	ErrorKind string                   `json:"error_kind"`
	Intent    string                   `json:"intent"`
	Hidden    bool                     `json:"hidden"`
	Kind      string                   `json:"kind"`
	Root      string                   `json:"root"`
	LocalRoot string                   `json:"local_root"`
	Relative  string                   `json:"relative"`
	Key       string                   `json:"key"`
	Source    string                   `json:"source"`
	Size      int64                    `json:"size"`
	Modified  int64                    `json:"modified"`
	Policy    string                   `json:"policy"`
	// Mode snapshots copy/move at admission; empty historical records mean copy, never retroactive deletion.
	Mode   string `json:"mode"`
	State  string `json:"state"`
	Parent string `json:"parent"`
	Target string `json:"target"`
	Stage  string `json:"stage"`
	Backup string `json:"backup"`
	SHA1   string `json:"sha1"`
	Error  string `json:"error"`
	// QueuedAt is the UTC RFC3339Nano time a file first entered the queue.
	// It stays fixed across retries and controls. Empty means a record written before ordered queueing; callers use UpdatedAt.
	QueuedAt string `json:"queued_at"`
	// CompletedAt is the UTC RFC3339Nano time a file first reached completed or skipped.
	// Empty means it has not finished successfully; later controls and retries do not overwrite it.
	CompletedAt string `json:"completed_at"`
	UpdatedAt   string `json:"updated_at"`
}
