package application

import (
	"context"
	"fmt"

	"bytemuse/backend/internal/domain"
)

// abortMultipartRecord releases the precise unfinished session before forgetting its identity.
// The owning worker persists the returned record; failure retains the cleanup obligation.
func (s *UploadService) abortMultipartRecord(ctx context.Context, remote uploadRemote, record *domain.UploadRecord) error {
	if record.Multipart == nil {
		return nil
	}
	api, ok := remote.(interface {
		AbortUpload(context.Context, domain.UploadMultipart) error
	})
	if !ok {
		return fmt.Errorf("网盘客户端不支持上传断点清理")
	}
	if err := api.AbortUpload(ctx, *record.Multipart); err != nil {
		return err
	}
	record.Multipart = nil
	return nil
}
