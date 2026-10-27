package orm

import "time"

// TaskExecutionTiming is a Chat round or an active Workflow interval, independent
// of card visibility and mutable chat history. A pause/review closes the interval;
// continuation starts a new one so wait time is excluded without losing history.
type TaskExecutionTiming struct {
	ID                string     `gorm:"column:id;type:varchar(128);primaryKey"`
	RunID             string     `gorm:"column:run_id;type:varchar(128);not null;index"`
	ConversationID    string     `gorm:"column:conversation_id;type:varchar(36);not null;index"`
	OwnerUserID       string     `gorm:"column:owner_user_id;type:varchar(255);not null"`
	HistoryID         string     `gorm:"column:history_id;type:varchar(128);not null;index"`
	WorkflowSessionID string     `gorm:"column:workflow_session_id;type:varchar(36);not null;default:'';index"`
	Status            string     `gorm:"column:status;type:varchar(24);not null"`
	Revision          int64      `gorm:"column:revision;not null;default:1"`
	RequestedAt       time.Time  `gorm:"column:requested_at;not null"`
	FinishedAt        *time.Time `gorm:"column:finished_at"`
}

func (TaskExecutionTiming) TableName() string { return "task_execution_timings" }

// TaskAttemptTiming retains every actual attempt, including failed and stale
// attempts which no longer appear in the ordinary task projection.
type TaskAttemptTiming struct {
	ID          string     `gorm:"column:id;type:varchar(255);primaryKey"`
	ExecutionID string     `gorm:"column:execution_id;type:varchar(128);not null;index"`
	StartedAt   time.Time  `gorm:"column:started_at;not null"`
	FinishedAt  *time.Time `gorm:"column:finished_at"`
}

func (TaskAttemptTiming) TableName() string { return "task_attempt_timings" }
