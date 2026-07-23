package testutil

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type TestDB struct {
	*gorm.DB
}

func NewTestDB(t *testing.T) *TestDB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "skillv2.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect sqlite test db: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.ResourceUpdateTask{},
		&SkillRow{},
		&SkillBlobRow{},
		&SkillRevisionRow{},
		&SkillRevisionEntryRow{},
		&SkillDraftRow{},
		&SkillDraftEntryRow{},
		&SkillDraftReviewSessionRow{},
		&SkillDraftReviewActionBatchRow{},
		&SkillDraftReviewActionItemRow{},
		&SkillMarketItemRow{},
		&SkillMarketInstallRow{},
		&SkillSearchIndexRow{},
		&SkillShareItemRow{},
	); err != nil {
		t.Fatalf("auto migrate skill v2 test tables: %v", err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_active_skill_maintenance_admission
		ON resource_update_tasks(user_id)
		WHERE resource_type = 'skill'
		  AND task_type IN ('generate_review', 'organize_skill')
		  AND status IN ('pending', 'running')`).Error; err != nil {
		t.Fatalf("create active skill maintenance admission index: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS skill_review_stats (
		id TEXT NOT NULL PRIMARY KEY,
		requestid TEXT NOT NULL,
		userid TEXT NOT NULL,
		status TEXT NOT NULL,
		started_at TEXT NOT NULL,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		summary TEXT NOT NULL DEFAULT '{}'
	)`).Error; err != nil {
		t.Fatalf("create skill review stats table: %v", err)
	}
	return &TestDB{DB: db}
}

func ResetSkillTables(t *testing.T, db *TestDB) {
	t.Helper()

	for _, table := range []string{
		"skill_draft_entries",
		"skill_draft_review_action_items",
		"skill_draft_review_action_batches",
		"skill_draft_review_sessions",
		"skill_drafts",
		"skill_revision_entries",
		"skill_revisions",
		"skill_market_installs",
		"skill_market_items",
		"skill_search_indexes",
		"skill_share_items",
		"skills",
		"skill_blobs",
	} {
		if err := db.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatalf("reset %s: %v", table, err)
		}
	}
}

func CountRows(t *testing.T, db *TestDB, table, where string, args ...any) int64 {
	t.Helper()

	var count int64
	query := db.Table(table)
	if where != "" {
		query = query.Where(where, args...)
	}
	if err := query.Count(&count).Error; err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	return count
}

func MustCreate(t *testing.T, db *TestDB, value any) {
	t.Helper()
	if err := db.Create(value).Error; err != nil {
		t.Fatalf("create fixture %#v: %v", value, err)
	}
}

func TimeFixture() time.Time {
	return time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
}

type SkillRow struct {
	ID                    string     `gorm:"column:id;type:varchar(36);primaryKey"`
	OwnerUserID           string     `gorm:"column:owner_user_id;type:text;not null;uniqueIndex:uk_skills_owner_identity,priority:1;uniqueIndex:uk_skills_owner_relative_root,priority:1"`
	OwnerUserName         string     `gorm:"column:owner_user_name;type:text;not null;default:''"`
	CreateUserID          string     `gorm:"column:create_user_id;type:text;not null"`
	CreateUserName        string     `gorm:"column:create_user_name;type:text;not null;default:''"`
	Category              string     `gorm:"column:category;type:text;not null;uniqueIndex:uk_skills_owner_identity,priority:2"`
	SkillName             string     `gorm:"column:skill_name;type:text;not null;uniqueIndex:uk_skills_owner_identity,priority:3"`
	OriginBuiltinSkillUID string     `gorm:"column:origin_builtin_skill_uid;type:text;not null;default:''"`
	Description           string     `gorm:"column:description;type:text"`
	Tags                  []byte     `gorm:"column:tags;type:json"`
	RelativeRoot          string     `gorm:"column:relative_root;type:text;not null;uniqueIndex:uk_skills_owner_relative_root,priority:2"`
	SkillMDPath           string     `gorm:"column:skill_md_path;type:text;not null;default:'SKILL.md'"`
	HeadRevisionID        *string    `gorm:"column:head_revision_id;type:varchar(36)"`
	Version               int64      `gorm:"column:version;not null;default:1"`
	AutoEvo               bool       `gorm:"column:auto_evo;not null;default:false"`
	AutoEvoApplyStatus    string     `gorm:"column:auto_evo_apply_status;type:text;not null;default:'idle'"`
	AutoEvoGeneration     int64      `gorm:"column:auto_evo_generation;not null;default:0"`
	AutoEvoStartedAt      *time.Time `gorm:"column:auto_evo_started_at"`
	AutoEvoFinishedAt     *time.Time `gorm:"column:auto_evo_finished_at"`
	AutoEvoError          string     `gorm:"column:auto_evo_error;type:text;not null;default:''"`
	IsEnabled             bool       `gorm:"column:is_enabled;not null;default:true"`
	UpdateStatus          string     `gorm:"column:update_status;type:text;not null;default:'up_to_date'"`
	Ext                   []byte     `gorm:"column:ext;type:json"`
	DeletedAt             *time.Time `gorm:"column:deleted_at"`
	DeletedBy             *string    `gorm:"column:deleted_by;type:text"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time  `gorm:"column:updated_at;not null"`
}

func (SkillRow) TableName() string { return "skills" }

type SkillSearchIndexRow struct {
	SkillID        string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	OwnerUserID    string    `gorm:"column:owner_user_id;type:text;not null;index:idx_skill_search_owner"`
	HeadRevisionID string    `gorm:"column:head_revision_id;type:varchar(36);not null"`
	Content        string    `gorm:"column:content;type:text;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (SkillSearchIndexRow) TableName() string { return "skill_search_indexes" }

type SkillBlobRow struct {
	Hash           string    `gorm:"column:hash;type:text;primaryKey"`
	Size           int64     `gorm:"column:size;not null"`
	Mime           string    `gorm:"column:mime;type:text"`
	FileType       string    `gorm:"column:file_type;type:text;not null;default:'unknown'"`
	Binary         bool      `gorm:"column:binary;not null;default:false;check:skill_blob_storage_shape,(binary = false AND storage_backend = 'postgres' AND content IS NOT NULL AND storage_key IS NULL) OR (binary = true AND storage_backend IN ('local_file','s3') AND content IS NULL AND storage_key IS NOT NULL)"`
	StorageBackend string    `gorm:"column:storage_backend;type:text;not null;check:skill_blob_storage_backend,storage_backend IN ('postgres','local_file','s3')"`
	StorageKey     *string   `gorm:"column:storage_key;type:text"`
	Content        []byte    `gorm:"column:content;type:blob"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (SkillBlobRow) TableName() string { return "skill_blobs" }

type SkillRevisionRow struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID          string    `gorm:"column:skill_id;type:varchar(36);not null;uniqueIndex:uk_skill_revisions_skill_no,priority:1"`
	ParentRevisionID *string   `gorm:"column:parent_revision_id;type:varchar(36)"`
	RevisionNo       int64     `gorm:"column:revision_no;not null;uniqueIndex:uk_skill_revisions_skill_no,priority:2"`
	TreeHash         string    `gorm:"column:tree_hash;type:text;not null"`
	Message          string    `gorm:"column:message;type:text"`
	ChangeSource     string    `gorm:"column:change_source;type:text;not null;default:'draft_commit'"`
	SourceRefType    string    `gorm:"column:source_ref_type;type:text;not null;default:''"`
	SourceRefID      string    `gorm:"column:source_ref_id;type:text;not null;default:''"`
	CreatedBy        *string   `gorm:"column:created_by;type:varchar(36)"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
}

func (SkillRevisionRow) TableName() string { return "skill_revisions" }

type SkillRevisionEntryRow struct {
	RevisionID string  `gorm:"column:revision_id;type:varchar(36);primaryKey"`
	Path       string  `gorm:"column:path;type:text;primaryKey"`
	EntryType  string  `gorm:"column:entry_type;type:text;not null;check:skill_revision_entry_type,entry_type IN ('file','dir');check:skill_revision_entry_blob_shape,(entry_type = 'file' AND blob_hash IS NOT NULL) OR (entry_type = 'dir' AND blob_hash IS NULL)"`
	BlobHash   *string `gorm:"column:blob_hash;type:text"`
	Size       int64   `gorm:"column:size"`
	Mime       string  `gorm:"column:mime;type:text"`
	FileType   string  `gorm:"column:file_type;type:text;not null;default:'unknown'"`
	Binary     bool    `gorm:"column:binary;not null;default:false"`
	Mode       int     `gorm:"column:mode;not null;default:420"`
}

func (SkillRevisionEntryRow) TableName() string { return "skill_revision_entries" }

type SkillDraftRow struct {
	SkillID        string     `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	BaseRevisionID *string    `gorm:"column:base_revision_id;type:varchar(36)"`
	DraftStatus    string     `gorm:"column:draft_status;type:text;not null;default:''"`
	DraftUpdatedAt *time.Time `gorm:"column:draft_updated_at"`
	TaskID         string     `gorm:"column:task_id;type:text;not null;default:''"`
	ConversationID *string    `gorm:"column:conversation_id;type:varchar(36)"`
	UpdatedBy      *string    `gorm:"column:updated_by;type:varchar(36)"`
	Version        int64      `gorm:"column:version;not null;default:1"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
}

func (SkillDraftRow) TableName() string { return "skill_drafts" }

type SkillDraftEntryRow struct {
	SkillID   string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	Path      string    `gorm:"column:path;type:text;primaryKey"`
	Op        string    `gorm:"column:op;type:text;not null;check:skill_draft_entry_op,op IN ('upsert','delete');check:skill_draft_entry_shape,(op = 'delete') OR (op = 'upsert' AND entry_type IN ('file','dir'))"`
	EntryType string    `gorm:"column:entry_type;type:text"`
	BlobHash  *string   `gorm:"column:blob_hash;type:text"`
	Size      int64     `gorm:"column:size"`
	Mime      string    `gorm:"column:mime;type:text"`
	FileType  string    `gorm:"column:file_type;type:text"`
	Binary    bool      `gorm:"column:binary"`
	Mode      int       `gorm:"column:mode"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (SkillDraftEntryRow) TableName() string { return "skill_draft_entries" }

type SkillDraftReviewSessionRow struct {
	ID                  string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID             string    `gorm:"column:skill_id;type:varchar(36);not null;index:idx_skill_draft_review_sessions_skill_status,priority:1"`
	BaseRevisionID      string    `gorm:"column:base_revision_id;type:varchar(36);not null"`
	DraftVersionAtStart int64     `gorm:"column:draft_version_at_start;not null"`
	DraftSnapshotHash   string    `gorm:"column:draft_snapshot_hash;type:text;not null"`
	Status              string    `gorm:"column:status;type:text;not null;default:'active';index:idx_skill_draft_review_sessions_skill_status,priority:2"`
	Version             int64     `gorm:"column:version;not null;default:1"`
	UndoLimit           int       `gorm:"column:undo_limit;not null;default:20"`
	CreatedBy           *string   `gorm:"column:created_by;type:text"`
	UpdatedBy           *string   `gorm:"column:updated_by;type:text"`
	CreatedAt           time.Time `gorm:"column:created_at;not null"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null;index:idx_skill_draft_review_sessions_skill_status,priority:3"`
}

func (SkillDraftReviewSessionRow) TableName() string {
	return "skill_draft_review_sessions"
}

type SkillDraftReviewActionBatchRow struct {
	ID              string     `gorm:"column:id;type:varchar(36);primaryKey"`
	ReviewSessionID string     `gorm:"column:review_session_id;type:varchar(36);not null;uniqueIndex:uk_skill_draft_review_batch_sequence,priority:1;index:idx_skill_draft_review_batches_session_created,priority:1"`
	Sequence        int64      `gorm:"column:sequence;not null;uniqueIndex:uk_skill_draft_review_batch_sequence,priority:2"`
	UndoLocked      bool       `gorm:"column:undo_locked;not null;default:false"`
	UndoneAt        *time.Time `gorm:"column:undone_at"`
	UndoneBy        *string    `gorm:"column:undone_by;type:text"`
	CreatedBy       *string    `gorm:"column:created_by;type:text"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;index:idx_skill_draft_review_batches_session_created,priority:2"`
}

func (SkillDraftReviewActionBatchRow) TableName() string {
	return "skill_draft_review_action_batches"
}

type SkillDraftReviewActionItemRow struct {
	ID              string    `gorm:"column:id;type:varchar(36);primaryKey"`
	BatchID         string    `gorm:"column:batch_id;type:varchar(36);not null;index:idx_skill_draft_review_items_batch"`
	ReviewSessionID string    `gorm:"column:review_session_id;type:varchar(36);not null;index:idx_skill_draft_review_items_session_hunk,priority:1"`
	Path            string    `gorm:"column:path;type:text;not null;index:idx_skill_draft_review_items_session_hunk,priority:2"`
	HunkID          string    `gorm:"column:hunk_id;type:text;not null;index:idx_skill_draft_review_items_session_hunk,priority:3"`
	BeforeDecision  string    `gorm:"column:before_decision;type:text;not null;default:'pending'"`
	AfterDecision   string    `gorm:"column:after_decision;type:text;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null"`
}

func (SkillDraftReviewActionItemRow) TableName() string {
	return "skill_draft_review_action_items"
}

type SkillMarketItemRow struct {
	ID            string     `gorm:"column:id;type:varchar(36);primaryKey"`
	SourceSkillID string     `gorm:"column:source_skill_id;type:varchar(36);not null"`
	Status        string     `gorm:"column:status;type:text;not null;default:'draft'"`
	Tags          []byte     `gorm:"column:tags;type:json;not null;default:'[]'"`
	Icon          string     `gorm:"column:icon;type:text;not null;default:''"`
	SortOrder     int        `gorm:"column:sort_order;not null;default:0"`
	VersionNote   string     `gorm:"column:version_note;type:text;not null;default:''"`
	CreatedBy     *string    `gorm:"column:created_by;type:varchar(36)"`
	UpdatedBy     *string    `gorm:"column:updated_by;type:varchar(36)"`
	PublishedAt   *time.Time `gorm:"column:published_at"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null"`
}

func (SkillMarketItemRow) TableName() string { return "skill_market_items" }

type SkillMarketInstallRow struct {
	MarketItemID string    `gorm:"column:market_item_id;type:varchar(36);primaryKey"`
	UserID       string    `gorm:"column:user_id;type:text;primaryKey"`
	SkillID      string    `gorm:"column:skill_id;type:varchar(36);not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

func (SkillMarketInstallRow) TableName() string { return "skill_market_installs" }

type SkillShareItemRow struct {
	ID            string    `gorm:"column:id;type:varchar(36);primaryKey"`
	ShareTaskID   string    `gorm:"column:share_task_id;type:varchar(36);not null;default:''"`
	SourceSkillID string    `gorm:"column:source_skill_id;type:varchar(36);not null"`
	TargetUserID  string    `gorm:"column:target_user_id;type:text;not null"`
	Status        string    `gorm:"column:status;type:text;not null"`
	TargetSkillID string    `gorm:"column:target_root_skill_id;type:varchar(36);not null;default:''"`
	CreatedAt     time.Time `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null"`
}

func (SkillShareItemRow) TableName() string { return "skill_share_items" }
