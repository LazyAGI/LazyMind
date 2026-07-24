package source

import "time"

type ormSource struct {
	SourceID          string `gorm:"column:source_id;primaryKey"`
	TenantID          string `gorm:"column:tenant_id"`
	CreatedBy         string `gorm:"column:created_by"`
	Name              string `gorm:"column:name"`
	DatasetID         string `gorm:"column:dataset_id"`
	Status            string `gorm:"column:status"`
	SourceOptions     JSON   `gorm:"column:source_options_json;type:jsonb"`
	IncludeExtensions JSON   `gorm:"column:include_extensions_json;type:jsonb"`
	ExcludeExtensions JSON   `gorm:"column:exclude_extensions_json;type:jsonb"`
	ConfigVersion     int64  `gorm:"column:config_version"`
	DeletedAt         *time.Time
	CreatedAt         time.Time `gorm:"column:created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at"`
}

func (ormSource) TableName() string { return "sources" }

type ormBinding struct {
	BindingID              string `gorm:"column:binding_id;primaryKey"`
	SourceID               string `gorm:"column:source_id"`
	BindingType            string `gorm:"column:binding_type"`
	ConnectorType          string `gorm:"column:connector_type"`
	TargetType             string `gorm:"column:target_type"`
	TargetRef              string `gorm:"column:target_ref"`
	TargetFingerprint      string `gorm:"column:target_fingerprint"`
	AgentID                string `gorm:"column:agent_id"`
	AuthConnectionID       string `gorm:"column:auth_connection_id"`
	ProviderOptions        JSON   `gorm:"column:provider_options_json;type:jsonb"`
	TreeKey                string `gorm:"column:tree_key"`
	BindingGeneration      int64  `gorm:"column:binding_generation"`
	CoreParentDocumentID   string `gorm:"column:core_parent_document_id"`
	CoreParentDocumentName string `gorm:"column:core_parent_document_name"`
	SyncMode               string `gorm:"column:sync_mode"`
	SchedulePolicy         JSON   `gorm:"column:schedule_policy_json;type:jsonb"`
	NextSyncAt             *time.Time
	IncludeExtensions      JSON       `gorm:"column:include_extensions_json;type:jsonb"`
	ExcludeExtensions      JSON       `gorm:"column:exclude_extensions_json;type:jsonb"`
	Status                 string     `gorm:"column:status"`
	LastError              JSON       `gorm:"column:last_error;type:jsonb"`
	DeletedAt              *time.Time `gorm:"column:deleted_at"`
	CreatedAt              time.Time  `gorm:"column:created_at"`
	UpdatedAt              time.Time  `gorm:"column:updated_at"`
}

func (ormBinding) TableName() string { return "source_bindings" }

type ormSourceObject struct {
	SourceID        string `gorm:"column:source_id"`
	BindingID       string `gorm:"column:binding_id;primaryKey"`
	TreeKey         string `gorm:"column:tree_key"`
	ObjectKey       string `gorm:"column:object_key;primaryKey"`
	ParentKey       string `gorm:"column:parent_key"`
	DisplayName     string `gorm:"column:display_name"`
	SearchName      string `gorm:"column:search_name"`
	ObjectType      string `gorm:"column:object_type"`
	IsDocument      bool   `gorm:"column:is_document"`
	IsContainer     bool   `gorm:"column:is_container"`
	HasChildren     bool   `gorm:"column:has_children"`
	SourceVersion   string `gorm:"column:source_version"`
	SizeBytes       int64  `gorm:"column:size_bytes"`
	MimeType        string `gorm:"column:mime_type"`
	FileExtension   string `gorm:"column:file_extension"`
	ModifiedAt      *time.Time
	DeletedAtSource *time.Time `gorm:"column:deleted_at_source"`
	Depth           int64      `gorm:"column:depth"`
	ProviderMeta    JSON       `gorm:"column:provider_meta_json;type:jsonb"`
	LastSeenRunID   string     `gorm:"column:last_seen_run_id"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

func (ormSourceObject) TableName() string { return "source_object_index" }

type ormDocumentState struct {
	SourceID            string `gorm:"column:source_id;primaryKey"`
	BindingID           string `gorm:"column:binding_id;primaryKey"`
	BindingGeneration   int64  `gorm:"column:binding_generation"`
	ObjectKey           string `gorm:"column:object_key;primaryKey"`
	SourceVersion       string `gorm:"column:source_version"`
	BaselineVersion     string `gorm:"column:baseline_version"`
	DeletedAtSource     *time.Time
	SourceState         string `gorm:"column:source_state"`
	SyncState           string `gorm:"column:sync_state"`
	PendingAction       string `gorm:"column:pending_action"`
	DocumentListVisible bool   `gorm:"column:document_list_visible"`
	Selectable          bool   `gorm:"column:selectable"`
	ParseQueueState     string `gorm:"column:parse_queue_state"`
	DocumentID          string `gorm:"column:document_id"`
	ActiveTaskID        string `gorm:"column:active_task_id"`
	LastDetectedAt      *time.Time
	LastSyncedAt        *time.Time
	LastError           JSON      `gorm:"column:last_error;type:jsonb"`
	CreatedAt           time.Time `gorm:"column:created_at"`
	UpdatedAt           time.Time `gorm:"column:updated_at"`
}

func (ormDocumentState) TableName() string { return "source_document_states" }

type ormDocument struct {
	DocumentID       string `gorm:"column:document_id;primaryKey"`
	TenantID         string `gorm:"column:tenant_id"`
	SourceID         string `gorm:"column:source_id"`
	BindingID        string `gorm:"column:binding_id"`
	ObjectKey        string `gorm:"column:object_key"`
	CoreDocumentID   string `gorm:"column:core_document_id"`
	CurrentVersionID string `gorm:"column:current_version_id"`
	DesiredVersionID string `gorm:"column:desired_version_id"`
	SourceVersion    string `gorm:"column:source_version"`
	DisplayName      string `gorm:"column:display_name"`
	MimeType         string `gorm:"column:mime_type"`
	FileExtension    string `gorm:"column:file_extension"`
	ParseStatus      string `gorm:"column:parse_status"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (ormDocument) TableName() string { return "documents" }

type ormParseTask struct {
	TaskID               string `gorm:"column:task_id;primaryKey"`
	TenantID             string `gorm:"column:tenant_id"`
	SourceID             string `gorm:"column:source_id"`
	BindingID            string `gorm:"column:binding_id"`
	BindingGeneration    int64  `gorm:"column:binding_generation"`
	ObjectKey            string `gorm:"column:object_key"`
	DocumentID           string `gorm:"column:document_id"`
	TaskAction           string `gorm:"column:task_action"`
	TargetVersionID      string `gorm:"column:target_version_id"`
	SourceVersion        string `gorm:"column:source_version"`
	CoreParentDocumentID string `gorm:"column:core_parent_document_id"`
	IdempotencyKey       string `gorm:"column:idempotency_key"`
	Status               string `gorm:"column:status"`
	CoreTaskID           string `gorm:"column:core_task_id"`
	CoreDocumentID       string `gorm:"column:core_document_id"`
	LeaseOwner           string `gorm:"column:lease_owner"`
	LeaseUntil           *time.Time
	RetryCount           int64     `gorm:"column:retry_count"`
	NextRunAt            time.Time `gorm:"column:next_run_at"`
	LastError            JSON      `gorm:"column:last_error;type:jsonb"`
	CreatedAt            time.Time `gorm:"column:created_at"`
	UpdatedAt            time.Time `gorm:"column:updated_at"`
}

func (ormParseTask) TableName() string { return "parse_tasks" }

type ormSyncCheckpoint struct {
	SourceID          string `gorm:"column:source_id"`
	BindingID         string `gorm:"column:binding_id;primaryKey"`
	BindingGeneration int64  `gorm:"column:binding_generation"`
	Cursor            string `gorm:"column:cursor"`
	NextSyncAt        *time.Time
	LastSyncAt        *time.Time
	LastSuccessAt     *time.Time
	LockOwner         string `gorm:"column:lock_owner"`
	LockUntil         *time.Time
	RetryCount        int64     `gorm:"column:retry_count"`
	LastError         JSON      `gorm:"column:last_error;type:jsonb"`
	CreatedAt         time.Time `gorm:"column:created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at"`
}

func (ormSyncCheckpoint) TableName() string { return "source_sync_checkpoints" }

type ormSyncRun struct {
	RunID             string     `gorm:"column:run_id;primaryKey"`
	SourceID          string     `gorm:"column:source_id"`
	BindingID         string     `gorm:"column:binding_id"`
	BindingGeneration int64      `gorm:"column:binding_generation"`
	TriggerType       string     `gorm:"column:trigger_type"`
	ScheduledFireAt   *time.Time `gorm:"column:scheduled_fire_at"`
	ScopeType         string     `gorm:"column:scope_type"`
	ScopeRef          JSON       `gorm:"column:scope_ref_json;type:jsonb"`
	Coverage          JSON       `gorm:"column:coverage_json;type:jsonb"`
	Status            string     `gorm:"column:status"`
	SeenCount         int64      `gorm:"column:seen_count"`
	NewCount          int64      `gorm:"column:new_count"`
	ModifiedCount     int64      `gorm:"column:modified_count"`
	DeletedCount      int64      `gorm:"column:deleted_count"`
	UnchangedCount    int64      `gorm:"column:unchanged_count"`
	ErrorCode         string     `gorm:"column:error_code"`
	ErrorMessage      string     `gorm:"column:error_message"`
	StartedAt         time.Time
	FinishedAt        *time.Time
}

func (ormSyncRun) TableName() string { return "source_sync_runs" }

type ormCreateOperation struct {
	OperationID                  string `gorm:"column:operation_id;primaryKey"`
	CallerID                     string `gorm:"column:caller_id"`
	RequestID                    string `gorm:"column:request_id"`
	RequestHash                  string `gorm:"column:request_hash"`
	SourceID                     string `gorm:"column:source_id"`
	DatasetID                    string `gorm:"column:dataset_id"`
	CreatedCoreParentDocumentIDs JSON   `gorm:"column:created_core_parent_document_ids_json;type:jsonb"`
	CreatedBindingIDs            JSON   `gorm:"column:created_binding_ids_json;type:jsonb"`
	Warning                      JSON   `gorm:"column:warning_json;type:jsonb"`
	Status                       string `gorm:"column:status"`
	CompensationStatus           string `gorm:"column:compensation_status"`
	CompensationError            JSON   `gorm:"column:compensation_error;type:jsonb"`
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
}

func (ormCreateOperation) TableName() string { return "data_source_create_operations" }

type ormAgent struct {
	AgentID           string `gorm:"column:agent_id;primaryKey"`
	TenantID          string `gorm:"column:tenant_id"`
	Hostname          string `gorm:"column:hostname"`
	Version           string `gorm:"column:version"`
	Status            string `gorm:"column:status"`
	ListenAddr        string `gorm:"column:listen_addr"`
	LastHeartbeatAt   time.Time
	ActiveSourceCount int64 `gorm:"column:active_source_count"`
	ActiveWatchCount  int64 `gorm:"column:active_watch_count"`
	ActiveTaskCount   int64 `gorm:"column:active_task_count"`
	UpdatedAt         time.Time
}

func (ormAgent) TableName() string { return "agents" }

type ormAgentCommand struct {
	CommandID    string `gorm:"column:command_id;primaryKey"`
	AgentID      string `gorm:"column:agent_id"`
	CommandType  string `gorm:"column:command_type"`
	Payload      JSON   `gorm:"column:payload_json;type:jsonb"`
	Status       string `gorm:"column:status"`
	AttemptCount int64  `gorm:"column:attempt_count"`
	NextRetryAt  *time.Time
	AckedAt      *time.Time
	LastError    JSON `gorm:"column:last_error;type:jsonb"`
	Result       JSON `gorm:"column:result_json;type:jsonb"`
	CreatedAt    time.Time
	DispatchedAt *time.Time
}

func (ormAgentCommand) TableName() string { return "agent_commands" }

type ormParseTaskDeadLetter struct {
	DeadLetterID      string `gorm:"column:dead_letter_id;primaryKey"`
	TaskID            string `gorm:"column:task_id"`
	TenantID          string `gorm:"column:tenant_id"`
	SourceID          string `gorm:"column:source_id"`
	BindingID         string `gorm:"column:binding_id"`
	BindingGeneration int64  `gorm:"column:binding_generation"`
	ObjectKey         string `gorm:"column:object_key"`
	DocumentID        string `gorm:"column:document_id"`
	TaskAction        string `gorm:"column:task_action"`
	TargetVersionID   string `gorm:"column:target_version_id"`
	RetryCount        int64  `gorm:"column:retry_count"`
	ErrorCode         string `gorm:"column:error_code"`
	LastError         JSON   `gorm:"column:last_error;type:jsonb"`
	FailedAt          time.Time
	CreatedAt         time.Time
}

func (ormParseTaskDeadLetter) TableName() string { return "parse_task_dead_letters" }
