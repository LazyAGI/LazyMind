package doc

import (
	"context"
	"fmt"
	"mime/multipart"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func validateKnowledgeUploadBatch(files []*multipart.FileHeader) error {
	var total int64
	for _, file := range files {
		if file.Size <= 0 || file.Size > OriginalFileMaxBytes {
			return fmt.Errorf("empty file or upload size limit exceeded")
		}
		total += file.Size
		if total > OriginalBatchMaxBytes {
			return fmt.Errorf("upload batch exceeds 1 GiB")
		}
	}
	return nil
}

// Page upload has a separate start-task action. Commit its original and task
// through the same binding service as MCP, leaving submission to that action.
func bindPageOriginal(ctx context.Context, caller DatasetCatalogCaller, req ImportOriginalRequest) (OriginalImportResult, error) {
	service, err := NewOriginalService(OriginalServiceDeps{DB: store.DB()})
	if err != nil {
		return OriginalImportResult{}, err
	}
	prepared, err := service.PrepareOriginalBinding(ctx, caller, req)
	if err != nil {
		return OriginalImportResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = service.CleanupPreparedBinding(prepared)
		}
	}()
	ctx, err = PrepareResourceAuthorization(ctx, caller, prepared.AuthorizationRequests())
	if err != nil {
		return OriginalImportResult{}, err
	}
	var result OriginalImportResult
	bodyCompleted := false
	err = store.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var bindErr error
		result, bindErr = service.BindOriginalTx(ctx, tx, caller, prepared)
		if bindErr != nil {
			return bindErr
		}
		updated := tx.Model(&orm.UploadedFile{}).Where("upload_file_id = ? AND status = ? AND deleted_at IS NULL", req.FileRef, UploadedFileStateUploaded).
			Updates(map[string]any{"status": UploadedFileStateBound, "task_id": result.Task.ID, "document_id": result.Document.ID, "updated_at": time.Now().UTC()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return resourceErr("CONFLICT", "upload was already bound")
		}
		if err := recordPaperUpload(ctx, tx, result.Task, prepared.source, prepared.metadata.OriginalFilename); err != nil {
			return err
		}
		bodyCompleted = true
		return nil
	})
	committed = err == nil || bodyCompleted
	if err != nil && bodyCompleted {
		return result, resourceErr("UNCONFIRMED", "original binding outcome requires verification before retry")
	}
	return result, err
}
