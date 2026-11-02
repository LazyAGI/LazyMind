package orm

import (
	"encoding/json"
	"time"
)

// Library entries own filing and user-confirmed corrections, not global identity.
type PaperLibraryEntry struct {
	DatasetID      string          `gorm:"type:varchar(255);primaryKey;uniqueIndex:uk_library_work,priority:1"`
	ID             string          `gorm:"type:varchar(255);primaryKey"`
	AcademicWorkID string          `gorm:"type:varchar(36);not null;uniqueIndex:uk_library_work,priority:2"`
	FolderID       string          `gorm:"type:varchar(255);not null;default:'';index"`
	Status         string          `gorm:"type:varchar(16);not null;default:'unread'"`
	TagsJSON       json.RawMessage `gorm:"type:json;not null"`
	MetadataJSON   json.RawMessage `gorm:"type:json;not null"`
	Source         string          `gorm:"type:varchar(32);not null;default:'unknown'"`
	ImportMethod   string          `gorm:"type:varchar(32);not null;default:''"`
	SourceURL      string          `gorm:"type:text;not null;default:''"`
	ArxivVersion   string          `gorm:"type:varchar(64);not null;default:''"`
	Revision       int64           `gorm:"not null;default:1"`
	RemovedAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (PaperLibraryEntry) TableName() string { return "paper_library_entries" }

type PaperLibraryFolder struct {
	DatasetID string `gorm:"type:varchar(255);primaryKey;uniqueIndex:uk_library_folder_name,priority:1"`
	ID        string `gorm:"type:varchar(255);primaryKey"`
	ParentID  string `gorm:"type:varchar(255);not null;default:'';index;uniqueIndex:uk_library_folder_name,priority:2"`
	Name      string `gorm:"type:varchar(255);not null"`
	NameKey   string `gorm:"type:varchar(255);not null;uniqueIndex:uk_library_folder_name,priority:3"`
	Revision  int64  `gorm:"not null;default:1"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (PaperLibraryFolder) TableName() string { return "paper_library_folders" }

type PaperLibraryNote struct {
	DatasetID string `gorm:"type:varchar(255);primaryKey"`
	ID        string `gorm:"type:varchar(255);primaryKey"`
	PaperID   string `gorm:"type:varchar(255);not null;index"`
	Title     string `gorm:"type:text;not null"`
	Content   string `gorm:"type:text;not null"`
	Kind      string `gorm:"type:varchar(32);not null;default:'note'"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (PaperLibraryNote) TableName() string { return "paper_library_notes" }

// Detaching a member never removes the document or its AcademicWorkDocument fact.
type PaperLibraryAttachment struct {
	DatasetID    string `gorm:"type:varchar(255);primaryKey"`
	ID           string `gorm:"type:varchar(255);primaryKey"`
	PaperID      string `gorm:"type:varchar(255);not null;index"`
	DocumentID   string `gorm:"type:varchar(128);not null;default:'';index"`
	ImportItemID string `gorm:"type:varchar(36);not null;default:''"`
	Name         string `gorm:"type:text;not null"`
	Kind         string `gorm:"type:varchar(32);not null;default:'pdf'"`
	SourceURL    string `gorm:"type:text;not null;default:''"`
	IsPrimary    bool   `gorm:"not null;default:false"`
	DetachedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (PaperLibraryAttachment) TableName() string { return "paper_library_attachments" }

// PaperLibrary is the immutable pre-normalization graph archive. New writes use
// the normalized entities and PaperLibraryLegacyState auxiliary fields.
type PaperLibrary struct {
	DatasetID string          `gorm:"type:varchar(255);primaryKey;not null"`
	DataJSON  json.RawMessage `gorm:"type:json;not null"`
	Revision  int64           `gorm:"not null;default:0;check:revision >= 0"`
	CreatedAt time.Time       `gorm:"not null"`
	UpdatedAt time.Time       `gorm:"not null"`
}

func (PaperLibrary) TableName() string { return "paper_libraries" }

// ExtrasJSON keeps fields which do not belong to normalized entities. MigratedAt
// is committed in the same transaction as the conversion, preventing replay.
type PaperLibraryLegacyState struct {
	DatasetID  string          `gorm:"type:varchar(255);primaryKey;not null"`
	ExtrasJSON json.RawMessage `gorm:"type:json;not null"`
	MigratedAt *time.Time
	UpdatedAt  time.Time `gorm:"not null"`
}

func (PaperLibraryLegacyState) TableName() string { return "paper_library_legacy_states" }
