package orm

import (
	"encoding/json"
	"time"
)

// SubAgentTask is an autonomous execution unit spawned by ChatAgent during ReAct reasoning.
type SubAgentTask struct {
	ID                 string          `gorm:"column:id;type:varchar(36);primaryKey"`
	ConversationID     string          `gorm:"column:conversation_id;type:varchar(36);not null"`
	TriggerHistoryID   string          `gorm:"column:trigger_history_id;type:varchar(36)"`
	SeqInConversation  int             `gorm:"column:seq_in_conversation;not null"`
	AgentType          string          `gorm:"column:agent_type;type:varchar(64);not null"`
	Title              string          `gorm:"column:title;type:varchar(255);not null"`
	Objective          string          `gorm:"column:objective;type:text;not null;default:''"`
	Params             json.RawMessage `gorm:"column:params;type:json"`
	Mode               string          `gorm:"column:mode;type:varchar(8);not null"`
	Status             string          `gorm:"column:status;type:varchar(16);not null;default:pending"`
	ProgressPct        int             `gorm:"column:progress_pct;not null;default:0"`
	CurrentPhase       string          `gorm:"column:current_phase;type:text"`
	EstimatedSec       int             `gorm:"column:estimated_sec"`
	Summary            string          `gorm:"column:summary;type:text;not null;default:''"`
	LastHeartbeat      time.Time       `gorm:"column:last_heartbeat;not null"`
	WorkspacePath      string          `gorm:"column:workspace_path;type:varchar(512);not null;default:''"`
	InputArtifactKeys  json.RawMessage `gorm:"column:input_artifact_keys;type:json;not null;default:'[]'"`
	OutputArtifactKeys json.RawMessage `gorm:"column:output_artifact_keys;type:json;not null;default:'[]'"`
	CreateUserID       string          `gorm:"column:create_user_id;type:varchar(255);not null;default:''"`
	CreatedAt          time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt          time.Time       `gorm:"column:updated_at;not null"`
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
	ArtifactKey string          `gorm:"column:artifact_key;type:varchar(64);not null"`
	ContentType string          `gorm:"column:content_type;type:varchar(32);not null"`
	Value       json.RawMessage `gorm:"column:value;type:json;not null"`
	Seq         int             `gorm:"column:seq;not null;default:1"`
	CreatedAt   time.Time       `gorm:"column:created_at;not null"`
}

func (SubAgentArtifact) TableName() string { return "sub_agent_artifacts" }
