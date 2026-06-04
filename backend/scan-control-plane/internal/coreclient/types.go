package coreclient

import (
	"context"
	"errors"
	"fmt"
	"io"
)

const (
	ActionCreate  = "CREATE"
	ActionReparse = "REPARSE"
	ActionDelete  = "DELETE"

	StatusSubmitted = "SUBMITTED"
	StatusSucceeded = "SUCCEEDED"

	ResultStatusRunning   = "RUNNING"
	ResultStatusSucceeded = "SUCCEEDED"
	ResultStatusFailed    = "FAILED"
	ResultStatusNotFound  = "NOT_FOUND"
)

const (
	ErrCodeCoreSubmitFailed     = "CORE_SUBMIT_FAILED"
	ErrCodeIdempotencyKeyReused = "IDEMPOTENCY_KEY_REUSED"
)

type Error struct {
	Code       string
	Message    string
	StatusCode int
	Body       map[string]any
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("core request failed with status %d", e.StatusCode)
}

func ErrorCodeOf(err error) string {
	var coreErr *Error
	if errors.As(err, &coreErr) {
		return coreErr.Code
	}
	return ""
}

type Client interface {
	SubmitParseTask(ctx context.Context, req SubmitParseTaskRequest) (SubmitParseTaskResponse, error)
	GetCoreTaskResult(ctx context.Context, req GetCoreTaskResultRequest) (CoreTaskResult, error)
}

type ResourceClient interface {
	CreateDataset(ctx context.Context, req CreateDatasetRequest) (CreateDatasetResponse, error)
	CreateBindingRootDocument(ctx context.Context, req CreateBindingRootDocumentRequest) (CreateBindingRootDocumentResponse, error)
	DeleteDocument(ctx context.Context, req DeleteDocumentRequest) error
	BatchDeleteDocuments(ctx context.Context, req BatchDeleteDocumentsRequest) error
}

type DatasetDeletionClient interface {
	DeleteDataset(ctx context.Context, req DeleteDatasetRequest) error
}

type DatasetAlgo struct {
	AlgoID      string `json:"algo_id,omitempty"`
	Description string `json:"description,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type CreateDatasetRequest struct {
	IdempotencyKey string       `json:"idempotency_key"`
	Name           string       `json:"name"`
	DisplayName    string       `json:"display_name,omitempty"`
	CreatedBy      string       `json:"created_by"`
	TenantID       string       `json:"tenant_id,omitempty"`
	Algo           *DatasetAlgo `json:"algo,omitempty"`
}

type CreateDatasetResponse struct {
	DatasetID string `json:"dataset_id"`
	Created   bool   `json:"created"`
}

type DeleteDatasetRequest struct {
	DatasetID string `json:"dataset_id"`
	UserID    string `json:"user_id,omitempty"`
}

type CreateBindingRootDocumentRequest struct {
	IdempotencyKey   string `json:"idempotency_key"`
	DatasetID        string `json:"dataset_id"`
	ParentDocumentID string `json:"parent_document_id,omitempty"`
	Name             string `json:"name"`
	UserID           string `json:"user_id,omitempty"`
}

type CreateBindingRootDocumentResponse struct {
	DocumentID string `json:"document_id"`
	Created    bool   `json:"created"`
}

type DeleteDocumentRequest struct {
	DatasetID  string `json:"dataset_id"`
	DocumentID string `json:"document_id"`
	UserID     string `json:"user_id,omitempty"`
}

type BatchDeleteDocumentsRequest struct {
	DatasetID   string   `json:"dataset_id"`
	DocumentIDs []string `json:"document_ids"`
	UserID      string   `json:"user_id,omitempty"`
}

type SubmitParseTaskRequest struct {
	IdempotencyKey   string    `json:"idempotency_key"`
	DatasetID        string    `json:"dataset_id"`
	ParentDocumentID string    `json:"parent_document_id"`
	SourceDocumentID string    `json:"source_document_id,omitempty"`
	UserID           string    `json:"user_id,omitempty"`
	DisplayName      string    `json:"display_name"`
	ContentURI       string    `json:"content_uri,omitempty"`
	Content          io.Reader `json:"-"`
	MimeType         string    `json:"mime_type"`
	FileExtension    string    `json:"file_extension"`
	Action           string    `json:"action"`
}

type SubmitParseTaskResponse struct {
	CoreTaskID     string `json:"core_task_id"`
	CoreDocumentID string `json:"core_document_id"`
	Status         string `json:"status"`
	VersionID      string `json:"version_id"`
	Created        bool   `json:"created"`
}

type GetCoreTaskResultRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	DatasetID      string `json:"dataset_id"`
	CoreTaskID     string `json:"core_task_id"`
	UserID         string `json:"user_id,omitempty"`
}

type CoreTaskResult struct {
	Status         string `json:"status"`
	CoreDocumentID string `json:"core_document_id"`
	VersionID      string `json:"version_id"`
	ErrorCode      string `json:"error_code"`
	ErrorMessage   string `json:"error_message"`
}
