package domain

import "context"

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

// UploadOverwriteSnapshot freezes the replacement set before direct upload.
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
	// Overwrite is an optional JSON receipt, never backfilled during startup. Once set, retries cannot extend it.
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
