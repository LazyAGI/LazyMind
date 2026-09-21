package orm

import (
	"encoding/json"
	"time"
)

// Publication operations retain external outcomes separately from local commits.
type DocumentPublicationOperation struct {
	MediaAssets        json.RawMessage `gorm:"type:json"`
	ID                 string          `gorm:"primaryKey;size:64"`
	OwnerUserID        string          `gorm:"not null;size:255;uniqueIndex:idx_document_publication_key,priority:1"`
	IdempotencyKey     string          `gorm:"not null;size:128;uniqueIndex:idx_document_publication_key,priority:2"`
	SessionID          string          `gorm:"not null;size:64;index"`
	SlotID             string          `gorm:"not null;size:255"`
	ItemIndex          int             `gorm:"not null"`
	Status             string          `gorm:"not null;size:32"`
	SourceRevisionID   string          `gorm:"not null;size:64"`
	SourceRevision     int
	SourceDraftVersion int64
	TargetDocument     json.RawMessage `gorm:"type:json"`
	RemoteValue        json.RawMessage `gorm:"type:json"`
	CandidateValue     json.RawMessage `gorm:"type:json"`
	SourceSchema       string
	SourceContentType  string
	AllowBound         bool
	SharedTarget       bool
	Template           string
	SourceHash         string          `gorm:"size:64"`
	SourceValue        json.RawMessage `gorm:"type:json"`
	RequestHash        string          `gorm:"size:64"`
	Provider           string          `gorm:"size:64"`
	Title              string
	ParentURI          string
	ReceiptJSON        json.RawMessage `gorm:"type:json"`
	ResultRevisionID   string          `gorm:"size:64"`
	ErrorCode          string          `gorm:"size:64"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (DocumentPublicationOperation) TableName() string { return "document_publication_operations" }

type DocumentPublicationBinding struct {
	ID                 string          `gorm:"primaryKey;size:64"`
	SessionID          string          `gorm:"not null;size:64;uniqueIndex:idx_document_publication_item,priority:1"`
	SlotID             string          `gorm:"not null;size:255;uniqueIndex:idx_document_publication_item,priority:2"`
	ItemIndex          int             `gorm:"not null;uniqueIndex:idx_document_publication_item,priority:3"`
	OwnerUserID        string          `gorm:"not null;size:255"`
	PendingOperationID string          `gorm:"size:64"`
	Provider           string          `gorm:"size:64"`
	TargetDocument     json.RawMessage `gorm:"type:json"`
	RemoteValue        json.RawMessage `gorm:"type:json"`
	SourceRevisionID   string          `gorm:"size:64"`
	ResultRevisionID   string          `gorm:"size:64"`
}

func (DocumentPublicationBinding) TableName() string { return "document_publication_bindings" }
