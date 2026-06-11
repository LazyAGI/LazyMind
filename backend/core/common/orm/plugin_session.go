package orm

import (
	"encoding/json"
	"time"
)

// PluginSession represents a plugin session tied to a conversation.
// One session is created per plugin invocation; it tracks the current step
// and a snapshot of all artifact values for page-refresh recovery.
// IsActive is false once the session finishes (plugin loop exits normally or on error).
type PluginSession struct {
	ID             string          `gorm:"primaryKey;column:id"`
	ConversationID string          `gorm:"column:conversation_id"`
	HistoryID      string          `gorm:"column:history_id"`
	PluginID       string          `gorm:"column:plugin_id"`
	CurrentStepID  string          `gorm:"column:current_step_id"`
	Meta           json.RawMessage `gorm:"column:meta;type:jsonb"`
	CreateUserID   string          `gorm:"column:create_user_id"`
	IsActive       bool            `gorm:"column:is_active;default:true"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (PluginSession) TableName() string { return "plugin_sessions" }

// PluginSessionStep represents one step execution attempt.
// Retries of the same step produce new rows with a new ID (step_exec_id).
// StepStatus values: running | interrupted | done | failed | abandoned
type PluginSessionStep struct {
	ID            string    `gorm:"primaryKey;column:id"`
	SessionID     string    `gorm:"column:session_id"`
	Step          string    `gorm:"column:step"`
	StepMode      string    `gorm:"column:step_mode"`
	StepStatus    string    `gorm:"column:step_status"`
	LastHeartbeat time.Time `gorm:"column:last_heartbeat"`
	WorkspacePath string    `gorm:"column:workspace_path"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (PluginSessionStep) TableName() string { return "plugin_session_steps" }

// PluginSessionStepCheckpoint stores incremental progress for a step execution.
// Sequence increases monotonically; the highest sequence is the current checkpoint.
type PluginSessionStepCheckpoint struct {
	ID             string          `gorm:"primaryKey;column:id"`
	StepExecID     string          `gorm:"column:step_exec_id"`
	Sequence       int             `gorm:"column:sequence"`
	CompletedCount int             `gorm:"column:completed_count"`
	TotalCount     int             `gorm:"column:total_count"`
	PartialResults json.RawMessage `gorm:"column:partial_results;type:jsonb"`
	PhaseNote      string          `gorm:"column:phase_note"`
	CreatedAt      time.Time
}

func (PluginSessionStepCheckpoint) TableName() string { return "plugin_session_step_checkpoints" }

// PluginSessionArtifact stores the output of a step execution.
// The same artifact_id can appear multiple times when a step is retried;
// LoadPluginSessionArtifacts always returns the most recent value.
type PluginSessionArtifact struct {
	ID         string          `gorm:"primaryKey;column:id"`
	SessionID  string          `gorm:"column:session_id"`
	StepExecID string          `gorm:"column:step_exec_id"`
	ArtifactID string          `gorm:"column:artifact_id"`
	Value      json.RawMessage `gorm:"column:value;type:jsonb"`
	CreatedAt  time.Time
}

func (PluginSessionArtifact) TableName() string { return "plugin_session_artifacts" }
