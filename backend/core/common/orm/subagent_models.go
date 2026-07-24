package orm

import (
	"encoding/json"
	"time"
)

// SubAgentTask is an autonomous execution unit spawned by ChatAgent during ReAct reasoning.
type SubAgentTask struct {
	ID                string          `gorm:"column:id;type:varchar(36);primaryKey"`
	ConversationID    string          `gorm:"column:conversation_id;type:varchar(36);not null"`
	TriggerHistoryID  string          `gorm:"column:trigger_history_id;type:varchar(36)"`
	SeqInConversation int             `gorm:"column:seq_in_conversation;not null"`
	AgentType         string          `gorm:"column:agent_type;type:varchar(64);not null"`
	Title             string          `gorm:"column:title;type:varchar(255);not null"`
	Objective         string          `gorm:"column:objective;type:text;not null;default:''"`
	Params            json.RawMessage `gorm:"column:params;type:json"`
	Mode              string          `gorm:"column:mode;type:varchar(8);not null"`
	Status            string          `gorm:"column:status;type:varchar(16);not null;default:pending"`
	ProgressPct       int             `gorm:"column:progress_pct;not null;default:0"`
	CurrentPhase      string          `gorm:"column:current_phase;type:text"`
	EstimatedSec      int             `gorm:"column:estimated_sec"`
	Summary           string          `gorm:"column:summary;type:text;not null;default:''"`
	LastHeartbeat     time.Time       `gorm:"column:last_heartbeat;not null"`
	WorkspacePath     string          `gorm:"column:workspace_path;type:varchar(512);not null;default:''"`
	InputSlots        json.RawMessage `gorm:"column:input_slots;type:json;not null;default:'[]'"`
	OutputSlots       json.RawMessage `gorm:"column:output_slots;type:json;not null;default:'[]'"`
	CreateUserID      string          `gorm:"column:create_user_id;type:varchar(255);not null;default:''"`
	CreatedAt         time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt         time.Time       `gorm:"column:updated_at;not null"`
}

func (SubAgentTask) TableName() string { return "sub_agent_tasks" }

// SubAgentStep is a ReAct step persisted for resume (assistant reasoning / tool results).
type SubAgentStep struct {
	ID        string          `gorm:"column:id;type:varchar(36);primaryKey"`
	TaskID    string          `gorm:"column:task_id;type:varchar(36);not null"`
	Seq       int             `gorm:"column:seq;not null"`
	Role      string          `gorm:"column:role;type:varchar(16);not null"`
	Content   json.RawMessage `gorm:"column:content;type:json;not null"`
	CreatedAt time.Time       `gorm:"column:created_at;not null"`
}

func (SubAgentStep) TableName() string { return "sub_agent_steps" }

// SubAgentArtifact is an output produced by a SubAgent via save_artifact.
type SubAgentArtifact struct {
	ID          string          `gorm:"column:id;type:varchar(36);primaryKey"`
	TaskID      string          `gorm:"column:task_id;type:varchar(36);not null"`
	Slot        string          `gorm:"column:slot;type:varchar(64);not null"`
	ContentType string          `gorm:"column:content_type;type:varchar(32);not null"`
	Value       json.RawMessage `gorm:"column:value;type:json;not null"`
	Seq         int             `gorm:"column:seq;not null;default:1"`
	// Hidden marks logically-deleted list items; list_index is never reused.
	Hidden bool `gorm:"column:hidden;not null;default:false"`
	// Caption is a human-readable description for image/file artifacts, used in artifact_summary.
	Caption   *string   `gorm:"column:caption"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (SubAgentArtifact) TableName() string { return "sub_agent_artifacts" }

// PluginHumanArtifact stores content written by human edits to plugin slots.
// Structure mirrors SubAgentArtifact but scoped to a session rather than a task.
// Value format is identical to SubAgentArtifact.Value.
type PluginHumanArtifact struct {
	ID          string          `gorm:"column:id;type:varchar(36);primaryKey"`
	SessionID   string          `gorm:"column:session_id;type:varchar(36);not null"`
	Slot        string          `gorm:"column:slot;type:varchar(64);not null"`
	ContentType string          `gorm:"column:content_type;type:varchar(32);not null"`
	Value       json.RawMessage `gorm:"column:value;type:jsonb;not null"`
	Caption     *string         `gorm:"column:caption"`
	CreatedAt   time.Time       `gorm:"column:created_at;not null"`
}

func (PluginHumanArtifact) TableName() string { return "plugin_human_artifacts" }
