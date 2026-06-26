package orm

import "time"

type AgentThread struct {
	ThreadID               string    `gorm:"column:thread_id;type:varchar(128);primaryKey"`
	CurrentTaskID          string    `gorm:"column:current_task_id;type:varchar(128);not null;default:'';index"`
	Status                 string    `gorm:"column:status;type:varchar(32);not null;default:'created'"`
	ThreadPayload          string    `gorm:"column:thread_payload;type:text;not null;default:''"`
	LastMessageRequestHash string    `gorm:"column:last_message_request_hash;type:varchar(64);not null;default:''"`
	CreateUserID           string    `gorm:"column:create_user_id;type:varchar(255);not null;default:''"`
	CreateUserName         string    `gorm:"column:create_user_name;type:varchar(255);not null;default:''"`
	CreatedAt              time.Time `gorm:"column:created_at;not null"`
	UpdatedAt              time.Time `gorm:"column:updated_at;not null"`
}

func (AgentThread) TableName() string { return "agent_threads" }

type AgentUserActiveThread struct {
	UserID      string    `gorm:"column:user_id;type:varchar(255);primaryKey"`
	ThreadID    string    `gorm:"column:thread_id;type:varchar(128);not null;default:'';index"`
	Status      string    `gorm:"column:status;type:varchar(32);not null;default:'creating';index:idx_agent_user_active_threads_status_lease,priority:1"`
	CreateToken string    `gorm:"column:create_token;type:varchar(64);not null;default:''"`
	LeaseUntil  time.Time `gorm:"column:lease_until;not null;index:idx_agent_user_active_threads_status_lease,priority:2"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null"`
}

func (AgentUserActiveThread) TableName() string { return "agent_user_active_threads" }

type AgentThreadRecord struct {
	ID          string    `gorm:"column:id;type:varchar(32);primaryKey;index:idx_agent_thread_records_thread_step_stream_id,priority:4"`
	ThreadID    string    `gorm:"column:thread_id;type:varchar(128);not null;index:idx_agent_thread_records_thread_stream_id,priority:1;index:idx_agent_thread_records_thread_round_id,priority:1;index:idx_agent_thread_records_thread_step_stream_id,priority:1;uniqueIndex:uk_agent_thread_records_record_key,priority:1"`
	RoundID     string    `gorm:"column:round_id;type:varchar(32);not null;default:'';index:idx_agent_thread_records_thread_round_id,priority:2;index:idx_agent_thread_records_round_stream_id,priority:1;uniqueIndex:uk_agent_thread_records_record_key,priority:2"`
	StepID      string    `gorm:"column:step_id;type:varchar(128);not null;default:'';index:idx_agent_thread_records_thread_step_stream_id,priority:2"`
	TaskID      string    `gorm:"column:task_id;type:varchar(128);not null;default:'';index"`
	StreamKind  string    `gorm:"column:stream_kind;type:varchar(32);not null;index:idx_agent_thread_records_thread_stream_id,priority:2;index:idx_agent_thread_records_round_stream_id,priority:2;index:idx_agent_thread_records_thread_step_stream_id,priority:3;uniqueIndex:uk_agent_thread_records_record_key,priority:3"`
	RecordKey   string    `gorm:"column:record_key;type:varchar(64);not null;uniqueIndex:uk_agent_thread_records_record_key,priority:4"`
	EventName   string    `gorm:"column:event_name;type:varchar(128);not null;default:''"`
	PayloadText string    `gorm:"column:payload_text;type:text;not null;default:''"`
	RawFrame    string    `gorm:"column:raw_frame;type:text;not null;default:''"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null"`
}

func (AgentThreadRecord) TableName() string { return "agent_thread_records" }

type AgentThreadStep struct {
	ThreadID      string     `gorm:"column:thread_id;type:varchar(128);primaryKey;index:idx_agent_thread_steps_thread_order,priority:1;index:idx_agent_thread_steps_thread_active,priority:1"`
	StepID        string     `gorm:"column:step_id;type:varchar(128);primaryKey;index:idx_agent_thread_steps_thread_order,priority:3"`
	Title         string     `gorm:"column:title;type:varchar(255);not null;default:''"`
	Status        string     `gorm:"column:status;type:varchar(32);not null;default:'running';index"`
	Active        bool       `gorm:"column:active;not null;default:false;index:idx_agent_thread_steps_thread_active,priority:2"`
	OrderIndex    int        `gorm:"column:order_index;not null;default:0;index:idx_agent_thread_steps_thread_order,priority:2"`
	EventCount    int64      `gorm:"column:event_count;not null;default:0"`
	CurrentTaskID string     `gorm:"column:current_task_id;type:varchar(128);not null;default:''"`
	NextStepRunID string     `gorm:"column:next_step_run_id;type:varchar(128);not null;default:''"`
	StartedAt     *time.Time `gorm:"column:started_at"`
	EndedAt       *time.Time `gorm:"column:ended_at"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null;index:idx_agent_thread_steps_thread_active,priority:3"`
}

func (AgentThreadStep) TableName() string { return "agent_thread_steps" }

type AgentThreadRound struct {
	RoundID          string    `gorm:"column:round_id;type:varchar(32);primaryKey"`
	ThreadID         string    `gorm:"column:thread_id;type:varchar(128);not null;index:idx_agent_thread_rounds_thread_id,priority:1;index:idx_agent_thread_rounds_thread_request_hash,priority:1"`
	RequestHash      string    `gorm:"column:request_hash;type:varchar(64);not null;default:'';index:idx_agent_thread_rounds_thread_request_hash,priority:2"`
	TaskID           string    `gorm:"column:task_id;type:varchar(128);not null;default:'';index"`
	Status           string    `gorm:"column:status;type:varchar(32);not null;default:'created'"`
	UserMessage      string    `gorm:"column:user_message;type:text;not null;default:''"`
	AssistantMessage string    `gorm:"column:assistant_message;type:text;not null;default:''"`
	RequestPayload   string    `gorm:"column:request_payload;type:text;not null;default:''"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null"`
}

func (AgentThreadRound) TableName() string { return "agent_thread_rounds" }
