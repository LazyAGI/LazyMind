package source

import (
	"context"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	scheduleengine "github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	SourceStatusActive = "ACTIVE"

	BindingStatusActive   = "ACTIVE"
	BindingStatusPaused   = "PAUSED"
	BindingStatusDeleting = "DELETING"

	SyncModeManual    = "manual"
	SyncModeScheduled = "scheduled"
	SyncModeWatch     = "watch"

	OperationStatusPending              = "PENDING"
	OperationStatusSucceeded            = "SUCCEEDED"
	OperationStatusSucceededWithWarning = "SUCCEEDED_WITH_WARNING"
	OperationStatusFailed               = "FAILED"

	CompensationStatusNone      = "NONE"
	CompensationStatusSucceeded = "SUCCEEDED"
	CompensationStatusFailed    = "FAILED"
)

type Engine interface {
	CreateSource(ctx context.Context, req CreateSourceRequest) (CreateSourceResponse, error)
	ListSources(ctx context.Context, req ListSourcesRequest) (ListSourcesResponse, error)
	GetSource(ctx context.Context, req GetSourceRequest) (GetSourceResponse, error)
	GetSourceSummary(ctx context.Context, req SourceSummaryRequest) (SourceSummaryResponse, error)
	TriggerSourceSync(ctx context.Context, req TriggerSourceSyncRequest) (TriggerSourceSyncResponse, error)
	UpdateSource(ctx context.Context, callerID, sourceID string, req UpdateSourceRequest) (UpdateSourceResponse, error)
	DeleteSource(ctx context.Context, sourceID string) (DeleteSourceResponse, error)
	DeleteSourceByDatasetID(ctx context.Context, datasetID string, opts DeleteSourceOptions) (DeleteSourceResponse, error)
	AddBinding(ctx context.Context, callerID, sourceID string, input BindingInput) (BindingMutationResponse, error)
	UpdateBinding(ctx context.Context, callerID, sourceID, bindingID string, input BindingInput) (BindingMutationResponse, error)
	DeleteBinding(ctx context.Context, sourceID, bindingID string) (DeleteBindingResponse, error)
}

type CreateSourceRequest struct {
	CallerID          string         `json:"-"`
	TenantID          string         `json:"-"`
	RequestID         string         `json:"request_id"`
	Name              string         `json:"name"`
	Bindings          []BindingInput `json:"bindings"`
	IncludeExtensions []string       `json:"include_extensions,omitempty"`
	ExcludeExtensions []string       `json:"exclude_extensions,omitempty"`
	SourceOptions     map[string]any `json:"source_options,omitempty"`
}

type UpdateSourceRequest struct {
	ConfigVersion     int64          `json:"config_version"`
	Name              *string        `json:"name,omitempty"`
	Bindings          []BindingInput `json:"bindings,omitempty"`
	BindingsProvided  bool           `json:"-"`
	IncludeExtensions []string       `json:"include_extensions,omitempty"`
	ExcludeExtensions []string       `json:"exclude_extensions,omitempty"`
	SourceOptions     map[string]any `json:"source_options,omitempty"`
}

type BindingInput struct {
	BindingID         string                  `json:"binding_id,omitempty"`
	ConnectorType     connector.ConnectorType `json:"connector_type,omitempty"`
	TargetType        connector.TargetType    `json:"target_type,omitempty"`
	TargetRef         string                  `json:"target_ref,omitempty"`
	DisplayName       string                  `json:"display_name,omitempty"`
	AgentID           string                  `json:"agent_id,omitempty"`
	AuthConnectionID  string                  `json:"auth_connection_id,omitempty"`
	ProviderOptions   map[string]any          `json:"provider_options,omitempty"`
	SyncMode          string                  `json:"sync_mode,omitempty"`
	SchedulePolicy    store.JSON              `json:"schedule_policy,omitempty"`
	IncludeExtensions []string                `json:"include_extensions,omitempty"`
	ExcludeExtensions []string                `json:"exclude_extensions,omitempty"`
	Status            string                  `json:"status,omitempty"`
}

type ListSourcesRequest struct {
	CallerID  string
	TenantID  string
	SourceIDs []string
	Keyword   string
	Status    string
	Page      int
	PageSize  int
}

type GetSourceRequest struct {
	CallerID        string
	TenantID        string
	SourceID        string
	IncludeBindings bool
	IncludeSummary  bool
}

type TriggerSourceSyncRequest struct {
	CallerID  string         `json:"-"`
	TenantID  string         `json:"-"`
	RequestID string         `json:"request_id"`
	SourceID  string         `json:"-"`
	BindingID string         `json:"binding_id,omitempty"`
	ScopeType string         `json:"scope_type,omitempty"`
	ScopeRef  map[string]any `json:"scope_ref,omitempty"`
}

type SourceSummaryRequest struct {
	CallerID  string
	TenantID  string
	SourceID  string
	BindingID string
}

type SourceResponse struct {
	SourceID          string         `json:"source_id"`
	TenantID          string         `json:"tenant_id,omitempty"`
	CreatedBy         string         `json:"created_by,omitempty"`
	Name              string         `json:"name"`
	DatasetID         string         `json:"dataset_id"`
	Status            string         `json:"status"`
	SourceOptions     map[string]any `json:"source_options,omitempty"`
	IncludeExtensions []string       `json:"include_extensions,omitempty"`
	ExcludeExtensions []string       `json:"exclude_extensions,omitempty"`
	ConfigVersion     int64          `json:"config_version"`
	DeletedAt         *time.Time     `json:"deleted_at,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type SourceBindingResponse struct {
	BindingID              string         `json:"binding_id"`
	SourceID               string         `json:"source_id"`
	ConnectorType          string         `json:"connector_type"`
	TargetType             string         `json:"target_type"`
	TargetRef              string         `json:"target_ref"`
	TargetFingerprint      string         `json:"target_fingerprint,omitempty"`
	AgentID                string         `json:"agent_id,omitempty"`
	AuthConnectionID       string         `json:"auth_connection_id,omitempty"`
	ProviderOptions        map[string]any `json:"provider_options,omitempty"`
	TreeKey                string         `json:"tree_key"`
	BindingGeneration      int64          `json:"binding_generation"`
	CoreParentDocumentID   string         `json:"core_parent_document_id"`
	CoreParentDocumentName string         `json:"core_parent_document_name"`
	SyncMode               string         `json:"sync_mode"`
	SchedulePolicy         store.JSON     `json:"schedule_policy,omitempty"`
	NextSyncAt             *time.Time     `json:"next_sync_at,omitempty"`
	IncludeExtensions      []string       `json:"include_extensions,omitempty"`
	ExcludeExtensions      []string       `json:"exclude_extensions,omitempty"`
	Status                 string         `json:"status"`
	LastError              map[string]any `json:"last_error,omitempty"`
	DeletedAt              *time.Time     `json:"deleted_at,omitempty"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
}

type CreateSourceResponse struct {
	Source    SourceResponse          `json:"source"`
	Bindings  []SourceBindingResponse `json:"bindings"`
	JobIDs    []string                `json:"job_ids,omitempty"`
	JobErrors []JobError              `json:"job_errors,omitempty"`
}

type ListSourcesResponse struct {
	Items []SourceListItemResponse `json:"items"`
	Total int                      `json:"total"`
}

type SourceListItemResponse struct {
	SourceID             string                        `json:"source_id"`
	TenantID             string                        `json:"tenant_id,omitempty"`
	CreatedBy            string                        `json:"created_by,omitempty"`
	Name                 string                        `json:"name"`
	DatasetID            string                        `json:"dataset_id"`
	Status               string                        `json:"status"`
	SourceOptions        map[string]any                `json:"source_options,omitempty"`
	IncludeExtensions    []string                      `json:"include_extensions,omitempty"`
	ExcludeExtensions    []string                      `json:"exclude_extensions,omitempty"`
	ConfigVersion        int64                         `json:"config_version"`
	BindingCount         int                           `json:"binding_count"`
	AuthConnectionStatus *AuthConnectionStatusResponse `json:"auth_connection_status,omitempty"`
	Summary              map[string]any                `json:"summary,omitempty"`
	DeletedAt            *time.Time                    `json:"deleted_at,omitempty"`
	CreatedAt            time.Time                     `json:"created_at"`
	UpdatedAt            time.Time                     `json:"updated_at"`
}

type AuthConnectionStatusResponse struct {
	Status        string   `json:"status"`
	ConnectionIDs []string `json:"connection_ids"`
}

type AuthConnectionStatusRequest struct {
	ConnectionIDs []string
	UserID        string
	TenantID      string
}

type AuthConnectionStatus struct {
	ConnectionID string
	Status       string
	LastError    string
}

type GetSourceResponse struct {
	Source   SourceResponse          `json:"source"`
	Bindings []SourceBindingResponse `json:"bindings,omitempty"`
	Summary  map[string]any          `json:"summary,omitempty"`
}

type TriggerSourceSyncResponse struct {
	RunIDs  []string                `json:"run_ids"`
	JobIDs  []string                `json:"job_ids"`
	Intents []SyncRunIntentResponse `json:"intents,omitempty"`
}

type SyncRunIntentResponse struct {
	RunID             string         `json:"run_id"`
	JobID             string         `json:"job_id,omitempty"`
	SourceID          string         `json:"source_id"`
	BindingID         string         `json:"binding_id"`
	BindingGeneration int64          `json:"binding_generation"`
	Status            string         `json:"status"`
	TriggerType       string         `json:"trigger_type"`
	ScopeType         string         `json:"scope_type"`
	ScopeRef          map[string]any `json:"scope_ref,omitempty"`
	Created           bool           `json:"created"`
}

type SourceSummaryResponse struct {
	SourceID            string                  `json:"source_id"`
	BindingID           string                  `json:"binding_id,omitempty"`
	TotalObjects        int64                   `json:"total_objects"`
	DocumentObjects     int64                   `json:"document_objects"`
	ContainerObjects    int64                   `json:"container_objects"`
	NewCount            int64                   `json:"new_count"`
	ModifiedCount       int64                   `json:"modified_count"`
	DeletedCount        int64                   `json:"deleted_count"`
	UnchangedCount      int64                   `json:"unchanged_count"`
	PendingTaskCount    int64                   `json:"pending_task_count"`
	RunningTaskCount    int64                   `json:"running_task_count"`
	SubmittedTaskCount  int64                   `json:"submitted_task_count"`
	FailedTaskCount     int64                   `json:"failed_task_count"`
	SucceededTaskCount  int64                   `json:"succeeded_task_count"`
	SupersededTaskCount int64                   `json:"superseded_task_count"`
	ParsedDocumentCount int64                   `json:"parsed_document_count"`
	StorageBytes        int64                   `json:"storage_bytes"`
	LastSuccessAt       *time.Time              `json:"last_success_at,omitempty"`
	LastError           map[string]any          `json:"last_error,omitempty"`
	Bindings            []SourceSummaryResponse `json:"bindings,omitempty"`
}

type UpdateSourceResponse struct {
	Source            SourceResponse          `json:"source"`
	Bindings          []SourceBindingResponse `json:"bindings"`
	CreatedBindingIDs []string                `json:"created_binding_ids,omitempty"`
	UpdatedBindingIDs []string                `json:"updated_binding_ids,omitempty"`
	RemovedBindingIDs []string                `json:"removed_binding_ids,omitempty"`
	JobIDs            []string                `json:"job_ids,omitempty"`
	JobErrors         []JobError              `json:"job_errors,omitempty"`
}

type BindingMutationResponse struct {
	Binding            SourceBindingResponse `json:"binding"`
	OldGeneration      int64                 `json:"old_generation,omitempty"`
	NewGeneration      int64                 `json:"new_generation,omitempty"`
	JobIDs             []string              `json:"job_ids,omitempty"`
	CompensationErrors []JobError            `json:"compensation_errors,omitempty"`
}

type DeleteBindingResponse struct {
	Deleted                     bool       `json:"deleted"`
	BindingID                   string     `json:"binding_id"`
	RemovedCoreParentDocumentID string     `json:"removed_core_parent_document_id,omitempty"`
	CancelledTaskCount          int64      `json:"cancelled_task_count"`
	CompensationErrors          []JobError `json:"compensation_errors,omitempty"`
}

type DeleteSourceResponse struct {
	Deleted            bool       `json:"deleted"`
	SourceID           string     `json:"source_id"`
	RemovedBindingIDs  []string   `json:"removed_binding_ids,omitempty"`
	RemovedDatasetID   string     `json:"removed_dataset_id,omitempty"`
	CompensationErrors []JobError `json:"compensation_errors,omitempty"`
}

type DeleteSourceOptions struct {
	SkipCoreDatasetDelete bool
}

type JobError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type SourceRepository interface {
	GetCreateOperation(ctx context.Context, callerID, requestID string) (store.CreateOperation, error)
	SaveCreateOperation(ctx context.Context, operation store.CreateOperation) error
	UpdateCreateOperation(ctx context.Context, operation store.CreateOperation) error
	CreateSourceWithBindings(ctx context.Context, record store.SourceCreateRecord) error
	ListSources(ctx context.Context, req store.SourceListRequest) ([]store.SourceListRecord, int, error)
	GetSource(ctx context.Context, sourceID string) (store.Source, error)
	GetSourceByDatasetID(ctx context.Context, datasetID string) (store.Source, error)
	UpdateSource(ctx context.Context, source store.Source) error
	UpdateSourceWithBindings(ctx context.Context, mutation store.SourceUpdateMutation) (store.SourceUpdateResult, error)
	DeleteSource(ctx context.Context, sourceID string, deletedAt time.Time) (store.SourceDeleteResult, error)
	ListBindings(ctx context.Context, sourceID string) ([]store.Binding, error)
	ListBindingsBySourceIDs(ctx context.Context, sourceIDs []string) ([]store.Binding, error)
	GetBinding(ctx context.Context, sourceID, bindingID string) (store.Binding, error)
	FindActiveBindingByTarget(ctx context.Context, sourceID, excludeBindingID, connectorType, targetType, targetFingerprint string) (store.Binding, bool, error)
	AddBinding(ctx context.Context, binding store.Binding, checkpoint store.SyncCheckpoint) error
	UpdateBinding(ctx context.Context, binding store.Binding, checkpoint store.SyncCheckpoint, cleanup store.BindingUpdateCleanup) error
	RecordSyncJobError(ctx context.Context, sourceID, bindingID string, generation int64, lastError store.JSON, now time.Time) error
	DeleteBinding(ctx context.Context, sourceID, bindingID string, deletedAt time.Time) (store.BindingDeleteResult, error)
	GetSourceSummary(ctx context.Context, req store.SourceSummaryRequest) (store.SourceSummary, error)
	CreateAgentCommand(ctx context.Context, command store.AgentCommand) error
}

type AuthConnectionStatusClient interface {
	BatchStatus(ctx context.Context, req AuthConnectionStatusRequest) (map[string]AuthConnectionStatus, error)
}

type ScheduleEngine interface {
	BuildCheckpoint(ctx context.Context, binding store.Binding, now time.Time) (store.SyncCheckpoint, error)
	TriggerInitialSync(ctx context.Context, binding store.Binding) ([]string, error)
	EnqueueManualSync(ctx context.Context, req scheduleengine.ManualSyncRequest) (scheduleengine.SyncRunIntent, error)
}
