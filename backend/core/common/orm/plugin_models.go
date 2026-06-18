package orm

import "time"

// PluginSession represents one plugin workflow execution for a conversation.
// A conversation may have at most one active session at a time.
type PluginSession struct {
	ID               string `gorm:"column:id;type:varchar(36);primaryKey"`
	ConversationID   string `gorm:"column:conversation_id;type:varchar(36);not null"`
	PluginID         string `gorm:"column:plugin_id;type:varchar(64);not null"`
	TriggerHistoryID string `gorm:"column:trigger_history_id;type:varchar(36)"`
	// Status: active | completed | failed | waiting
	Status        string    `gorm:"column:status;type:varchar(16);not null;default:active"`
	CurrentStepID string    `gorm:"column:current_step_id;type:varchar(64)"`
	CreateUserID  string    `gorm:"column:create_user_id;type:varchar(255);not null;default:''"`
	CreatedAt     time.Time `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null"`
}

func (PluginSession) TableName() string { return "plugin_sessions" }

// PluginSessionStep tracks one step execution instance inside a plugin session.
// Each record maps to exactly one sub_agent_tasks row (task_id == sub_agent_tasks.id).
type PluginSessionStep struct {
	ID        string `gorm:"column:id;type:varchar(36);primaryKey"`
	SessionID string `gorm:"column:session_id;type:varchar(36);not null"`
	StepID    string `gorm:"column:step_id;type:varchar(64);not null"`
	Attempt   int    `gorm:"column:attempt;not null;default:1"`
	TaskID    string `gorm:"column:task_id;type:varchar(36);not null"`
	// Status mirrors sub_agent_tasks.status (synced by Go on each event).
	Status    string    `gorm:"column:status;type:varchar(16);not null;default:pending"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (PluginSessionStep) TableName() string { return "plugin_session_steps" }

// PluginSlotRevision records one artifact write into a plugin panel slot.
// selected=true means this revision is the currently displayed version of the slot.
type PluginSlotRevision struct {
	ID        string `gorm:"column:id;type:varchar(36);primaryKey"`
	SessionID string `gorm:"column:session_id;type:varchar(36);not null"`
	SlotID    string `gorm:"column:slot_id;type:varchar(64);not null"`
	Revision  int    `gorm:"column:revision;not null"`
	// ListIndex is the 0-based position within a cardinality=list slot; NULL for single.
	ListIndex   *int      `gorm:"column:list_index"`
	Selected    bool      `gorm:"column:selected;not null;default:true"`
	ArtifactKey string    `gorm:"column:artifact_key;type:varchar(255);not null"`
	StepID      string    `gorm:"column:step_id;type:varchar(64);not null"`
	Attempt     int       `gorm:"column:attempt;not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
}

func (PluginSlotRevision) TableName() string { return "plugin_slot_revisions" }
