package service

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type UploadSession struct {
	UploadID    string
	OwnerUserID string
	State       string
	StoredPath  string
	Filename    string
}

type UploadStore interface {
	Get(ctx context.Context, uploadID string) (UploadSession, error)
}

type ZipDownloader interface {
	Download(ctx context.Context, url string) (string, error)
}

type SkillServiceDeps struct {
	DB          *gorm.DB
	UploadStore UploadStore
	Downloader  ZipDownloader
	BlobStore   *BlobStore
	Clock       Clock
}

type SkillService struct {
	db          *gorm.DB
	uploadStore UploadStore
	downloader  ZipDownloader
	blobStore   *BlobStore
	clock       Clock
}

type SourceInput struct {
	Type       string
	UploadID   string
	Filename   string
	StoredPath string
	URL        string
}

type CreateSkillRequest struct {
	OwnerUserID           string
	OwnerUserName         string
	CreateUserID          string
	CreateUserName        string
	Name                  string
	Category              string
	OriginBuiltinSkillUID string
	Description           string
	Tags                  []string
	AutoEvo               bool
	IsEnabled             *bool
	Source                SourceInput
}

type CreateSkillResponse struct {
	SkillID        string
	HeadRevisionID string
}

type PatchSkillRequest struct {
	SkillID     string
	UserID      string
	Name        *string
	Category    *string
	Description *string
	Tags        *[]string
	AutoEvo     *bool
	IsEnabled   *bool
	Source      *SourceInput
}

type PatchSkillResponse struct {
	SkillID        string
	HeadRevisionID string
}

type DeleteSkillRequest struct {
	SkillID string
	UserID  string
}

type RestoreSkillRequest struct {
	SkillID string
	UserID  string
}

type PurgeSkillRequest struct {
	SkillID string
	UserID  string
}

type EmptyTrashRequest struct {
	UserID string
}

type DiscardDraftRequest struct {
	SkillID string
	UserID  string
}

type DiscardDraftResponse struct {
	DraftVersion int64
}

type ListSkillsRequest struct {
	UserID string
}

type ListSkillsResponse struct {
	Items []SkillSummary
}

type GetSkillRequest struct {
	SkillID string
	UserID  string
}

type SkillSummary struct {
	ID             string
	SkillID        string
	Name           string
	SkillName      string
	Category       string
	Description    string
	Tags           []string
	HeadRevisionID string
	FileContent    string
	AutoEvo        bool
	IsEnabled      bool
	Draft          DraftSummary
	DeletedAt      *time.Time
	DeletedBy      string
}

type SkillDetail struct {
	SkillSummary
	Draft DraftSummary
}

type DraftSummary struct {
	HasUncommittedDraft bool
	TaskID              string
	Version             int64
}

type TreeRef struct {
	SkillID string
	RefType string
}

type TreeNode struct {
	Name     string
	Path     string
	Type     string
	Children []TreeNode
	BlobHash string
	Size     int64
	Mime     string
	FileType string
	Binary   bool
}

type FileRef struct {
	SkillID string
	RefType string
	Path    string
}

type FileContent struct {
	Path        string
	Content     string
	Binary      bool
	DownloadURL string
	StorageKey  string
	Mime        string
	FileType    string
	BlobHash    string
}

type AutoEvoDraftRequest struct {
	SkillID        string
	ConversationID string
	Files          map[string][]byte
}

type AcceptReviewRequest struct {
	SkillID     string
	UserID      string
	ReviewID    string
	Name        string
	Category    string
	Description string
	Files       map[string][]byte
}

type AcceptReviewResponse struct {
	SkillID        string
	HeadRevisionID string
}

type skillRow struct {
	ID                    string     `gorm:"column:id;type:varchar(36);primaryKey"`
	OwnerUserID           string     `gorm:"column:owner_user_id;type:text;not null"`
	OwnerUserName         string     `gorm:"column:owner_user_name;type:text;not null;default:''"`
	CreateUserID          string     `gorm:"column:create_user_id;type:text;not null"`
	CreateUserName        string     `gorm:"column:create_user_name;type:text;not null;default:''"`
	Category              string     `gorm:"column:category;type:text;not null"`
	SkillName             string     `gorm:"column:skill_name;type:text;not null"`
	OriginBuiltinSkillUID string     `gorm:"column:origin_builtin_skill_uid;type:text;not null;default:''"`
	Description           string     `gorm:"column:description;type:text"`
	Tags                  []byte     `gorm:"column:tags;type:json"`
	RelativeRoot          string     `gorm:"column:relative_root;type:text;not null"`
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

func (skillRow) TableName() string { return "skills" }

type skillBlobRow struct {
	Hash           string    `gorm:"column:hash;type:text;primaryKey"`
	Size           int64     `gorm:"column:size;not null"`
	Mime           string    `gorm:"column:mime;type:text"`
	FileType       string    `gorm:"column:file_type;type:text;not null;default:'unknown'"`
	Binary         bool      `gorm:"column:binary;not null;default:false"`
	StorageBackend string    `gorm:"column:storage_backend;type:text;not null"`
	StorageKey     *string   `gorm:"column:storage_key;type:text"`
	Content        []byte    `gorm:"column:content;type:blob"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (skillBlobRow) TableName() string { return "skill_blobs" }

type skillRevisionRow struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID          string    `gorm:"column:skill_id;type:varchar(36);not null"`
	ParentRevisionID *string   `gorm:"column:parent_revision_id;type:varchar(36)"`
	RevisionNo       int64     `gorm:"column:revision_no;not null"`
	TreeHash         string    `gorm:"column:tree_hash;type:text;not null"`
	Message          string    `gorm:"column:message;type:text"`
	ChangeSource     string    `gorm:"column:change_source;type:text;not null;default:'draft_commit'"`
	SourceRefType    string    `gorm:"column:source_ref_type;type:text;not null;default:''"`
	SourceRefID      string    `gorm:"column:source_ref_id;type:text;not null;default:''"`
	CreatedBy        *string   `gorm:"column:created_by;type:varchar(36)"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
}

func (skillRevisionRow) TableName() string { return "skill_revisions" }

type skillRevisionEntryRow struct {
	RevisionID string  `gorm:"column:revision_id;type:varchar(36);primaryKey"`
	Path       string  `gorm:"column:path;type:text;primaryKey"`
	EntryType  string  `gorm:"column:entry_type;type:text;not null"`
	BlobHash   *string `gorm:"column:blob_hash;type:text"`
	Size       int64   `gorm:"column:size"`
	Mime       string  `gorm:"column:mime;type:text"`
	FileType   string  `gorm:"column:file_type;type:text;not null;default:'unknown'"`
	Binary     bool    `gorm:"column:binary;not null;default:false"`
	Mode       int     `gorm:"column:mode;not null;default:420"`
}

func (skillRevisionEntryRow) TableName() string { return "skill_revision_entries" }

type skillDraftRow struct {
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

func (skillDraftRow) TableName() string { return "skill_drafts" }

type skillDraftEntryRow struct {
	SkillID   string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	Path      string    `gorm:"column:path;type:text;primaryKey"`
	Op        string    `gorm:"column:op;type:text;not null"`
	EntryType string    `gorm:"column:entry_type;type:text"`
	BlobHash  *string   `gorm:"column:blob_hash;type:text"`
	Size      int64     `gorm:"column:size"`
	Mime      string    `gorm:"column:mime;type:text"`
	FileType  string    `gorm:"column:file_type;type:text"`
	Binary    bool      `gorm:"column:binary"`
	Mode      int       `gorm:"column:mode"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (skillDraftEntryRow) TableName() string { return "skill_draft_entries" }

type skillMarketInstallRow struct {
	MarketItemID string    `gorm:"column:market_item_id;type:varchar(36);primaryKey"`
	UserID       string    `gorm:"column:user_id;type:text;primaryKey"`
	SkillID      string    `gorm:"column:skill_id;type:varchar(36);not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

func (skillMarketInstallRow) TableName() string { return "skill_market_installs" }

type skillSearchIndexRow struct {
	SkillID string `gorm:"column:skill_id;type:varchar(36);primaryKey"`
}

func (skillSearchIndexRow) TableName() string { return "skill_search_indexes" }
