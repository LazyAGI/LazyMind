package source

import "time"

type AdminListRequest struct {
	SourceIDs []string
	SourceID  string
	BindingID string
	Page      int
	PageSize  int
}

type DeletingResource struct {
	ResourceType string
	SourceID     string
	BindingID    string
	Status       string
	DeletedAt    *time.Time
	LastError    JSON
	UpdatedAt    time.Time
}

type CreateOperationListRequest struct {
	SourceIDs            []string
	Statuses             []string
	CompensationStatuses []string
	Page                 int
	PageSize             int
}

type DeadLetterListRequest struct {
	SourceIDs []string
	SourceID  string
	BindingID string
	Page      int
	PageSize  int
}

type ReconcileRequest struct {
	SourceID  string
	BindingID string
	RequestID string
	RunAt     time.Time
}

type ReconcileResult struct {
	Run SyncRun
}
