package orm

import (
	"encoding/json"
	"time"
)

// ConversationGroup is an active, user-owned navigation group. Scope is an
// optional user-authored inclusion rule used by future organizer runs.
type ConversationGroup struct {
	Pinned         bool       `gorm:"column:pinned;not null;default:false"`
	SortOrder      int64      `gorm:"column:sort_order;not null;default:0"`
	ID             string     `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID         string     `gorm:"column:user_id;type:varchar(255);not null;uniqueIndex:uk_conversation_groups_user_name,priority:1"`
	Name           string     `gorm:"column:name;type:varchar(255);not null"`
	NormalizedName string     `gorm:"column:normalized_name;type:varchar(255);not null;uniqueIndex:uk_conversation_groups_user_name,priority:2"`
	Scope          string     `gorm:"column:scope;type:text;not null;default:''"`
	Version        int64      `gorm:"column:version;not null;default:1"`
	CreatedBy      string     `gorm:"column:created_by;type:varchar(16);not null;default:'user'"`
	CreatedRunID   string     `gorm:"column:created_run_id;type:varchar(64);not null;default:'';index"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
	DeletedAt      *time.Time `gorm:"column:deleted_at;index"`
}

func (ConversationGroup) TableName() string { return "conversation_groups" }

type ConversationGroupMember struct {
	ConversationID string    `gorm:"column:conversation_id;type:varchar(36);primaryKey"`
	GroupID        string    `gorm:"column:group_id;type:varchar(36);not null;index"`
	UserID         string    `gorm:"column:user_id;type:varchar(255);not null;index"`
	Revision       int64     `gorm:"column:revision;not null;default:1"`
	Source         string    `gorm:"column:source;type:varchar(16);not null;default:'user'"`
	SourceRunID    string    `gorm:"column:source_run_id;type:varchar(64);not null;default:'';index"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (ConversationGroupMember) TableName() string { return "conversation_group_members" }

// ConversationGroupState fences membership changes even while a conversation
// is free (and therefore has no membership row). This prevents undo ABA races.
type ConversationGroupState struct {
	ConversationID string    `gorm:"column:conversation_id;type:varchar(36);primaryKey"`
	UserID         string    `gorm:"column:user_id;type:varchar(255);not null;index"`
	GroupID        *string   `gorm:"column:group_id;type:varchar(36)"`
	Revision       int64     `gorm:"column:revision;not null;default:1"`
	SourceRunID    string    `gorm:"column:source_run_id;type:varchar(64);not null;default:'';index"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (ConversationGroupState) TableName() string { return "conversation_group_states" }

type ConversationOrganizerRun struct {
	ID              string          `gorm:"column:id;type:varchar(64);primaryKey"`
	UserID          string          `gorm:"column:user_id;type:varchar(255);not null;index"`
	Status          string          `gorm:"column:status;type:varchar(16);not null;index"`
	Stage           string          `gorm:"column:stage;type:varchar(32);not null;default:'snapshot'"`
	SnapshotJSON    json.RawMessage `gorm:"column:snapshot_json;type:json;not null"`
	SnapshotHash    string          `gorm:"column:snapshot_hash;type:varchar(64);not null"`
	ModelConfigJSON json.RawMessage `gorm:"column:model_config_json;type:json;not null"`
	PreparationJSON json.RawMessage `gorm:"column:preparation_json;type:json"`
	StreamJSON      json.RawMessage `gorm:"column:stream_json;type:json"`
	CheckpointJSON  json.RawMessage `gorm:"column:checkpoint_json;type:json"`
	ProposalJSON    json.RawMessage `gorm:"column:proposal_json;type:json"`
	ResultJSON      json.RawMessage `gorm:"column:result_json;type:json"`
	ProgressCurrent int64           `gorm:"column:progress_current;not null;default:0"`
	ProgressTotal   int64           `gorm:"column:progress_total;not null;default:0"`
	Version         int64           `gorm:"column:version;not null;default:1"`
	JobID           string          `gorm:"column:job_id;type:varchar(64);not null;default:'';index"`
	ErrorCode       string          `gorm:"column:error_code;type:varchar(64);not null;default:''"`
	ErrorMessage    string          `gorm:"column:error_message;type:text;not null;default:''"`
	CreatedAt       time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time       `gorm:"column:updated_at;not null"`
	FinishedAt      *time.Time      `gorm:"column:finished_at"`
	UndoneAt        *time.Time      `gorm:"column:undone_at"`
}

func (ConversationOrganizerRun) TableName() string { return "conversation_organizer_runs" }

// These rows freeze all existing free conversations as persistent locks; the run
// SnapshotJSON separately holds only conversations eligible for analysis. A lock is active while
// its owning run is pending, running, or applying.
type ConversationOrganizerSnapshotItem struct {
	Ordinal           int             `gorm:"column:ordinal;not null;default:0"`
	FrozenInput       json.RawMessage `gorm:"column:frozen_input;type:json"`
	PreparationStatus string          `gorm:"column:preparation_status;type:varchar(16);not null;default:''"`
	PreparationReason string          `gorm:"column:preparation_reason;type:varchar(64);not null;default:''"`
	PreparationError  string          `gorm:"column:preparation_error;type:varchar(64);not null;default:''"`
	Assignment        string          `gorm:"column:assignment;type:varchar(255);not null;default:''"`

	RunID            string    `gorm:"column:run_id;type:varchar(64);primaryKey"`
	ConversationID   string    `gorm:"column:conversation_id;type:varchar(36);primaryKey;index"`
	UserID           string    `gorm:"column:user_id;type:varchar(255);not null;index"`
	Title            string    `gorm:"column:title;type:text;not null"`
	Summary          string    `gorm:"column:summary;type:text;not null"`
	TitleRevision    int64     `gorm:"column:title_revision;not null;default:0"`
	MetadataRevision int64     `gorm:"column:metadata_revision;not null;default:0"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
}

func (ConversationOrganizerSnapshotItem) TableName() string {
	return "conversation_organizer_snapshot_items"
}

// Change stores both the organizer's initial move and immediate result-panel
// corrections. Revision fencing prevents undo from overwriting later edits.
type ConversationOrganizerChange struct {
	ID                  string     `gorm:"column:id;type:varchar(64);primaryKey"`
	RunID               string     `gorm:"column:run_id;type:varchar(64);not null;index"`
	ConversationID      string     `gorm:"column:conversation_id;type:varchar(36);not null;index"`
	BeforeGroupID       *string    `gorm:"column:before_group_id;type:varchar(36)"`
	AfterGroupID        *string    `gorm:"column:after_group_id;type:varchar(36)"`
	AfterMemberRevision int64      `gorm:"column:after_member_revision;not null;default:0"`
	Kind                string     `gorm:"column:kind;type:varchar(16);not null"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null"`
	UndoneAt            *time.Time `gorm:"column:undone_at"`
}

func (ConversationOrganizerChange) TableName() string { return "conversation_organizer_changes" }

// Directory rows contain only bounded cards, never full member lists.
type ConversationOrganizerCandidate struct {
	RunID string          `gorm:"column:run_id;type:varchar(64);primaryKey"`
	ID    string          `gorm:"column:id;type:varchar(255);primaryKey"`
	Data  json.RawMessage `gorm:"column:data;type:json;not null"`
}

func (ConversationOrganizerCandidate) TableName() string { return "conversation_organizer_candidates" }
