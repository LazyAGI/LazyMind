package evalset

import "time"

type ImportTemplatePathParams struct {
	FileType string `path:"file_type"`
}

type ImportNormalizedRow struct {
	CaseID            string `json:"case_id"`
	GenerateReason    string `json:"generate_reason"`
	GroundTruth       string `json:"ground_truth"`
	IsDeleted         bool   `json:"is_deleted"`
	KeyPoints         string `json:"key_points"`
	Question          string `json:"question"`
	QuestionType      string `json:"question_type"`
	ReferenceChunkIDs string `json:"reference_chunk_ids"`
	ReferenceContext  string `json:"reference_context"`
	ReferenceDoc      string `json:"reference_doc"`
	ReferenceDocIDs   string `json:"reference_doc_ids"`
}

type ImportPreviewResponse struct {
	ImportToken            string                        `json:"import_token"`
	FileName               string                        `json:"file_name"`
	FileType               string                        `json:"file_type"`
	TotalRows              int64                         `json:"total_rows"`
	EmptyRows              int64                         `json:"empty_rows"`
	ValidRows              int64                         `json:"valid_rows"`
	InvalidRows            int64                         `json:"invalid_rows"`
	PreviewRows            []ImportNormalizedRow         `json:"preview_rows"`
	InvalidPreviewRows     []ImportInvalidPreviewRow     `json:"invalid_preview_rows"`
	ErrorDetails           []ImportValidationErrorDetail `json:"error_details"`
	ErrorsTruncated        bool                          `json:"errors_truncated"`
	InvalidRowsDownloadURL string                        `json:"invalid_rows_download_url,omitempty"`
	ExpiresAt              time.Time                     `json:"expires_at"`
}

type ImportValidationErrorDetail struct {
	Row    int    `json:"row"`
	Column string `json:"column"`
	Reason string `json:"reason"`
}

type ImportInvalidPreviewRow struct {
	Row    int                           `json:"row"`
	Values map[string]string             `json:"values"`
	Errors []ImportValidationErrorDetail `json:"errors"`
}

type ImportValidationErrorResponse struct {
	Errors    []ImportValidationErrorDetail `json:"errors"`
	Truncated bool                          `json:"truncated"`
}

type CreateEvalSetByImportRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	DatasetIDs  []string `json:"dataset_ids"`
	GroupID     string   `json:"group_id"`
	ImportToken string   `json:"import_token"`
}

type CreateEvalSetByImportResponse struct {
	EvalSetID string `json:"eval_set_id"`
	TaskID    string `json:"task_id"`
}

type AppendEvalSetImportRequest struct {
	ImportToken string `json:"import_token"`
}

type AppendEvalSetImportResponse struct {
	TaskID string `json:"task_id"`
}

type EvalSetImportTaskPathParams struct {
	TaskID string `path:"task_id"`
}

type EvalSetImportTaskResponse struct {
	ID              string                        `json:"id"`
	EvalSetID       string                        `json:"eval_set_id"`
	DatasetIDs      []string                      `json:"dataset_ids"`
	DatasetNames    []string                      `json:"dataset_names"`
	Status          string                        `json:"status"`
	FileName        string                        `json:"file_name"`
	FileType        string                        `json:"file_type"`
	TotalRows       int64                         `json:"total_rows"`
	ValidRows       int64                         `json:"valid_rows"`
	InsertedRows    int64                         `json:"inserted_rows"`
	ProgressCurrent int64                         `json:"progress_current"`
	ProgressTotal   int64                         `json:"progress_total"`
	ErrorCode       string                        `json:"error_code"`
	ErrorMessage    string                        `json:"error_message"`
	ErrorDetails    []ImportValidationErrorDetail `json:"error_details"`
	CreatedAt       time.Time                     `json:"created_at"`
	StartedAt       *time.Time                    `json:"started_at"`
	FinishedAt      *time.Time                    `json:"finished_at"`
}
