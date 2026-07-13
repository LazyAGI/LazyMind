package share

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	skillsearch "lazymind/core/skillv2/search"
)

type ServiceDeps struct {
	DB        *gorm.DB
	BlobStore *BlobStore
}

type Service struct {
	db        *gorm.DB
	blobStore *BlobStore
}

func NewService(deps ServiceDeps) *Service {
	return &Service{db: deps.DB, blobStore: deps.BlobStore}
}

type AcceptRequest struct {
	ShareItemID string
	UserID      string
	UserName    string
}

type AcceptResponse struct {
	TargetSkillID string
}

func (s *Service) Accept(ctx context.Context, req AcceptRequest) (AcceptResponse, error) {
	var out AcceptResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item skillShareItemRow
		if err := tx.Where("id = ?", req.ShareItemID).Take(&item).Error; err != nil {
			return err
		}
		if item.TargetUserID != req.UserID {
			return fmt.Errorf("share item is not for target user")
		}
		if item.Status != "pending" && item.Status != "pending_accept" {
			return fmt.Errorf("share item is not pending")
		}
		skillID, _, err := copyHeadRevision(tx, item.SourceSkillID, req.UserID, req.UserName, "share_accept", req.UserID)
		if err != nil {
			return err
		}
		now := time.Now()
		if err := skillsearch.RebuildSkillTx(ctx, tx, skillID, now); err != nil {
			return err
		}
		updates := map[string]any{
			"target_root_skill_id": skillID,
			"status":               "completed",
			"updated_at":           now,
		}
		if tx.Migrator().HasColumn(&skillShareItemRow{}, "accepted_at") {
			updates["accepted_at"] = now
		}
		if err := tx.Model(&skillShareItemRow{}).Where("id = ?", req.ShareItemID).Updates(updates).Error; err != nil {
			return err
		}
		out.TargetSkillID = skillID
		return nil
	})
	return out, err
}

type LocalObjectStore struct {
	root string
}

func NewLocalObjectStore(root string) *LocalObjectStore {
	return &LocalObjectStore{root: root}
}

func (s *LocalObjectStore) Put(ctx context.Context, key string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	p := filepath.Join(s.root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

type BlobStore struct {
	db      *gorm.DB
	objects *LocalObjectStore
}

func NewBlobStore(db *gorm.DB, objects *LocalObjectStore) *BlobStore {
	return &BlobStore{db: db, objects: objects}
}

func copyHeadRevision(tx *gorm.DB, sourceSkillID, ownerUserID, ownerUserName, changeSource, createdBy string) (string, string, error) {
	var source skillRow
	if err := tx.Where("id = ?", sourceSkillID).Take(&source).Error; err != nil {
		return "", "", err
	}
	if source.HeadRevisionID == nil {
		return "", "", fmt.Errorf("source skill has no head revision")
	}
	var sourceRev skillRevisionRow
	if err := tx.Where("id = ? AND skill_id = ?", *source.HeadRevisionID, source.ID).Take(&sourceRev).Error; err != nil {
		return "", "", err
	}
	var sourceEntries []skillRevisionEntryRow
	if err := tx.Where("revision_id = ?", sourceRev.ID).Order("path ASC").Find(&sourceEntries).Error; err != nil {
		return "", "", err
	}
	now := time.Now()
	skillID := newID()
	revisionID := newID()
	var createdByPtr *string
	if createdBy != "" {
		createdByPtr = &createdBy
	}
	copy := source
	copy.ID = skillID
	copy.OwnerUserID = ownerUserID
	copy.OwnerUserName = ownerUserName
	copy.CreateUserID = createdBy
	copy.CreateUserName = ownerUserName
	copy.HeadRevisionID = &revisionID
	copy.RelativeRoot = path.Join(source.Category, source.SkillName)
	copy.Version = 1
	copy.CreatedAt = now
	copy.UpdatedAt = now
	if err := tx.Create(&copy).Error; err != nil {
		return "", "", err
	}
	entries := make([]skillRevisionEntryRow, 0, len(sourceEntries))
	for _, entry := range sourceEntries {
		entry.RevisionID = revisionID
		entries = append(entries, entry)
	}
	if err := tx.Create(&skillRevisionRow{
		ID:            revisionID,
		SkillID:       skillID,
		RevisionNo:    1,
		TreeHash:      hashEntries(entries),
		ChangeSource:  changeSource,
		SourceRefType: "skill",
		SourceRefID:   sourceSkillID,
		CreatedBy:     createdByPtr,
		CreatedAt:     now,
	}).Error; err != nil {
		return "", "", err
	}
	if len(entries) > 0 {
		if err := tx.Create(&entries).Error; err != nil {
			return "", "", err
		}
	}
	if err := tx.Create(&skillDraftRow{SkillID: skillID, BaseRevisionID: &revisionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		return "", "", err
	}
	return skillID, revisionID, nil
}

func hashEntries(entries []skillRevisionEntryRow) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		hash := ""
		if entry.BlobHash != nil {
			hash = *entry.BlobHash
		}
		lines = append(lines, entry.Path+"\x00"+entry.EntryType+"\x00"+hash)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func newID() string {
	if id, err := uuid.NewRandom(); err == nil {
		return id.String()
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
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
	CreatedAt             time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time  `gorm:"column:updated_at;not null"`
}

func (skillRow) TableName() string { return "skills" }

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

type skillShareItemRow struct {
	ID            string     `gorm:"column:id;type:varchar(36);primaryKey"`
	ShareTaskID   string     `gorm:"column:share_task_id;type:varchar(36);not null;default:''"`
	SourceSkillID string     `gorm:"column:source_skill_id;type:varchar(36);not null"`
	TargetUserID  string     `gorm:"column:target_user_id;type:text;not null"`
	Status        string     `gorm:"column:status;type:text;not null"`
	TargetSkillID string     `gorm:"column:target_root_skill_id;type:varchar(36);not null;default:''"`
	AcceptedAt    *time.Time `gorm:"column:accepted_at"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null"`
}

func (skillShareItemRow) TableName() string { return "skill_share_items" }
