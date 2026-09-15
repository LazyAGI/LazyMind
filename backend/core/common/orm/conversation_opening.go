package orm

import (
	"encoding/json"
	"time"
)

// ConversationOpening is derived from a bounded opening, independently of chat activity.
type ConversationOpening struct {
	ConversationID   string          `gorm:"primaryKey;type:varchar(36)" json:"-"`
	UserID           string          `gorm:"type:varchar(255);not null;index" json:"-"`
	Summary          string          `gorm:"type:text;not null;default:''" json:"-"`
	IntentStatus     string          `gorm:"type:varchar(16);not null;default:''" json:"-"`
	MissingContext   json.RawMessage `gorm:"type:json" json:"-"`
	InputJSON        json.RawMessage `gorm:"type:json;not null" json:"-"`
	SourceHistoryIDs json.RawMessage `gorm:"type:json;not null" json:"-"`
	SourceHash       string          `gorm:"type:varchar(64);not null" json:"-"`
	EvidenceHash     string          `gorm:"type:varchar(64);not null" json:"-"`
	OpeningTurns     int             `gorm:"not null" json:"-"`
	SeedRevision     int64           `gorm:"not null;default:1" json:"-"`
	MetadataRevision int64           `gorm:"not null;default:0" json:"-"`
	TitleRevision    int64           `gorm:"not null;default:0" json:"-"`
	GeneratorVersion string          `gorm:"type:varchar(32);not null" json:"-"`
	GenerationCount  int             `gorm:"not null;default:0" json:"-"`
	CallCount        int             `gorm:"not null;default:0" json:"-"`
	WindowClosed     bool            `gorm:"not null;default:false" json:"-"`
	Status           string          `gorm:"type:varchar(16);not null;index" json:"-"`
	ErrorCode        string          `gorm:"type:varchar(64);not null;default:''" json:"-"`
	ModelID          json.RawMessage `gorm:"type:json" json:"-"`
	UsageJSON        json.RawMessage `gorm:"type:json" json:"-"`
	JobID            string          `gorm:"type:varchar(64);not null;default:''" json:"-"`
	BackfillID       string          `gorm:"type:varchar(64);not null;default:'';index" json:"-"`
	UpdatedAt        time.Time       `json:"-"`
}

func (ConversationOpening) TableName() string { return "conversation_opening_metadata" }

type ConversationOpeningBackfill struct {
	ID           string     `gorm:"primaryKey;type:varchar(64)" json:"id"`
	UserID       string     `gorm:"type:varchar(255);not null;uniqueIndex" json:"-"`
	Version      string     `gorm:"type:varchar(32);not null" json:"-"`
	Status       string     `gorm:"type:varchar(16);not null" json:"status"`
	CursorTime   *time.Time `json:"-"`
	CursorID     string     `gorm:"type:varchar(36);not null;default:''" json:"-"`
	Scanned      int64      `gorm:"not null;default:0" json:"scanned"`
	Skipped      int64      `gorm:"not null;default:0" json:"skipped"`
	ScanComplete bool       `gorm:"not null;default:false" json:"scan_complete"`
	CreatedAt    time.Time  `json:"-"`
	UpdatedAt    time.Time  `json:"-"`
}

func (ConversationOpeningBackfill) TableName() string { return "conversation_opening_backfills" }
