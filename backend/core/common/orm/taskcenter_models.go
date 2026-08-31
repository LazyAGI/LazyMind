package orm

import (
	"time"
)

// TaskCenterTask records one execution instance in the task center.
// Each plugin run, background chat, or scheduled trigger produces exactly one row.
// Sub-agent tasks / plugin steps are NOT stored here; they are queried by relation.
type TaskCenterTask struct {
	ID                string     `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID            string     `gorm:"column:user_id;type:varchar(255);not null;index:idx_tct_user_status,priority:1"`
	ConversationID    string     `gorm:"column:conversation_id;type:varchar(36);not null"`
	WorkflowSessionID *string    `gorm:"column:plugin_session_id;type:varchar(36)"`
	TaskType          string     `gorm:"column:task_type;type:varchar(32);not null"` // workflow_run | background_chat | scheduled
	Title             *string    `gorm:"column:title;type:text"`
	Status            string     `gorm:"column:status;type:varchar(16);not null;default:pending;index:idx_tct_user_status,priority:2"` // pending|running|waiting|succeeded|failed|canceled
	ScheduleID        *string    `gorm:"column:schedule_id;type:varchar(36)"`                                                          // FK → user_schedules.id; non-null when task_type=scheduled
	GroupID           *string    `gorm:"column:group_id;type:varchar(36);index"`
	ScheduledFireAt   *time.Time `gorm:"column:scheduled_fire_at;index"`
	LogicalSlotKey    string     `gorm:"column:logical_slot_key;type:varchar(160)"`
	WindowStart       *time.Time `gorm:"column:window_start"`
	WindowEnd         *time.Time `gorm:"column:window_end"`
	TriggerType       string     `gorm:"column:trigger_type;type:varchar(32);not null;default:'manual'"`
	Attempt           int        `gorm:"column:attempt;not null;default:1"` // Dependency scheduler increments this to fence stale claims.
	DefinitionVersion int        `gorm:"column:definition_version;not null;default:1"`
	DependencyStatus  string     `gorm:"column:dependency_status;type:varchar(32);not null;default:'none'"`
	HasLateInputs     bool       `gorm:"column:has_late_inputs;not null;default:false"`
	ProgressJSON      RawJSON    `gorm:"column:progress_json;type:text"`
	PredictedDoneAt   *time.Time `gorm:"column:predicted_completion_at"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;not null"`
	FinishedAt        *time.Time `gorm:"column:finished_at"`
	ArchivedAt        *time.Time `gorm:"column:archived_at"` // non-null = hidden from task center list
	ArchivedReason    string     `gorm:"column:archived_reason;type:varchar(32);not null;default:''"`
}

func (TaskCenterTask) TableName() string { return "task_center_tasks" }

// UserSchedule stores a recurring trigger rule defined by the user in chat.
// Each cron tick creates a new TaskCenterTask row (task_type=scheduled, schedule_id=this.ID).
// Each trigger creates a fresh conversation (is_task_conv=true); no conversation_id binding.
type UserSchedule struct {
	ID                string     `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID            string     `gorm:"column:user_id;type:varchar(255);not null"`
	Name              string     `gorm:"column:name;type:varchar(128);not null;default:''"`
	Remark            string     `gorm:"column:remark;type:text;not null;default:''"`
	CronExpr          string     `gorm:"column:cron_expr;type:varchar(64);not null"`
	Timezone          string     `gorm:"column:timezone;type:varchar(64);not null;default:'Asia/Shanghai'"`
	PromptTemplate    string     `gorm:"column:prompt_template;type:text;not null"` // task description sent to chat on each trigger
	KbIDs             string     `gorm:"column:kb_ids;type:text;not null;default:'[]'"`
	FileIDs           string     `gorm:"column:file_ids;type:text;not null;default:'[]'"`
	GroupID           *string    `gorm:"column:group_id;type:varchar(36);index"`
	GroupPosition     int        `gorm:"column:group_position;not null;default:0"`
	DefinitionVersion int        `gorm:"column:definition_version;not null;default:1"`
	Enabled           bool       `gorm:"column:enabled;not null;default:true"`
	RunCount          int        `gorm:"column:run_count;not null;default:0"`
	LastRunAt         *time.Time `gorm:"column:last_run_at"`
	NextRunAt         time.Time  `gorm:"column:next_run_at;not null"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null"`
}

func (UserSchedule) TableName() string { return "user_schedules" }

// AutomationGroup is an optional presentation and bulk-management container.
// Dependency edges are intentionally independent from group membership.
type AutomationGroup struct {
	ID        string    `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID    string    `gorm:"column:user_id;type:varchar(255);not null;index"`
	Name      string    `gorm:"column:name;type:varchar(128);not null"`
	Remark    string    `gorm:"column:remark;type:text;not null;default:''"`
	Timezone  string    `gorm:"column:timezone;type:varchar(64);not null;default:'Asia/Shanghai'"`
	Enabled   bool      `gorm:"column:enabled;not null;default:true"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (AutomationGroup) TableName() string { return "automation_groups" }

type ScheduleDependency struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID           string    `gorm:"column:user_id;type:varchar(255);not null;index"`
	SourceScheduleID string    `gorm:"column:source_schedule_id;type:varchar(36);not null;index"`
	TargetScheduleID string    `gorm:"column:target_schedule_id;type:varchar(36);not null;index"`
	WindowType       string    `gorm:"column:window_type;type:varchar(32);not null;default:'between_target_fires'"`
	WindowConfigJSON RawJSON   `gorm:"column:window_config_json;type:text"`
	ContentTypesJSON RawJSON   `gorm:"column:content_types_json;type:text"`
	IncompletePolicy string    `gorm:"column:incomplete_policy;type:varchar(48);not null;default:'wait_then_run_with_warning'"`
	MaxWaitSeconds   int       `gorm:"column:max_wait_seconds;not null;default:7200"`
	Enabled          bool      `gorm:"column:enabled;not null;default:true"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null"`
}

func (ScheduleDependency) TableName() string { return "schedule_dependencies" }

type TaskRunOutput struct {
	ID                   string    `gorm:"column:id;type:varchar(36);primaryKey"`
	TaskID               string    `gorm:"column:task_id;type:varchar(36);not null;uniqueIndex"`
	ConversationID       string    `gorm:"column:conversation_id;type:varchar(36);not null;index"`
	FinalAnswerText      string    `gorm:"column:final_answer_text;type:text"`
	SummaryText          string    `gorm:"column:summary_text;type:text"`
	ArtifactManifestJSON RawJSON   `gorm:"column:artifact_manifest_json;type:text"`
	OutputStatus         string    `gorm:"column:output_status;type:varchar(24);not null"`
	ContentHash          string    `gorm:"column:content_hash;type:varchar(64);not null"`
	CreatedAt            time.Time `gorm:"column:created_at;not null"`
	UpdatedAt            time.Time `gorm:"column:updated_at;not null"`
}

func (TaskRunOutput) TableName() string { return "task_run_outputs" }

type TaskRunInput struct {
	ID                   string    `gorm:"column:id;type:varchar(36);primaryKey"`
	DownstreamTaskID     string    `gorm:"column:downstream_task_id;type:varchar(36);not null;index;uniqueIndex:uk_task_run_input_snapshot,priority:1"`
	UpstreamTaskID       string    `gorm:"column:upstream_task_id;type:varchar(36);not null;index;uniqueIndex:uk_task_run_input_snapshot,priority:3"`
	DependencyID         string    `gorm:"column:dependency_id;type:varchar(36);not null;uniqueIndex:uk_task_run_input_snapshot,priority:2"`
	SourceLogicalSlotKey string    `gorm:"column:source_logical_slot_key;type:varchar(160)"`
	OutputID             string    `gorm:"column:output_id;type:varchar(36);not null"`
	OutputContentHash    string    `gorm:"column:output_content_hash;type:varchar(64);not null"`
	Position             int       `gorm:"column:position;not null"`
	SnapshotJSON         RawJSON   `gorm:"column:snapshot_json;type:text"`
	CreatedAt            time.Time `gorm:"column:created_at;not null"`
}

func (TaskRunInput) TableName() string { return "task_run_inputs" }
