package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// PaperUploadContext associates the existing single-PDF task with one import
// operation. It does not change the normal document upload execution path.
type PaperUploadContext struct {
	IdempotencyKey string `json:"idempotency_key"`
	LibraryPaperID string `json:"library_paper_id"`
	Title          string `json:"title"`
}
type paperUploadKey struct{}
type paperUploadScope struct {
	PaperUploadContext
	DatasetID, UserID, BatchID string
}

func preparePaperUpload(r *http.Request, dataset *orm.Dataset, req CreateTaskRequest) (*http.Request, string, *orm.Task, error) {
	if req.PaperImport == nil {
		return r, "", nil, nil
	}
	input := *req.PaperImport
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Title = strings.TrimSpace(input.Title)
	if len(req.Items) != 1 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || input.LibraryPaperID == "" || len(input.LibraryPaperID) > 255 || input.Title == "" || len(input.Title) > 4096 {
		return r, "", nil, errors.New("invalid paper upload context")
	}
	if dataset.Scene != "academic_papers" {
		return r, "", nil, errors.New("paper upload requires a paper library")
	}
	item := req.Items[0]
	if item.UploadFileID == "" && item.ContentHash == "" || item.Task.TaskType != "" && item.Task.TaskType != TaskTypeParseUploaded {
		return r, "", nil, errors.New("paper upload requires an uploaded PDF")
	}
	scope := paperUploadScope{PaperUploadContext: input, DatasetID: dataset.ID, UserID: store.UserID(r)}
	scope.BatchID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("paper-upload:"+scope.UserID+":"+input.IdempotencyKey)).String()
	r = r.WithContext(context.WithValue(r.Context(), paperUploadKey{}, scope))
	task, err := existingPaperUpload(store.DB().WithContext(r.Context()), scope)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r, scope.BatchID, nil, nil
	}
	if err != nil {
		return r, "", nil, err
	}
	return r, scope.BatchID, &task, nil
}
func existingPaperUpload(db *gorm.DB, scope paperUploadScope) (orm.Task, error) {
	var batch orm.PaperImportBatch
	var task orm.Task
	if err := db.Where("id=? AND created_by=?", scope.BatchID, scope.UserID).Take(&batch).Error; err != nil {
		return task, err
	}
	if batch.TargetDatasetID != scope.DatasetID || batch.EntryType != "pdf_upload" {
		return task, errors.New("paper upload request key is already in use")
	}
	var item orm.PaperImportItem
	if err := db.Where("batch_id=?", batch.ID).Take(&item).Error; err != nil {
		return task, err
	}
	if item.LibraryPaperID != scope.LibraryPaperID || item.TitleSnapshot != scope.Title {
		return task, errors.New("paper upload request does not match its original context")
	}
	err := db.Where("id=? AND dataset_id=? AND deleted_at IS NULL", item.DocumentTaskID, scope.DatasetID).Take(&task).Error
	return task, err
}
func recordPaperUpload(ctx context.Context, tx *gorm.DB, task orm.Task, uploaded orm.UploadedFile, name string) error {
	scope, ok := ctx.Value(paperUploadKey{}).(paperUploadScope)
	if !ok {
		return nil
	}
	if !strings.EqualFold(filepath.Ext(name), ".pdf") {
		return errors.New("paper upload only supports PDF")
	}
	now := time.Now().UTC()
	source, _ := json.Marshal(map[string]string{"file_name": name})
	batch := orm.PaperImportBatch{ID: scope.BatchID, EntryType: "pdf_upload", TargetDatasetID: scope.DatasetID, SourceContextJSON: source, SourceDocumentIDsJSON: json.RawMessage(`[]`), PolicySnapshotJSON: json.RawMessage(`{}`), Status: "succeeded", TotalItems: 1, CompletedItems: 1, IdempotencyKey: scope.IdempotencyKey, CreatedBy: scope.UserID, CreatedAt: now, UpdatedAt: now}
	item := orm.PaperImportItem{ID: uuid.NewString(), BatchID: batch.ID, TitleSnapshot: scope.Title, LibraryPaperID: scope.LibraryPaperID, ResultKind: "imported", Status: "succeeded", Stage: "done", DocumentID: task.DocID, DocumentTaskID: task.ID, ContentSHA256: uploaded.ContentHash, ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{"source_provider":"local"}`), ErrorDetailsJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := tx.Create(&batch).Error; err != nil {
		return fmt.Errorf("record paper upload: %w", err)
	}
	return tx.Create(&item).Error
}
