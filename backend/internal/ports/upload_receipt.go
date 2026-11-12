package ports

import "context"

// UploadReceipt is an immutable verified upload handoff. Only State is updated after insertion.
// Info contains the verified remote identity and ancestry, never credentials or download URLs.
type UploadReceipt struct {
	Key, Account, FileID, ParentID  string
	Info, LocalRoot, Relative, SHA1 string
	Rules                           string
	Size, Modified, VerifiedAt      int64
	State                           string
}

// UploadReceiptRepository separates local postprocessing from the upload queue snapshot.
// Insert must preserve an existing receipt; Complete updates only its processing state.
type UploadReceiptRepository interface {
	Insert(context.Context, UploadReceipt) error
	Get(context.Context, string) (*UploadReceipt, error)
	Find(context.Context, string, string) (*UploadReceipt, error)
	Pending(context.Context, string) ([]UploadReceipt, error)
	Complete(context.Context, string) error
}
