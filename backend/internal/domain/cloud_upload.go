package domain

import "context"

type uploadProgressKey struct{}

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
// Stage is an owned temporary object; Backup is retained until the replacement has been verified.
type UploadRecord struct {
	// Optional snapshot fields default to zero for records written before recovery controls.
	RemotePath string `json:"remote_path"`
	StageID    string `json:"stage_id"`
	StageSHA1  string `json:"stage_sha1"`
	StageSize  int64  `json:"stage_size"`
	OldID      string `json:"old_id"`
	OldSHA1    string `json:"old_sha1"`
	OldSize    int64  `json:"old_size"`
	Retries    int    `json:"retries"`
	NextRetry  string `json:"next_retry"`
	ErrorKind  string `json:"error_kind"`
	Intent     string `json:"intent"`
	Hidden     bool   `json:"hidden"`
	Kind       string `json:"kind"`
	Root       string `json:"root"`
	LocalRoot  string `json:"local_root"`
	Relative   string `json:"relative"`
	Key        string `json:"key"`
	Source     string `json:"source"`
	Size       int64  `json:"size"`
	Modified   int64  `json:"modified"`
	Policy     string `json:"policy"`
	State      string `json:"state"`
	Parent     string `json:"parent"`
	Target     string `json:"target"`
	Stage      string `json:"stage"`
	Backup     string `json:"backup"`
	SHA1       string `json:"sha1"`
	Error      string `json:"error"`
	UpdatedAt  string `json:"updated_at"`
}
