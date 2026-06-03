package admin

import "time"

type ListRequest struct {
	SourceIDs []string
	SourceID  string
	BindingID string
	Page      int
	PageSize  int
}

type DeletingResourceListResponse struct {
	Items []DeletingResourceResponse `json:"items"`
	Total int                        `json:"total"`
}

type DeletingResourceResponse struct {
	ResourceType string         `json:"resource_type"`
	SourceID     string         `json:"source_id"`
	BindingID    string         `json:"binding_id,omitempty"`
	Status       string         `json:"status"`
	DeletedAt    *time.Time     `json:"deleted_at,omitempty"`
	LastError    map[string]any `json:"last_error,omitempty"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type CompensationListResponse struct {
	Items []CompensationResponse `json:"items"`
	Total int                    `json:"total"`
}

type CompensationResponse struct {
	OperationID        string         `json:"operation_id"`
	SourceID           string         `json:"source_id,omitempty"`
	DatasetID          string         `json:"dataset_id,omitempty"`
	Status             string         `json:"status"`
	CompensationStatus string         `json:"compensation_status"`
	CompensationError  map[string]any `json:"compensation_error,omitempty"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type DeadLetterListResponse struct {
	Items []DeadLetterResponse `json:"items"`
	Total int                  `json:"total"`
}

type DeadLetterResponse struct {
	DeadLetterID      string         `json:"dead_letter_id"`
	TaskID            string         `json:"task_id"`
	SourceID          string         `json:"source_id"`
	BindingID         string         `json:"binding_id"`
	BindingGeneration int64          `json:"binding_generation"`
	ObjectKey         string         `json:"object_key"`
	DocumentID        string         `json:"document_id"`
	TaskAction        string         `json:"task_action"`
	TargetVersionID   string         `json:"target_version_id"`
	RetryCount        int64          `json:"retry_count"`
	ErrorCode         string         `json:"error_code,omitempty"`
	LastError         map[string]any `json:"last_error,omitempty"`
	FailedAt          time.Time      `json:"failed_at"`
	CreatedAt         time.Time      `json:"created_at"`
}

type ReconcileRequest struct {
	RequestID string `json:"request_id,omitempty"`
}

type ReconcileResponse struct {
	RunID             string `json:"run_id"`
	SourceID          string `json:"source_id"`
	BindingID         string `json:"binding_id"`
	BindingGeneration int64  `json:"binding_generation"`
	Status            string `json:"status"`
	TriggerType       string `json:"trigger_type"`
	ScopeType         string `json:"scope_type"`
}
