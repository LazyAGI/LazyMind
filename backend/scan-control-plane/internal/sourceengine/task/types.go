package task

import (
	"context"
	"time"

	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	DefaultMaxObjectsPerGenerateRequest = 20

	TaskActionCreate  = store.ParseTaskActionCreate
	TaskActionReparse = store.ParseTaskActionReparse
	TaskActionDelete  = store.ParseTaskActionDelete

	TaskStatusPending    = store.ParseTaskStatusPending
	TaskStatusRunning    = store.ParseTaskStatusRunning
	TaskStatusSubmitted  = store.ParseTaskStatusSubmitted
	TaskStatusSucceeded  = store.ParseTaskStatusSucceeded
	TaskStatusFailed     = store.ParseTaskStatusFailed
	TaskStatusSuperseded = store.ParseTaskStatusSuperseded

	DocumentParseStatusPending   = "PENDING"
	DocumentParseStatusSucceeded = "SUCCEEDED"
)

type GenerateRequest struct {
	CallerID       string          `json:"-"`
	TenantID       string          `json:"-"`
	RequestID      string          `json:"request_id,omitempty"`
	SourceID       string          `json:"-"`
	BindingID      string          `json:"binding_id,omitempty"`
	ObjectKeys     []string        `json:"object_keys,omitempty"`
	DocumentIDs    []string        `json:"document_ids,omitempty"`
	Paths          []string        `json:"paths,omitempty"`
	Mode           string          `json:"mode,omitempty"`
	Priority       int             `json:"priority,omitempty"`
	TriggerPolicy  string          `json:"trigger_policy,omitempty"`
	UpdatedOnly    bool            `json:"updated_only,omitempty"`
	SelectionToken string          `json:"selection_token,omitempty"`
	Scopes         []GenerateScope `json:"scopes,omitempty"`
}

type GenerateScope struct {
	Key         string `json:"key,omitempty"`
	ObjectKey   string `json:"object_key,omitempty"`
	NodeRef     string `json:"node_ref,omitempty"`
	Path        string `json:"path,omitempty"`
	IsDocument  bool   `json:"is_document,omitempty"`
	IsContainer bool   `json:"is_container,omitempty"`
}

type GeneratePendingRequest struct {
	CallerID  string
	TenantID  string
	SourceID  string
	BindingID string
	RunID     string
	Priority  int
}

type GenerateResult struct {
	RequestedCount     int      `json:"requested_count"`
	AcceptedCount      int      `json:"accepted_count"`
	DuplicateCount     int      `json:"duplicate_count"`
	AlreadyActiveCount int      `json:"already_active_count"`
	SkippedCount       int      `json:"skipped_count"`
	TaskIDs            []string `json:"task_ids"`
	JobID              string   `json:"job_id,omitempty"`
	JobIDs             []string `json:"job_ids,omitempty"`
	RunIDs             []string `json:"run_ids,omitempty"`
	QueuedSyncCount    int      `json:"queued_sync_count,omitempty"`
}

type ManualSyncScheduler interface {
	TriggerSourceSync(ctx context.Context, req sourceengine.TriggerSourceSyncRequest) (sourceengine.TriggerSourceSyncResponse, error)
}

type ExpediteRequest struct {
	CallerID    string   `json:"-"`
	TenantID    string   `json:"-"`
	SourceID    string   `json:"-"`
	BindingID   string   `json:"binding_id,omitempty"`
	TaskIDs     []string `json:"task_ids,omitempty"`
	DocumentIDs []string `json:"document_ids,omitempty"`
	ObjectKeys  []string `json:"object_keys,omitempty"`
	Priority    int      `json:"priority,omitempty"`
}

type ExpediteResult struct {
	UpdatedCount int      `json:"updated_count"`
	SkippedCount int      `json:"skipped_count"`
	TaskIDs      []string `json:"task_ids"`
	SkippedItems []string `json:"skipped_items,omitempty"`
}

type RetryRequest struct {
	CallerID string
	TenantID string
	TaskID   string
	Force    bool
}

type ParseTaskQueryRequest struct {
	CallerID    string
	TenantID    string
	SourceIDs   []string
	SourceID    string
	BindingID   string
	DocumentID  string
	Statuses    []string
	TaskActions []string
	Page        int
	PageSize    int
}

type ParseTaskListResponse struct {
	Items []ParseTaskResponse `json:"items"`
	Total int                 `json:"total"`
}

type ParseTaskResponse struct {
	TaskID               string         `json:"task_id"`
	SourceID             string         `json:"source_id"`
	BindingID            string         `json:"binding_id"`
	ObjectKey            string         `json:"object_key"`
	DocumentID           string         `json:"document_id"`
	DisplayName          string         `json:"display_name,omitempty"`
	TaskAction           string         `json:"task_action"`
	TargetVersionID      string         `json:"target_version_id"`
	SourceVersion        string         `json:"source_version,omitempty"`
	BindingGeneration    int64          `json:"binding_generation"`
	Status               string         `json:"status"`
	CoreTaskID           string         `json:"core_task_id,omitempty"`
	CoreDocumentID       string         `json:"core_document_id,omitempty"`
	CoreParentDocumentID string         `json:"core_parent_document_id,omitempty"`
	LeaseOwner           string         `json:"lease_owner,omitempty"`
	LeaseUntil           *time.Time     `json:"lease_until,omitempty"`
	RetryCount           int64          `json:"retry_count"`
	NextRunAt            time.Time      `json:"next_run_at"`
	LastError            map[string]any `json:"last_error,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type ParseTaskDetailResponse struct {
	Task     ParseTaskResponse      `json:"task"`
	Document *DocumentResponse      `json:"document,omitempty"`
	State    *DocumentStateResponse `json:"state,omitempty"`
	Object   *ObjectResponse        `json:"object,omitempty"`
}

type DocumentResponse struct {
	DocumentID       string    `json:"document_id"`
	SourceID         string    `json:"source_id"`
	BindingID        string    `json:"binding_id"`
	ObjectKey        string    `json:"object_key"`
	CoreDocumentID   string    `json:"core_document_id,omitempty"`
	CurrentVersionID string    `json:"current_version_id,omitempty"`
	DesiredVersionID string    `json:"desired_version_id,omitempty"`
	SourceVersion    string    `json:"source_version,omitempty"`
	DisplayName      string    `json:"display_name,omitempty"`
	ParseStatus      string    `json:"parse_status,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type DocumentStateResponse struct {
	SourceID            string         `json:"source_id"`
	BindingID           string         `json:"binding_id"`
	BindingGeneration   int64          `json:"binding_generation"`
	ObjectKey           string         `json:"object_key"`
	SourceVersion       string         `json:"source_version,omitempty"`
	BaselineVersion     string         `json:"baseline_version,omitempty"`
	SourceState         string         `json:"source_state"`
	SyncState           string         `json:"sync_state"`
	PendingAction       string         `json:"pending_action,omitempty"`
	DocumentListVisible bool           `json:"document_list_visible"`
	Selectable          bool           `json:"selectable"`
	ParseQueueState     string         `json:"parse_queue_state,omitempty"`
	DocumentID          string         `json:"document_id,omitempty"`
	ActiveTaskID        string         `json:"active_task_id,omitempty"`
	LastDetectedAt      *time.Time     `json:"last_detected_at,omitempty"`
	LastSyncedAt        *time.Time     `json:"last_synced_at,omitempty"`
	LastError           map[string]any `json:"last_error,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

type ObjectResponse struct {
	SourceID      string    `json:"source_id"`
	BindingID     string    `json:"binding_id"`
	ObjectKey     string    `json:"object_key"`
	DisplayName   string    `json:"display_name"`
	SourceVersion string    `json:"source_version,omitempty"`
	IsDocument    bool      `json:"is_document"`
	IsContainer   bool      `json:"is_container"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ParseTaskStatsResponse struct {
	Total                int64            `json:"total"`
	ByStatus             map[string]int64 `json:"by_status"`
	ByAction             map[string]int64 `json:"by_action"`
	RetryableFailedCount int64            `json:"retryable_failed_count"`
}

type Planner interface {
	GenerateTasks(ctx context.Context, req GenerateRequest) (GenerateResult, error)
	GeneratePendingTasks(ctx context.Context, req GeneratePendingRequest) (GenerateResult, error)
	ExpediteTasks(ctx context.Context, req ExpediteRequest) (ExpediteResult, error)
	RetryTask(ctx context.Context, req RetryRequest) (ParseTaskDetailResponse, error)
}

type Query interface {
	ListParseTasks(ctx context.Context, req ParseTaskQueryRequest) (ParseTaskListResponse, error)
	GetParseTask(ctx context.Context, taskID string) (ParseTaskDetailResponse, error)
	GetParseTaskStats(ctx context.Context, req ParseTaskQueryRequest) (ParseTaskStatsResponse, error)
}

type QueryStore interface {
	ListParseTasks(ctx context.Context, req store.ParseTaskListRequest) ([]store.ParseTaskWithRefs, int, error)
	GetParseTask(ctx context.Context, taskID string) (store.ParseTaskWithRefs, error)
	GetParseTaskStats(ctx context.Context, req store.ParseTaskStatsRequest) (store.ParseTaskStats, error)
}
