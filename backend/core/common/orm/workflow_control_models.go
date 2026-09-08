package orm

import "time"

// WorkflowReviewCheckpoint binds one completed execution to the content actually reviewed.
type WorkflowReviewCheckpoint struct {
	ID                string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	SessionID         string     `gorm:"type:varchar(36);not null;index" json:"session_id"`
	AttemptID         string     `gorm:"type:varchar(36);not null;uniqueIndex" json:"execution_id"`
	StepID            string     `gorm:"type:varchar(64);not null" json:"step_id"`
	Version           int64      `gorm:"not null;default:1" json:"version"`
	Status            string     `gorm:"type:varchar(16);not null;default:pending" json:"status"`
	SlotsJSON         string     `gorm:"type:text;not null;default:'[]'" json:"-"`
	ManifestJSON      string     `gorm:"type:text;not null;default:'[]'" json:"-"`
	ManifestHash      string     `gorm:"type:varchar(64);not null;default:''" json:"manifest_hash"`
	DecisionCommandID string     `gorm:"type:varchar(255);not null;default:''" json:"decision_command_id,omitempty"`
	AcceptedBy        string     `gorm:"type:varchar(255);not null;default:''" json:"accepted_by,omitempty"`
	AcceptedAt        *time.Time `json:"accepted_at,omitempty"`
	CreatedAt         time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt         time.Time  `gorm:"not null" json:"updated_at"`
}

func (WorkflowReviewCheckpoint) TableName() string { return "workflow_review_checkpoints" }

// WorkflowHostAction persists the intent to contact a host separately from its receipt.
type WorkflowHostAction struct {
	ID                string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	SessionID         string     `gorm:"type:varchar(36);not null;index" json:"session_id"`
	CommandID         string     `gorm:"type:varchar(255);not null;uniqueIndex" json:"command_id"`
	Kind              string     `gorm:"type:varchar(16);not null" json:"kind"`
	BindingGeneration int64      `gorm:"not null" json:"binding_generation"`
	ConnectorID       string     `gorm:"type:varchar(128);not null;index" json:"connector_id"`
	NativeSessionID   string     `gorm:"type:varchar(255);not null" json:"native_session_id"`
	ExecutionID       string     `gorm:"type:varchar(36);not null;default:''" json:"execution_id,omitempty"`
	Status            string     `gorm:"type:varchar(16);not null;default:pending;index" json:"status"`
	DispatchOwner     string     `gorm:"type:varchar(128);not null;default:''" json:"-"`
	DispatchTokenHash string     `gorm:"type:varchar(64);not null;default:''" json:"-"`
	DispatchExpiresAt *time.Time `json:"dispatch_expires_at,omitempty"`
	AttemptCount      int        `gorm:"not null;default:0" json:"attempt_count"`
	LastError         string     `gorm:"type:text;not null;default:''" json:"last_error,omitempty"`
	NativeEventSeq    int64      `gorm:"not null;default:0" json:"native_event_seq,omitempty"`
	AcceptedAt        *time.Time `json:"accepted_at,omitempty"`
	ConsumedAt        *time.Time `json:"consumed_at,omitempty"`
	CreatedAt         time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt         time.Time  `gorm:"not null" json:"updated_at"`
}

func (WorkflowHostAction) TableName() string { return "workflow_host_actions" }
