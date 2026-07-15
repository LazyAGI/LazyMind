package orm

import (
	"encoding/json"
	"time"
)

// PluginSession represents one plugin workflow execution for a conversation.
// A conversation may have at most one active session at a time.
type PluginSession struct {
	ID                 string `gorm:"column:id;type:varchar(36);primaryKey"`
	ConversationID     string `gorm:"column:conversation_id;type:varchar(36);not null"`
	PluginID           string `gorm:"column:plugin_id;type:varchar(64);not null"`
	PluginRef          string `gorm:"column:plugin_ref;type:varchar(512);not null;default:''"`
	PluginRevisionID   string `gorm:"column:plugin_revision_id;type:varchar(36);not null;default:''"`
	PluginRevisionNo   int64  `gorm:"column:plugin_revision_no;not null;default:0"`
	PluginTreeHash     string `gorm:"column:plugin_tree_hash;type:varchar(64);not null;default:''"`
	PluginRemoteRoot   string `gorm:"column:plugin_remote_root;type:varchar(1024);not null;default:''"`
	StateVersion       int64  `gorm:"column:state_version;not null;default:0"`
	GraphHash          string `gorm:"column:graph_hash;type:varchar(64);not null;default:''"`
	GraphSchemaVersion string `gorm:"column:graph_schema_version;type:varchar(16);not null;default:''"`
	TriggerHistoryID   string `gorm:"column:trigger_history_id;type:varchar(36)"`
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
	Validity  string    `gorm:"column:validity;type:varchar(16);not null;default:effective"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (PluginSessionStep) TableName() string { return "plugin_session_steps" }

// PluginSlotRevision records one artifact write into a plugin panel slot.
// selected=true means this revision is the currently displayed version of the slot.
//
// Value resolution (read path):
//   - AI revision:    ArtifactSeq != nil → value comes from sub_agent_artifacts at
//     (task_id via plugin_session_steps, slot, seq=ArtifactSeq).
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
	ChangeSource      string    `gorm:"column:change_source;type:varchar(16);not null;default:'ai'"`
	Slot              string    `gorm:"column:slot;type:varchar(255);not null"`
	StepID            string    `gorm:"column:step_id;type:varchar(64);not null"`
	Attempt           int       `gorm:"column:attempt;not null"`
	Validity          string    `gorm:"column:validity;type:varchar(16);not null;default:effective"`
	ProducerAttemptID string    `gorm:"column:producer_attempt_id;type:varchar(36);not null;default:''"`
	CreatedAt         time.Time `gorm:"column:created_at;not null"`
}

func (PluginSlotRevision) TableName() string { return "plugin_slot_revisions" }

type PluginAttemptInputBinding struct {
	ID                 string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SessionID          string    `gorm:"column:session_id;type:varchar(36);not null;index"`
	AttemptID          string    `gorm:"column:attempt_id;type:varchar(36);not null;index"`
	MaterialID         string    `gorm:"column:material_id;type:varchar(64);not null"`
	MaterialRevisionID string    `gorm:"column:material_revision_id;type:varchar(36);not null;index"`
	BindAs             string    `gorm:"column:bind_as;type:varchar(64);not null;default:''"`
	CreatedAt          time.Time `gorm:"column:created_at;not null"`
}

func (PluginAttemptInputBinding) TableName() string { return "plugin_attempt_input_bindings" }

type PluginRouteDecision struct {
	ID              string          `gorm:"column:id;type:varchar(36);primaryKey"`
	SessionID       string          `gorm:"column:session_id;type:varchar(36);not null;index"`
	FromStepID      string          `gorm:"column:from_step_id;type:varchar(64);not null"`
	SourceAttemptID string          `gorm:"column:source_attempt_id;type:varchar(36);not null;default:''"`
	ActivatedJSON   json.RawMessage `gorm:"column:activated_json;type:jsonb;not null"`
	PrunedJSON      json.RawMessage `gorm:"column:pruned_json;type:jsonb;not null"`
	BypassedJSON    json.RawMessage `gorm:"column:bypassed_json;type:jsonb;not null"`
	WitnessJSON     json.RawMessage `gorm:"column:witness_json;type:jsonb;not null"`
	Validity        string          `gorm:"column:validity;type:varchar(16);not null;default:effective"`
	StateVersion    int64           `gorm:"column:state_version;not null"`
	CreatedAt       time.Time       `gorm:"column:created_at;not null"`
}

func (PluginRouteDecision) TableName() string { return "plugin_route_decisions" }

type PluginTransitionCommand struct {
	CommandID             string          `gorm:"column:command_id;type:varchar(36);primaryKey"`
	SessionID             string          `gorm:"column:session_id;type:varchar(36);not null;default:'';index"`
	Operation             string          `gorm:"column:operation;type:varchar(16);not null"`
	TargetStepID          string          `gorm:"column:target_step_id;type:varchar(64);not null;default:''"`
	Status                string          `gorm:"column:status;type:varchar(16);not null"`
	TaskID                string          `gorm:"column:task_id;type:varchar(36);not null;default:''"`
	ExpectedStateVersion  int64           `gorm:"column:expected_state_version;not null;default:0"`
	ResultingStateVersion int64           `gorm:"column:resulting_state_version;not null;default:0"`
	ResponseJSON          json.RawMessage `gorm:"column:response_json;type:jsonb;not null"`
	CreatedAt             time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time       `gorm:"column:updated_at;not null"`
}

func (PluginTransitionCommand) TableName() string { return "plugin_transition_commands" }

// PluginRunOutbox makes an accepted Plugin transition durably dispatchable.
// Payload contains the exact SubAgent RunRequest needed after a process restart.
type PluginRunOutbox struct {
	TaskID    string          `gorm:"column:task_id;type:varchar(36);primaryKey"`
	Payload   json.RawMessage `gorm:"column:payload;type:jsonb;not null"`
	Status    string          `gorm:"column:status;type:varchar(16);not null;index"`
	LastError string          `gorm:"column:last_error;type:text;not null;default:''"`
	CreatedAt time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt time.Time       `gorm:"column:updated_at;not null"`
}

func (PluginRunOutbox) TableName() string { return "plugin_run_outbox" }

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

// PluginDraft stores user-created plugin draft content.
// Each draft is owned by the creating user and represents a work-in-progress plugin.
// The original Content column is kept for backward compatibility; readers should prefer
// the split columns and fall back to Content when the split columns are empty.
type PluginDraft struct {
	ID        string    `gorm:"column:id;type:varchar(36);primaryKey"`
	Name      string    `gorm:"column:name;type:varchar(255);not null;default:''"`
	Content   string    `gorm:"column:content;type:text;not null;default:''"`
	CreatedBy string    `gorm:"column:created_by;type:varchar(255);not null;default:'';index:idx_plugin_drafts_created_by;uniqueIndex:idx_plugin_drafts_user_plugin_id,priority:1,where:plugin_id != ''"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
	// Split content columns (migration 20260706120000).
	// generate_status: '' | 'generating' | 'brief_done' | 'skeleton_done' | 'state_done' | 'done' | 'failed'
	//   ''             — never triggered AI generation
	//   'generating'   — Phase 0 in progress (design brief)
	//   'brief_done'   — Phase 0 complete; Phase 1 running
	//   'skeleton_done' — Phase 1 complete (plugin.yaml available); Phase 2 running
	//   'state_done'   — Phase 2 complete (state.yml available); Phase 3 running
	//   'done'         — All phases complete
	//   'failed'       — A phase failed; see generate_error for details
	PluginYAMLContent string `gorm:"column:plugin_yaml_content;type:text;not null;default:''"`
	StateYAMLContent  string `gorm:"column:state_yaml_content;type:text;not null;default:''"`
	// StateLayoutContent stores only the x-layout JSON (node positions/widths) extracted
	// from state.yml. Separated so layout drag-saves never contend with AI writes.
	// Saved with last-write-wins; no version check needed (single-user, AI never writes this).
	StateLayoutContent string `gorm:"column:state_layout_content;type:text;not null;default:''"`
	ScenarioContent    string `gorm:"column:scenario_content;type:text;not null;default:''"`
	ScriptsContent     string `gorm:"column:scripts_content;type:text;not null;default:'{}'"`
	GenerateStatus     string `gorm:"column:generate_status;type:varchar(32);not null;default:''"`
	// GenerateError stores the last error message when GenerateStatus = 'failed' (migration 20260707120000).
	GenerateError string `gorm:"column:generate_error;type:text;not null;default:''"`
	// GenerateWarning stores non-fatal warnings produced during generation (migration 20260709120000).
	// Non-empty when GenerateStatus = 'done' but some fields were incomplete after retries.
	GenerateWarning string `gorm:"column:generate_warning;type:text;not null;default:''"`
	// Version is an optimistic-lock counter. SavePluginDraft increments it on every
	// successful write to plugin_yaml_content or state_yaml_content and rejects saves
	// that arrive with a stale version (returns 409 Conflict).
	// AI generate_job writes bypass the version check (it only writes its own fields).
	Version int `gorm:"column:version;type:int;not null;default:1"`
	// Source tracking (migration 20260709130000).
	// SourceType: '' | 'ai' | 'skill' | 'blank'
	SourceType            string `gorm:"column:source_type;type:varchar(16);not null;default:''"`
	SourceSkillID         string `gorm:"column:source_skill_id;type:varchar(36);not null;default:''"`
	SourceSkillName       string `gorm:"column:source_skill_name;type:varchar(255);not null;default:''"`
	SourceSkillRevisionID string `gorm:"column:source_skill_revision_id;type:varchar(36);not null;default:''"`
	SourceSkillRevisionNo int64  `gorm:"column:source_skill_revision_no;not null;default:0"`
	SourceSkillTreeHash   string `gorm:"column:source_skill_tree_hash;type:varchar(64);not null;default:''"`
	SourceAnalysisID      string `gorm:"column:source_analysis_id;type:varchar(36);not null;default:''"`
	// DesignBriefContent stores the Phase 0 design brief Markdown (migration 20260709140000).
	// Empty for old drafts that were generated before Phase 0 was introduced.
	DesignBriefContent string `gorm:"column:design_brief_content;type:text;not null;default:''"`
	// PluginID mirrors the `id:` field inside PluginYAMLContent (migration 20260709150000).
	// Kept in sync on every save that touches PluginYAMLContent.
	// A partial unique index (created_by, plugin_id) WHERE plugin_id != '' enforces
	// per-user uniqueness while allowing legacy empty-string rows to coexist.
	PluginID       string `gorm:"column:plugin_id;type:varchar(255);not null;default:'';uniqueIndex:idx_plugin_drafts_user_plugin_id,priority:2,where:plugin_id != ''"`
	BaseRevisionID string `gorm:"column:base_revision_id;type:varchar(36);not null;default:''"`
}

func (PluginDraft) TableName() string { return "plugin_drafts" }

type PluginGenerationAnalysis struct {
	ID, DraftID, UserID, SourceType, SourceSkillID           string
	SourceSkillRevisionID                                    string
	SourceSkillRevisionNo                                    int64
	SourceSkillTreeHash, Status, VerdictCode, VerdictMessage string
	CandidatesJSON, SelectedCandidateID, CoverageReportJSON  string
	ToolMappingReportJSON, ScriptReportJSON                  string
	SourcePackageJSON                                        string
	CreatedAt, UpdatedAt                                     time.Time
}

func (PluginGenerationAnalysis) TableName() string { return "plugin_generation_analyses" }

type PluginRepairRun struct {
	ID, DraftID, UserID, BasePluginRevisionID                         string
	DraftVersionBefore                                                int
	Target, Mode, SourceAnalysisID, SourceSkillRevisionID, RepairHint string
	DiagnosticsBeforeJSON, ChangesJSON, DiagnosticsAfterJSON, Status  string
	CreatedAt, UpdatedAt                                              time.Time
}

func (PluginRepairRun) TableName() string { return "plugin_repair_runs" }

type PluginResource struct {
	ID              string    `gorm:"column:id;type:varchar(36);primaryKey"`
	PluginRef       string    `gorm:"column:plugin_ref;type:varchar(512);not null;uniqueIndex"`
	PluginID        string    `gorm:"column:plugin_id;type:varchar(255);not null"`
	OwnerUserID     string    `gorm:"column:owner_user_id;type:varchar(255);not null;index:idx_plugins_owner,priority:1"`
	OwnerScope      string    `gorm:"column:owner_scope;type:varchar(128);not null"`
	SourceType      string    `gorm:"column:source_type;type:varchar(16);not null;default:'user'"`
	RelativeRoot    string    `gorm:"column:relative_root;type:varchar(1024);not null;uniqueIndex"`
	Name            string    `gorm:"column:name;type:varchar(255);not null;default:''"`
	Description     string    `gorm:"column:description;type:text;not null;default:''"`
	WhenToUse       string    `gorm:"column:when_to_use;type:text;not null;default:''"`
	HeadRevisionID  string    `gorm:"column:head_revision_id;type:varchar(36)"`
	Version         int64     `gorm:"column:version;not null;default:0"`
	Status          string    `gorm:"column:status;type:varchar(16);not null;default:'active';index:idx_plugins_owner,priority:2"`
	ContainsScripts bool      `gorm:"column:contains_scripts;not null;default:false"`
	CreatedAt       time.Time `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time `gorm:"column:updated_at;not null"`
}

func (PluginResource) TableName() string { return "plugins" }

type PluginBlob struct {
	Hash      string    `gorm:"column:hash;type:varchar(64);primaryKey"`
	Size      int64     `gorm:"column:size;not null"`
	Mime      string    `gorm:"column:mime;type:varchar(128)"`
	FileType  string    `gorm:"column:file_type;type:varchar(32);not null;default:'unknown'"`
	Binary    bool      `gorm:"column:is_binary;not null;default:false"`
	Content   []byte    `gorm:"column:content;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (PluginBlob) TableName() string { return "plugin_blobs" }

type PluginRevision struct {
	ID                 string          `gorm:"column:id;type:varchar(36);primaryKey"`
	PluginResourceID   string          `gorm:"column:plugin_resource_id;type:varchar(36);not null;uniqueIndex:uk_plugin_revisions_resource_no,priority:1;index:idx_plugin_revisions_resource"`
	ParentRevisionID   string          `gorm:"column:parent_revision_id;type:varchar(36)"`
	RevisionNo         int64           `gorm:"column:revision_no;not null;uniqueIndex:uk_plugin_revisions_resource_no,priority:2"`
	TreeHash           string          `gorm:"column:tree_hash;type:varchar(64);not null"`
	CompiledGraph      json.RawMessage `gorm:"column:compiled_graph;type:jsonb"`
	GraphHash          string          `gorm:"column:graph_hash;type:varchar(64);not null;default:''"`
	GraphSchemaVersion string          `gorm:"column:graph_schema_version;type:varchar(16);not null;default:''"`
	Message            string          `gorm:"column:message;type:text;not null;default:''"`
	CreatedBy          string          `gorm:"column:created_by;type:varchar(255)"`
	CreatedAt          time.Time       `gorm:"column:created_at;not null"`
}

func (PluginRevision) TableName() string { return "plugin_revisions" }

type PluginRevisionEntry struct {
	RevisionID string  `gorm:"column:revision_id;type:varchar(36);primaryKey"`
	Path       string  `gorm:"column:path;type:varchar(1024);primaryKey"`
	EntryType  string  `gorm:"column:entry_type;type:varchar(16);not null;default:'file'"`
	BlobHash   *string `gorm:"column:blob_hash;type:varchar(64)"`
	Size       int64   `gorm:"column:size;not null;default:0"`
	Mime       string  `gorm:"column:mime;type:varchar(128)"`
	FileType   string  `gorm:"column:file_type;type:varchar(32);not null;default:'unknown'"`
	Binary     bool    `gorm:"column:is_binary;not null;default:false"`
	Mode       int     `gorm:"column:mode;not null;default:420"`
}

func (PluginRevisionEntry) TableName() string { return "plugin_revision_entries" }

type UserPluginSetting struct {
	UserID    string    `gorm:"column:user_id;type:varchar(255);primaryKey"`
	PluginRef string    `gorm:"column:plugin_ref;type:varchar(512);primaryKey"`
	Enabled   bool      `gorm:"column:enabled;not null;default:false"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (UserPluginSetting) TableName() string { return "user_plugin_settings" }
