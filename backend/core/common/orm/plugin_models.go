package orm

import (
	"encoding/json"
	"time"
)

// PluginSession represents one plugin workflow execution for a conversation.
// A conversation may have at most one active session at a time.
type PluginSession struct {
	ID               string `gorm:"column:id;type:varchar(36);primaryKey"`
	ConversationID   string `gorm:"column:conversation_id;type:varchar(36);not null"`
	PluginID         string `gorm:"column:plugin_id;type:varchar(64);not null"`
	TriggerHistoryID string `gorm:"column:trigger_history_id;type:varchar(36)"`
	// Status: active | completed | failed | waiting
	Status        string `gorm:"column:status;type:varchar(16);not null;default:active"`
	CurrentStepID string `gorm:"column:current_step_id;type:varchar(64)"`
	// Dismissed marks that the user has explicitly removed this session.
	// Orthogonal to Status: a dismissed session retains its last status for auditing
	// but is excluded from all active-session lookups.
	Dismissed bool `gorm:"column:dismissed;type:boolean;not null;default:false"`
	// IntentContext stores the global constraint/intent for this session (JSON string).
	IntentContext string    `gorm:"column:intent_context;type:text;not null;default:'{}'"`
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
//
// Value resolution (read path):
//   - AI revision:    ArtifactSeq != nil → value comes from sub_agent_artifacts at
//     (task_id via plugin_session_steps, artifact_key, seq=ArtifactSeq).
//   - Human revision: HumanArtifactID != nil → value comes from plugin_human_artifacts.
//   - Legacy fallback: both nil → value comes from ContentSnapshot (pre-migration rows).
type PluginSlotRevision struct {
	ID        string `gorm:"column:id;type:varchar(36);primaryKey"`
	SessionID string `gorm:"column:session_id;type:varchar(36);not null"`
	SlotID    string `gorm:"column:slot_id;type:varchar(64);not null"`
	Revision  int    `gorm:"column:revision;not null"`
	// ListIndex is the 0-based position within a cardinality=list slot; NULL for single.
	ListIndex *int `gorm:"column:list_index"`
	Selected  bool `gorm:"column:selected;not null;default:true"`
	// ArtifactSeq points to sub_agent_artifacts.seq for AI revisions.
	// NULL for human revisions.
	ArtifactSeq *int `gorm:"column:artifact_seq"`
	// HumanArtifactID points to plugin_human_artifacts.id for human revisions.
	// NULL for AI revisions.
	HumanArtifactID *string `gorm:"column:human_artifact_id;type:varchar(36)"`
	// ContentSnapshot is kept for legacy fallback (pre-migration AI rows where
	// artifact_seq was not yet populated, and pre-human_artifact_id human rows).
	ContentSnapshot json.RawMessage `gorm:"column:content_snapshot;type:jsonb"`
	// ChangeSource distinguishes AI-generated ('ai') from human-edited ('human') revisions.
	ChangeSource string    `gorm:"column:change_source;type:varchar(16);not null;default:'ai'"`
	ArtifactKey  string    `gorm:"column:artifact_key;type:varchar(255);not null"`
	StepID       string    `gorm:"column:step_id;type:varchar(64);not null"`
	Attempt      int       `gorm:"column:attempt;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
}

func (PluginSlotRevision) TableName() string { return "plugin_slot_revisions" }

// PluginSlotOrder tracks the display ordering of list-cardinality slot items.
// order_list is a JSONB array of list_index values in display order (visible items only).
// order_version is an optimistic-lock counter incremented on every reorder or delete.
type PluginSlotOrder struct {
	SessionID    string          `gorm:"column:session_id;type:varchar(36);not null;primaryKey"`
	SlotID       string          `gorm:"column:slot_id;type:varchar(64);not null;primaryKey"`
	OrderList    json.RawMessage `gorm:"column:order_list;type:jsonb;not null;default:'[]'"`
	OrderVersion int             `gorm:"column:order_version;not null;default:0"`
	UpdatedAt    time.Time       `gorm:"column:updated_at;not null"`
}

func (PluginSlotOrder) TableName() string { return "plugin_slot_order" }

// PluginStepIntent stores step-level intent/constraints set by the user during a session.
// There is at most one row per (session_id, step_id) pair; upserted on each update_intent call.
type PluginStepIntent struct {
	ID            string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SessionID     string    `gorm:"column:session_id;type:varchar(36);not null;uniqueIndex:uk_plugin_step_intent,priority:1"`
	StepID        string    `gorm:"column:step_id;type:varchar(64);not null;uniqueIndex:uk_plugin_step_intent,priority:2"`
	IntentContext string    `gorm:"column:intent_context;type:text;not null;default:'{}'"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null"`
}

func (PluginStepIntent) TableName() string { return "plugin_step_intents" }
