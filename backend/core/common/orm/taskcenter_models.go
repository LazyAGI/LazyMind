package orm

import (
	"encoding/json"
	"time"
)

// TaskCenterTask records one execution instance in the task center.
// Each plugin run, background chat, or scheduled trigger produces exactly one row.
// Sub-agent tasks / plugin steps are NOT stored here; they are queried by relation.
type TaskCenterTask struct {
	ID              string          `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID          string          `gorm:"column:user_id;type:varchar(255);not null;index:idx_tct_user_status,priority:1"`
	ConversationID  string          `gorm:"column:conversation_id;type:varchar(36);not null"`
	PluginSessionID *string         `gorm:"column:plugin_session_id;type:varchar(36)"`
	TaskType        string          `gorm:"column:task_type;type:varchar(32);not null"` // plugin_run | background_chat | scheduled
	Title           *string         `gorm:"column:title;type:text"`
	Status          string          `gorm:"column:status;type:varchar(16);not null;default:pending;index:idx_tct_user_status,priority:2"` // pending|running|waiting|succeeded|failed|canceled
	ScheduleID      *string         `gorm:"column:schedule_id;type:varchar(36)"`                                                          // FK → user_schedules.id; non-null when task_type=scheduled
	ProgressJSON    json.RawMessage `gorm:"column:progress_json;type:text"`
	PredictedDoneAt *time.Time      `gorm:"column:predicted_completion_at"`
	CreatedAt       time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time       `gorm:"column:updated_at;not null"`
	FinishedAt      *time.Time      `gorm:"column:finished_at"`
	ArchivedAt      *time.Time      `gorm:"column:archived_at"` // non-null = hidden from task center list
}

func (TaskCenterTask) TableName() string { return "task_center_tasks" }

// UserSchedule stores a recurring trigger rule defined by the user in chat.
// Each cron tick creates a new TaskCenterTask row (task_type=scheduled, schedule_id=this.ID).
// Each trigger creates a fresh conversation (is_task_conv=true); no conversation_id binding.
type UserSchedule struct {
	ID             string     `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID         string     `gorm:"column:user_id;type:varchar(255);not null"`
	Name           string     `gorm:"column:name;type:varchar(128);not null;default:''"`
	Remark         string     `gorm:"column:remark;type:text;not null;default:''"`
	CronExpr       string     `gorm:"column:cron_expr;type:varchar(64);not null"`
	Timezone       string     `gorm:"column:timezone;type:varchar(64);not null;default:'Asia/Shanghai'"`
	PromptTemplate string     `gorm:"column:prompt_template;type:text;not null"` // task description sent to chat on each trigger
	KbIDs          string     `gorm:"column:kb_ids;type:text;not null;default:'[]'"`
	FileIDs        string     `gorm:"column:file_ids;type:text;not null;default:'[]'"`
	Enabled        bool       `gorm:"column:enabled;not null;default:true"`
	RunCount       int        `gorm:"column:run_count;not null;default:0"`
	LastRunAt      *time.Time `gorm:"column:last_run_at"`
	NextRunAt      time.Time  `gorm:"column:next_run_at;not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
}

func (UserSchedule) TableName() string { return "user_schedules" }
