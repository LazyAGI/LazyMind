package orm

import (
	"encoding/json"
	"time"
)

const (
	ResourceUpdateTaskTypeGenerateReview  = "generate_review"
	ResourceUpdateTaskTypeAutoApplyReview = "auto_apply_review"

	ResourceUpdateResourceTypeSkill          = "skill"
	ResourceUpdateResourceTypeMemory         = "memory"
	ResourceUpdateResourceTypeUserPreference = "user_preference"

	ResourceUpdateTriggerTypeScheduled        = "scheduled"
	ResourceUpdateTriggerTypeConversationIdle = "conversation_idle"
	ResourceUpdateTriggerTypeReviewResult     = "review_result"
	ResourceUpdateTriggerTypeAutoEvoEnabled   = "auto_evo_enabled"

	ResourceUpdateTaskStatusPending = "pending"
	ResourceUpdateTaskStatusRunning = "running"
	ResourceUpdateTaskStatusDone    = "done"
	ResourceUpdateTaskStatusFailed  = "failed"
	ResourceUpdateTaskStatusSkipped = "skipped"

	ConversationIdleEventStatusWaiting    = "waiting"
	ConversationIdleEventStatusProcessing = "processing"
	ConversationIdleEventStatusTriggered  = "triggered"
	ConversationIdleEventStatusSkipped    = "skipped"
	ConversationIdleEventStatusFailed     = "failed"
)

type ResourceUpdateTask struct {
	ID             string          `gorm:"column:id;type:varchar(36);primaryKey"`
	TaskType       string          `gorm:"column:task_type;type:varchar(32);not null;index;uniqueIndex:uniq_resource_update_task_trigger,priority:1"`
	ResourceType   string          `gorm:"column:resource_type;type:varchar(32);not null;index;uniqueIndex:uniq_resource_update_task_trigger,priority:2;uniqueIndex:uniq_resource_update_active_auto_apply_result,priority:1,where:task_type = 'auto_apply_review' AND (status = 'pending' OR status = 'running')"`
	UserID         string          `gorm:"column:user_id;type:varchar(255);not null;default:'';index;index:idx_resource_update_tasks_user_created,priority:1"`
	ResourceID     string          `gorm:"column:resource_id;type:varchar(128);not null;default:'';index"`
	TriggerType    string          `gorm:"column:trigger_type;type:varchar(32);not null;index;uniqueIndex:uniq_resource_update_task_trigger,priority:3"`
	TriggerID      string          `gorm:"column:trigger_id;type:varchar(512);not null;index;uniqueIndex:uniq_resource_update_task_trigger,priority:4"`
	Status         string          `gorm:"column:status;type:varchar(32);not null;index;index:idx_resource_update_tasks_pending,priority:1;index:idx_resource_update_tasks_running_lock,priority:1"`
	RequestJSON    json.RawMessage `gorm:"column:request_json;type:json"`
	ReviewResultID string          `gorm:"column:review_result_id;type:varchar(128);index;uniqueIndex:uniq_resource_update_active_auto_apply_result,priority:2,where:task_type = 'auto_apply_review' AND (status = 'pending' OR status = 'running')"`
	ResultID       string          `gorm:"column:result_id;type:varchar(128);index"`
	ErrorCode      string          `gorm:"column:error_code;type:varchar(64);not null;default:''"`
	ErrorMessage   string          `gorm:"column:error_message;type:text;not null;default:''"`
	AttemptCount   int             `gorm:"column:attempt_count;not null;default:0"`
	NextRunAt      time.Time       `gorm:"column:next_run_at;not null;index:idx_resource_update_tasks_pending,priority:2"`
	LockedBy       string          `gorm:"column:locked_by;type:varchar(128);not null;default:''"`
	LockedUntil    *time.Time      `gorm:"column:locked_until;index:idx_resource_update_tasks_running_lock,priority:2"`
	CreatedAt      time.Time       `gorm:"column:created_at;not null;index:idx_resource_update_tasks_pending,priority:3;index:idx_resource_update_tasks_user_created,priority:2,sort:desc"`
	UpdatedAt      time.Time       `gorm:"column:updated_at;not null"`
	StartedAt      *time.Time      `gorm:"column:started_at"`
	FinishedAt     *time.Time      `gorm:"column:finished_at"`
}

func (ResourceUpdateTask) TableName() string { return "resource_update_tasks" }

type SkillReviewSchedulerState struct {
	UserID               string     `gorm:"column:user_id;type:varchar(255);primaryKey"`
	LastWindowEnd        time.Time  `gorm:"column:last_window_end;not null"`
	NextRunAt            time.Time  `gorm:"column:next_run_at;not null;index:idx_skill_review_scheduler_state_scan,priority:2"`
	StageIndex           int        `gorm:"column:stage_index;not null;default:0"`
	StageSuccessCount    int        `gorm:"column:stage_success_count;not null;default:0"`
	TotalSuccessCount    int        `gorm:"column:total_success_count;not null;default:0"`
	LastAcceptedAt       *time.Time `gorm:"column:last_accepted_at"`
	LastQuantityCheckAt  *time.Time `gorm:"column:last_quantity_check_at;index:idx_skill_review_scheduler_state_scan,priority:3"`
	LastPreflightCheckAt *time.Time `gorm:"column:last_preflight_check_at"`
	ActiveTaskID         string     `gorm:"column:active_task_id;type:varchar(36);not null;default:''"`
	LockedBy             string     `gorm:"column:locked_by;type:varchar(128);not null;default:''"`
	LockedUntil          *time.Time `gorm:"column:locked_until;index:idx_skill_review_scheduler_state_scan,priority:1"`
	LastErrorCode        string     `gorm:"column:last_error_code;type:varchar(64);not null;default:''"`
	LastErrorMessage     string     `gorm:"column:last_error_message;type:text;not null;default:''"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null"`
}

func (SkillReviewSchedulerState) TableName() string { return "skill_review_scheduler_state" }

type ConversationIdleEvent struct {
	ID                   string     `gorm:"column:id;type:varchar(36);primaryKey"`
	EventID              string     `gorm:"column:event_id;type:varchar(512);not null;uniqueIndex:uk_conversation_idle_events_event_id"`
	SessionID            string     `gorm:"column:session_id;type:varchar(128);not null;index;index:idx_conversation_idle_events_session_waiting,priority:1"`
	UserID               string     `gorm:"column:user_id;type:varchar(255);not null;index"`
	LastMessageID        string     `gorm:"column:last_message_id;type:varchar(128);not null"`
	LastActivityAt       time.Time  `gorm:"column:last_activity_at;not null"`
	DueAt                time.Time  `gorm:"column:due_at;not null;index;index:idx_conversation_idle_events_due,priority:2;index:idx_conversation_idle_events_session_waiting,priority:3,sort:desc"`
	Status               string     `gorm:"column:status;type:varchar(32);not null;index;index:idx_conversation_idle_events_due,priority:1;index:idx_conversation_idle_events_session_waiting,priority:2"`
	SkipReason           string     `gorm:"column:skip_reason;type:varchar(128);not null;default:''"`
	ErrorCode            string     `gorm:"column:error_code;type:varchar(64);not null;default:''"`
	ErrorMessage         string     `gorm:"column:error_message;type:text;not null;default:''"`
	MemoryTaskID         string     `gorm:"column:memory_task_id;type:varchar(36);not null;default:''"`
	UserPreferenceTaskID string     `gorm:"column:user_preference_task_id;type:varchar(36);not null;default:''"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null"`
	TriggeredAt          *time.Time `gorm:"column:triggered_at"`
}

func (ConversationIdleEvent) TableName() string { return "conversation_idle_events" }
