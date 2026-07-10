package orm

import "time"

type SkillV2Skill struct {
	ID                    string     `gorm:"column:id;type:varchar(36);primaryKey"`
	OwnerUserID           string     `gorm:"column:owner_user_id;type:varchar(255);not null;uniqueIndex:uk_skills_owner_identity,priority:1;uniqueIndex:uk_skills_owner_relative_root,priority:1"`
	OwnerUserName         string     `gorm:"column:owner_user_name;type:varchar(255);not null;default:''"`
	CreateUserID          string     `gorm:"column:create_user_id;type:varchar(255);not null"`
	CreateUserName        string     `gorm:"column:create_user_name;type:varchar(255);not null;default:''"`
	Category              string     `gorm:"column:category;type:varchar(128);not null;uniqueIndex:uk_skills_owner_identity,priority:2"`
	SkillName             string     `gorm:"column:skill_name;type:varchar(255);not null;uniqueIndex:uk_skills_owner_identity,priority:3"`
	OriginBuiltinSkillUID string     `gorm:"column:origin_builtin_skill_uid;type:varchar(64);not null;default:''"`
	Description           string     `gorm:"column:description;type:text"`
	Tags                  []byte     `gorm:"column:tags;type:json"`
	RelativeRoot          string     `gorm:"column:relative_root;type:varchar(1024);not null;uniqueIndex:uk_skills_owner_relative_root,priority:2"`
	SkillMDPath           string     `gorm:"column:skill_md_path;type:varchar(1024);not null;default:'SKILL.md'"`
	HeadRevisionID        *string    `gorm:"column:head_revision_id;type:varchar(36)"`
	Version               int64      `gorm:"column:version;not null;default:1"`
	AutoEvo               bool       `gorm:"column:auto_evo;not null;default:false"`
	AutoEvoApplyStatus    string     `gorm:"column:auto_evo_apply_status;type:varchar(32);not null;default:'idle'"`
	AutoEvoGeneration     int64      `gorm:"column:auto_evo_generation;not null;default:0"`
	AutoEvoStartedAt      *time.Time `gorm:"column:auto_evo_started_at"`
	AutoEvoFinishedAt     *time.Time `gorm:"column:auto_evo_finished_at"`
	AutoEvoError          string     `gorm:"column:auto_evo_error;type:text;not null;default:''"`
	IsEnabled             bool       `gorm:"column:is_enabled;not null;default:true"`
	UpdateStatus          string     `gorm:"column:update_status;type:varchar(32);not null;default:'up_to_date'"`
	Ext                   []byte     `gorm:"column:ext;type:json"`
	DeletedAt             *time.Time `gorm:"column:deleted_at"`
	DeletedBy             *string    `gorm:"column:deleted_by;type:varchar(255)"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time  `gorm:"column:updated_at;not null"`
}

func (SkillV2Skill) TableName() string { return "skills" }

type SkillV2Blob struct {
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

func (SkillV2Blob) TableName() string { return "skill_blobs" }

type SkillV2Revision struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID          string    `gorm:"column:skill_id;type:varchar(36);not null;uniqueIndex:uk_skill_revisions_skill_no,priority:1"`
	ParentRevisionID *string   `gorm:"column:parent_revision_id;type:varchar(36)"`
	RevisionNo       int64     `gorm:"column:revision_no;not null;uniqueIndex:uk_skill_revisions_skill_no,priority:2"`
	TreeHash         string    `gorm:"column:tree_hash;type:varchar(64);not null"`
	Message          string    `gorm:"column:message;type:text"`
	ChangeSource     string    `gorm:"column:change_source;type:varchar(32);not null;default:'draft_commit'"`
	SourceRefType    string    `gorm:"column:source_ref_type;type:varchar(64);not null;default:''"`
	SourceRefID      string    `gorm:"column:source_ref_id;type:varchar(128);not null;default:''"`
	CreatedBy        *string   `gorm:"column:created_by;type:varchar(255)"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
}

func (SkillV2Revision) TableName() string { return "skill_revisions" }

type SkillV2RevisionEntry struct {
	RevisionID string  `gorm:"column:revision_id;type:varchar(36);primaryKey"`
	Path       string  `gorm:"column:path;type:varchar(1024);primaryKey"`
	EntryType  string  `gorm:"column:entry_type;type:varchar(16);not null"`
	BlobHash   *string `gorm:"column:blob_hash;type:varchar(64)"`
	Size       int64   `gorm:"column:size"`
	Mime       string  `gorm:"column:mime;type:varchar(128)"`
	FileType   string  `gorm:"column:file_type;type:varchar(32);not null;default:'unknown'"`
	Binary     bool    `gorm:"column:binary;not null;default:false"`
	Mode       int     `gorm:"column:mode;not null;default:420"`
}

func (SkillV2RevisionEntry) TableName() string { return "skill_revision_entries" }

type SkillV2Draft struct {
	SkillID        string     `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	BaseRevisionID *string    `gorm:"column:base_revision_id;type:varchar(36)"`
	DraftStatus    string     `gorm:"column:draft_status;type:varchar(32);not null;default:''"`
	DraftUpdatedAt *time.Time `gorm:"column:draft_updated_at"`
	TaskID         string     `gorm:"column:task_id;type:varchar(128);not null;default:''"`
	ConversationID *string    `gorm:"column:conversation_id;type:varchar(36)"`
	UpdatedBy      *string    `gorm:"column:updated_by;type:varchar(255)"`
	Version        int64      `gorm:"column:version;not null;default:1"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
}

func (SkillV2Draft) TableName() string { return "skill_drafts" }

type SkillV2DraftEntry struct {
	SkillID   string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	Path      string    `gorm:"column:path;type:varchar(1024);primaryKey"`
	Op        string    `gorm:"column:op;type:varchar(16);not null"`
	EntryType string    `gorm:"column:entry_type;type:varchar(16)"`
	BlobHash  *string   `gorm:"column:blob_hash;type:varchar(64)"`
	Size      int64     `gorm:"column:size"`
	Mime      string    `gorm:"column:mime;type:varchar(128)"`
	FileType  string    `gorm:"column:file_type;type:varchar(32)"`
	Binary    bool      `gorm:"column:binary"`
	Mode      int       `gorm:"column:mode"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (SkillV2DraftEntry) TableName() string { return "skill_draft_entries" }

type SkillMarketItem struct {
	ID            string     `gorm:"column:id;type:varchar(36);primaryKey"`
	SourceSkillID string     `gorm:"column:source_skill_id;type:varchar(36);not null"`
	Status        string     `gorm:"column:status;type:varchar(32);not null;default:'draft'"`
	Icon          string     `gorm:"column:icon;type:text;not null;default:''"`
	SortOrder     int        `gorm:"column:sort_order;not null;default:0"`
	VersionNote   string     `gorm:"column:version_note;type:text;not null;default:''"`
	CreatedBy     *string    `gorm:"column:created_by;type:varchar(255)"`
	UpdatedBy     *string    `gorm:"column:updated_by;type:varchar(255)"`
	PublishedAt   *time.Time `gorm:"column:published_at"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null"`
}

func (SkillMarketItem) TableName() string { return "skill_market_items" }

type SkillSearchIndex struct {
	SkillID        string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	OwnerUserID    string    `gorm:"column:owner_user_id;type:varchar(255);not null;index:idx_skill_search_owner"`
	HeadRevisionID string    `gorm:"column:head_revision_id;type:varchar(36);not null"`
	Content        string    `gorm:"column:content;type:text;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (SkillSearchIndex) TableName() string { return "skill_search_indexes" }

type SkillDraftReviewSession struct {
	ID                  string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID             string    `gorm:"column:skill_id;type:varchar(36);not null;index:idx_skill_draft_review_sessions_skill_status,priority:1"`
	BaseRevisionID      string    `gorm:"column:base_revision_id;type:varchar(36);not null"`
	DraftVersionAtStart int64     `gorm:"column:draft_version_at_start;not null"`
	DraftSnapshotHash   string    `gorm:"column:draft_snapshot_hash;type:varchar(64);not null"`
	Status              string    `gorm:"column:status;type:varchar(32);not null;default:'active';index:idx_skill_draft_review_sessions_skill_status,priority:2"`
	Version             int64     `gorm:"column:version;not null;default:1"`
	UndoLimit           int       `gorm:"column:undo_limit;not null;default:20"`
	CreatedBy           *string   `gorm:"column:created_by;type:varchar(255)"`
	UpdatedBy           *string   `gorm:"column:updated_by;type:varchar(255)"`
	CreatedAt           time.Time `gorm:"column:created_at;not null"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null;index:idx_skill_draft_review_sessions_skill_status,priority:3"`
}

func (SkillDraftReviewSession) TableName() string { return "skill_draft_review_sessions" }

type SkillDraftReviewActionBatch struct {
	ID              string     `gorm:"column:id;type:varchar(36);primaryKey"`
	ReviewSessionID string     `gorm:"column:review_session_id;type:varchar(36);not null;uniqueIndex:uk_skill_draft_review_batch_sequence,priority:1;index:idx_skill_draft_review_batches_session_created,priority:1"`
	Sequence        int64      `gorm:"column:sequence;not null;uniqueIndex:uk_skill_draft_review_batch_sequence,priority:2"`
	UndoLocked      bool       `gorm:"column:undo_locked;not null;default:false"`
	UndoneAt        *time.Time `gorm:"column:undone_at"`
	UndoneBy        *string    `gorm:"column:undone_by;type:varchar(255)"`
	CreatedBy       *string    `gorm:"column:created_by;type:varchar(255)"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;index:idx_skill_draft_review_batches_session_created,priority:2"`
}

func (SkillDraftReviewActionBatch) TableName() string {
	return "skill_draft_review_action_batches"
}

type SkillDraftReviewActionItem struct {
	ID              string    `gorm:"column:id;type:varchar(36);primaryKey"`
	BatchID         string    `gorm:"column:batch_id;type:varchar(36);not null;index:idx_skill_draft_review_items_batch"`
	ReviewSessionID string    `gorm:"column:review_session_id;type:varchar(36);not null;index:idx_skill_draft_review_items_session_hunk,priority:1"`
	Path            string    `gorm:"column:path;type:varchar(1024);not null;index:idx_skill_draft_review_items_session_hunk,priority:2"`
	HunkID          string    `gorm:"column:hunk_id;type:varchar(128);not null;index:idx_skill_draft_review_items_session_hunk,priority:3"`
	BeforeDecision  string    `gorm:"column:before_decision;type:varchar(16);not null;default:'pending'"`
	AfterDecision   string    `gorm:"column:after_decision;type:varchar(16);not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null"`
}

func (SkillDraftReviewActionItem) TableName() string {
	return "skill_draft_review_action_items"
}
