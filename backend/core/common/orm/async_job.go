package orm

import (
	"encoding/json"
	"time"
)

type AsyncJob struct {
	ID               string          `gorm:"column:id;type:varchar(64);primaryKey"`
	JobType          string          `gorm:"column:job_type;type:varchar(64);not null;index:idx_async_jobs_type_status,priority:1"`
	Status           string          `gorm:"column:status;type:varchar(32);not null;index:idx_async_jobs_status_next,priority:1"`
	ResourceType     string          `gorm:"column:resource_type;type:varchar(64);not null;default:'';index:idx_async_jobs_resource,priority:1"`
	ResourceID       string          `gorm:"column:resource_id;type:varchar(128);not null;default:'';index:idx_async_jobs_resource,priority:2"`
	IdempotencyKey   string          `gorm:"column:idempotency_key;type:varchar(128);not null;default:'';index"`
	PayloadJSON      json.RawMessage `gorm:"column:payload_json;type:json"`
	ResultJSON       json.RawMessage `gorm:"column:result_json;type:json"`
	ErrorCode        string          `gorm:"column:error_code;type:varchar(64);not null;default:''"`
	ErrorMessage     string          `gorm:"column:error_message;type:text;not null;default:''"`
	ErrorDetailsJSON json.RawMessage `gorm:"column:error_details_json;type:json"`
	ProgressCurrent  int64           `gorm:"column:progress_current;not null;default:0"`
	ProgressTotal    int64           `gorm:"column:progress_total;not null;default:0"`
	AttemptCount     int             `gorm:"column:attempt_count;not null;default:0"`
	MaxAttempts      int             `gorm:"column:max_attempts;not null;default:1"`
	NextRunAt        time.Time       `gorm:"column:next_run_at;not null;index:idx_async_jobs_status_next,priority:2"`
	LockedBy         string          `gorm:"column:locked_by;type:varchar(128);not null;default:''"`
	LockUntil        *time.Time      `gorm:"column:lock_until;index"`
	StartedAt        *time.Time      `gorm:"column:started_at"`
	FinishedAt       *time.Time      `gorm:"column:finished_at"`
	HeartbeatAt      *time.Time      `gorm:"column:heartbeat_at"`
	CreateUserID     string          `gorm:"column:create_user_id;type:varchar(255);not null;default:''"`
	CreateUserName   string          `gorm:"column:create_user_name;type:varchar(255);not null;default:''"`
	CreatedAt        time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt        time.Time       `gorm:"column:updated_at;not null"`
}

func (AsyncJob) TableName() string { return "async_jobs" }
