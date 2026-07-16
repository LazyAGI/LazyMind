package orm

import (
	"encoding/json"
	"time"
)

type PersonalResource struct {
	ID                 string          `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID             string          `gorm:"column:user_id;type:varchar(255);not null;uniqueIndex:uk_personal_resources_user_type,priority:1"`
	ResourceType       string          `gorm:"column:resource_type;type:varchar(64);not null;uniqueIndex:uk_personal_resources_user_type,priority:2"`
	HeadRevisionID     *string         `gorm:"column:head_revision_id;type:varchar(36)"`
	Version            int64           `gorm:"column:version;not null;default:1"`
	AutoEvo            bool            `gorm:"column:auto_evo;not null;default:true"`
	AutoEvoApplyStatus string          `gorm:"column:auto_evo_apply_status;type:varchar(32);not null;default:'idle'"`
	AutoEvoGeneration  int64           `gorm:"column:auto_evo_generation;not null;default:0"`
	AutoEvoStartedAt   *time.Time      `gorm:"column:auto_evo_started_at"`
	AutoEvoFinishedAt  *time.Time      `gorm:"column:auto_evo_finished_at"`
	AutoEvoError       string          `gorm:"column:auto_evo_error;type:text;not null;default:''"`
	Ext                json.RawMessage `gorm:"column:ext;type:json"`
	UpdatedBy          string          `gorm:"column:updated_by;type:varchar(255);not null;default:''"`
	UpdatedByName      string          `gorm:"column:updated_by_name;type:varchar(255);not null;default:''"`
	CreatedAt          time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt          time.Time       `gorm:"column:updated_at;not null"`
}

func (PersonalResource) TableName() string { return "personal_resources" }

type PersonalResourceBlob struct {
	Hash           string    `gorm:"column:hash;type:varchar(64);primaryKey"`
	Size           int64     `gorm:"column:size;not null"`
	Mime           string    `gorm:"column:mime;type:varchar(128)"`
	FileType       string    `gorm:"column:file_type;type:varchar(32);not null;default:'unknown'"`
	Binary         bool      `gorm:"column:binary;not null;default:false"`
	StorageBackend string    `gorm:"column:storage_backend;type:varchar(32);not null"`
	StorageKey     *string   `gorm:"column:storage_key;type:text"`
	Content        []byte    `gorm:"column:content;type:bytea"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (PersonalResourceBlob) TableName() string { return "personal_resource_blobs" }

type PersonalResourceRevision struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey"`
	ResourceID       string    `gorm:"column:resource_id;type:varchar(36);not null;uniqueIndex:uk_personal_resource_revisions_no,priority:1;index:idx_personal_resource_revisions_created,priority:1"`
	ParentRevisionID *string   `gorm:"column:parent_revision_id;type:varchar(36)"`
	RevisionNo       int64     `gorm:"column:revision_no;not null;uniqueIndex:uk_personal_resource_revisions_no,priority:2"`
	Path             string    `gorm:"column:path;type:varchar(1024);not null"`
	BlobHash         string    `gorm:"column:blob_hash;type:varchar(64);not null;index:idx_personal_resource_revisions_blob"`
	ContentHash      string    `gorm:"column:content_hash;type:varchar(64);not null"`
	Size             int64     `gorm:"column:size;not null;default:0"`
	Mime             string    `gorm:"column:mime;type:varchar(128)"`
	FileType         string    `gorm:"column:file_type;type:varchar(32);not null;default:'unknown'"`
	Binary           bool      `gorm:"column:binary;not null;default:false"`
	Message          string    `gorm:"column:message;type:text"`
	ChangeSource     string    `gorm:"column:change_source;type:varchar(32);not null;default:'draft_commit'"`
	SourceRefType    string    `gorm:"column:source_ref_type;type:varchar(64);not null;default:''"`
	SourceRefID      string    `gorm:"column:source_ref_id;type:varchar(128);not null;default:''"`
	CreatedBy        *string   `gorm:"column:created_by;type:varchar(255)"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;index:idx_personal_resource_revisions_created,priority:2"`
}

func (PersonalResourceRevision) TableName() string { return "personal_resource_revisions" }

type PersonalResourceDraft struct {
	ResourceID     string     `gorm:"column:resource_id;type:varchar(36);primaryKey"`
	BaseRevisionID *string    `gorm:"column:base_revision_id;type:varchar(36)"`
	Path           string     `gorm:"column:path;type:varchar(1024);not null"`
	BlobHash       string     `gorm:"column:blob_hash;type:varchar(64);not null;index:idx_personal_resource_drafts_blob"`
	ContentHash    string     `gorm:"column:content_hash;type:varchar(64);not null"`
	Size           int64      `gorm:"column:size;not null;default:0"`
	Mime           string     `gorm:"column:mime;type:varchar(128)"`
	FileType       string     `gorm:"column:file_type;type:varchar(32);not null;default:'unknown'"`
	Binary         bool       `gorm:"column:binary;not null;default:false"`
	DraftStatus    string     `gorm:"column:draft_status;type:varchar(32);not null;default:''"`
	DraftUpdatedAt *time.Time `gorm:"column:draft_updated_at"`
	TaskID         string     `gorm:"column:task_id;type:varchar(128);not null;default:''"`
	ConversationID *string    `gorm:"column:conversation_id;type:varchar(128)"`
	UpdatedBy      *string    `gorm:"column:updated_by;type:varchar(255)"`
	Version        int64      `gorm:"column:version;not null;default:1"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
}

func (PersonalResourceDraft) TableName() string { return "personal_resource_drafts" }

type PersonalResourceReviewSession struct {
	ID             string    `gorm:"column:id;type:varchar(36);primaryKey"`
	ResourceID     string    `gorm:"column:resource_id;type:varchar(36);not null;index:idx_personal_resource_review_sessions_resource_status,priority:1"`
	Path           string    `gorm:"column:path;type:varchar(1024);not null"`
	BaseRevisionID string    `gorm:"column:base_revision_id;type:varchar(36);not null"`
	HeadRevisionID string    `gorm:"column:head_revision_id;type:varchar(36);not null"`
	DraftVersion   int64     `gorm:"column:draft_version;not null"`
	DraftBlobHash  string    `gorm:"column:draft_blob_hash;type:varchar(64);not null"`
	ReviewVersion  int64     `gorm:"column:review_version;not null;default:1"`
	Status         string    `gorm:"column:status;type:varchar(32);not null;default:'active';index:idx_personal_resource_review_sessions_resource_status,priority:2"`
	CreatedBy      *string   `gorm:"column:created_by;type:varchar(255)"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (PersonalResourceReviewSession) TableName() string {
	return "personal_resource_review_sessions"
}

type PersonalResourceReviewActionBatch struct {
	ID                  string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SessionID           string    `gorm:"column:session_id;type:varchar(36);not null;index:idx_personal_resource_review_batches_session_created,priority:1"`
	ResourceID          string    `gorm:"column:resource_id;type:varchar(36);not null"`
	BeforeDraftBlobHash string    `gorm:"column:before_draft_blob_hash;type:varchar(64);not null"`
	AfterDraftBlobHash  string    `gorm:"column:after_draft_blob_hash;type:varchar(64);not null"`
	BeforeDraftVersion  int64     `gorm:"column:before_draft_version;not null"`
	AfterDraftVersion   int64     `gorm:"column:after_draft_version;not null"`
	ReviewVersion       int64     `gorm:"column:review_version;not null"`
	CreatedBy           *string   `gorm:"column:created_by;type:varchar(255)"`
	CreatedAt           time.Time `gorm:"column:created_at;not null;index:idx_personal_resource_review_batches_session_created,priority:2"`
}

func (PersonalResourceReviewActionBatch) TableName() string {
	return "personal_resource_review_action_batches"
}

type PersonalResourceReviewActionItem struct {
	ID        string    `gorm:"column:id;type:varchar(36);primaryKey"`
	BatchID   string    `gorm:"column:batch_id;type:varchar(36);not null;index:idx_personal_resource_review_items_batch"`
	HunkID    string    `gorm:"column:hunk_id;type:varchar(128);not null"`
	Decision  string    `gorm:"column:decision;type:varchar(16);not null"`
	OldStart  int       `gorm:"column:old_start;not null;default:0"`
	OldLines  int       `gorm:"column:old_lines;not null;default:0"`
	NewStart  int       `gorm:"column:new_start;not null;default:0"`
	NewLines  int       `gorm:"column:new_lines;not null;default:0"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (PersonalResourceReviewActionItem) TableName() string {
	return "personal_resource_review_action_items"
}
