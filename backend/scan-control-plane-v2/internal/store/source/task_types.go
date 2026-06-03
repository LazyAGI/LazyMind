package source

import "time"

const (
	ParseTaskStatusPending    = "PENDING"
	ParseTaskStatusRunning    = "RUNNING"
	ParseTaskStatusSubmitted  = "SUBMITTED"
	ParseTaskStatusSucceeded  = "SUCCEEDED"
	ParseTaskStatusFailed     = "FAILED"
	ParseTaskStatusSuperseded = "SUPERSEDED"

	ParseTaskActionCreate  = "CREATE"
	ParseTaskActionReparse = "REPARSE"
	ParseTaskActionDelete  = "DELETE"
)

type ParseTask struct {
	TaskID               string
	TenantID             string
	SourceID             string
	BindingID            string
	BindingGeneration    int64
	ObjectKey            string
	DocumentID           string
	TaskAction           string
	TargetVersionID      string
	SourceVersion        string
	CoreParentDocumentID string
	IdempotencyKey       string
	Status               string
	CoreTaskID           string
	CoreDocumentID       string
	LeaseOwner           string
	LeaseUntil           *time.Time
	RetryCount           int64
	NextRunAt            time.Time
	LastError            JSON
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type ParseTaskDeadLetter struct {
	DeadLetterID      string
	TaskID            string
	TenantID          string
	SourceID          string
	BindingID         string
	BindingGeneration int64
	ObjectKey         string
	DocumentID        string
	TaskAction        string
	TargetVersionID   string
	RetryCount        int64
	ErrorCode         string
	LastError         JSON
	FailedAt          time.Time
	CreatedAt         time.Time
}

type ParseTaskListRequest struct {
	SourceIDs   []string
	SourceID    string
	BindingID   string
	DocumentID  string
	Statuses    []string
	TaskActions []string
	Page        int
	PageSize    int
}

type ParseTaskWithRefs struct {
	Task     ParseTask
	Document *Document
	State    *DocumentState
	Object   *SourceObject
	Source   *Source
	Binding  *Binding
}

type ParseTaskStatsRequest struct {
	SourceIDs  []string
	SourceID   string
	BindingID  string
	DocumentID string
}

type ParseTaskStats struct {
	Total                int64
	ByStatus             map[string]int64
	ByAction             map[string]int64
	RetryableFailedCount int64
}
