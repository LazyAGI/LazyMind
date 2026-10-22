package orm

import (
	"encoding/json"
	"time"
)

// Recognition is document-scoped; manual revisions fence stale recognizers.
type AcademicDocumentState struct {
	MetadataJSON json.RawMessage `gorm:"serializer:json;type:json;not null;default:'{}'" json:"metadata"`
	DocumentID   string          `gorm:"primaryKey;type:varchar(128)" json:"document_id"`
	DatasetID    string          `gorm:"index;type:varchar(255);not null" json:"dataset_id"`
	Kind         string          `gorm:"type:varchar(16);not null" json:"kind"`
	Status       string          `gorm:"type:varchar(16);not null" json:"status"`
	Manual       bool            `gorm:"not null" json:"manual"`
	Revision     int64           `gorm:"not null" json:"revision"`
	PaperID      string          `gorm:"type:varchar(36);not null;default:''" json:"paper_id"`
	Fingerprint  string          `gorm:"type:varchar(128);not null;default:''" json:"fingerprint"`
	Error        string          `gorm:"type:text;not null;default:''" json:"error,omitempty"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func (AcademicDocumentState) TableName() string { return "academic_document_states" }

type AcademicUserPaperState struct {
	UserID      string `gorm:"primaryKey;type:varchar(255)"`
	PaperID     string `gorm:"primaryKey;type:varchar(36)"`
	IsImportant bool   `gorm:"not null;default:false"`
	UpdatedAt   time.Time
}

func (AcademicUserPaperState) TableName() string { return "academic_user_paper_states" }

type AcademicExpansion struct {
	ProgressJSON   json.RawMessage `gorm:"serializer:json;type:json;not null;default:'{}'" json:"progress"`
	ID             string          `gorm:"primaryKey;type:varchar(36)" json:"id"`
	UserID         string          `gorm:"index;type:varchar(255);not null" json:"-"`
	DatasetID      string          `gorm:"index;type:varchar(255);not null" json:"dataset_id"`
	ConversationID string          `gorm:"uniqueIndex;type:varchar(36);not null" json:"conversation_id"`
	RequestKey     string          `gorm:"uniqueIndex:uk_academic_expansion_request,priority:2;type:varchar(128);not null" json:"-"`
	RequestOwner   string          `gorm:"uniqueIndex:uk_academic_expansion_request,priority:1;type:varchar(255);not null" json:"-"`
	Title          string          `gorm:"type:text;not null" json:"title"`
	Goal           string          `gorm:"type:varchar(16);not null" json:"goal"`
	Status         string          `gorm:"type:varchar(32);not null" json:"status"`
	Version        int64           `gorm:"not null;default:0" json:"version"`
	ResultJSON     json.RawMessage `gorm:"type:json;not null" json:"result"`
	AutoReviewID   string          `gorm:"type:varchar(36);not null;default:''" json:"auto_review_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	DeletedAt      *time.Time      `gorm:"index" json:"-"`
}

func (AcademicExpansion) TableName() string { return "academic_expansions" }

type AcademicExpansionRound struct {
	ID          string          `gorm:"primaryKey;type:varchar(36)" json:"id"`
	ExpansionID string          `gorm:"uniqueIndex:uk_academic_round,priority:1;type:varchar(36);not null" json:"expansion_id"`
	RequestKey  string          `gorm:"uniqueIndex:uk_academic_round,priority:2;type:varchar(128);not null" json:"request_key"`
	Status      string          `gorm:"type:varchar(32);not null" json:"status"`
	Version     int64           `gorm:"not null;default:0" json:"version"`
	InputJSON   json.RawMessage `gorm:"type:json;not null" json:"input"`
	ResultJSON  json.RawMessage `gorm:"type:json;not null" json:"result"`
	SessionID   string          `gorm:"type:varchar(36);not null;default:''" json:"session_id"`
	AttemptID   string          `gorm:"type:varchar(36);not null;default:''" json:"attempt_id"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (AcademicExpansionRound) TableName() string { return "academic_expansion_rounds" }

type AcademicHandoff struct {
	DatasetID       string          `gorm:"type:varchar(255);not null;default:''" json:"dataset_id"`
	ReflectionFirst bool            `gorm:"not null;default:false" json:"reflection_first"`
	MockResultJSON  json.RawMessage `gorm:"serializer:json;type:json;not null;default:'{}'" json:"mock_result"`
	ID              string          `gorm:"primaryKey;type:varchar(36)" json:"id"`
	UserID          string          `gorm:"uniqueIndex:uk_academic_handoff,priority:1;type:varchar(255);not null" json:"-"`
	RequestKey      string          `gorm:"uniqueIndex:uk_academic_handoff,priority:2;type:varchar(128);not null" json:"-"`
	ExpansionID     string          `gorm:"index;type:varchar(36);not null;default:''" json:"expansion_id"`
	Version         int64           `gorm:"not null;default:0" json:"version"`
	Kind            string          `gorm:"type:varchar(32);not null" json:"kind"`
	ConversationID  string          `gorm:"type:varchar(36);not null" json:"conversation_id"`
	InputJSON       json.RawMessage `gorm:"type:json;not null" json:"input"`
	ContentsJSON    json.RawMessage `gorm:"type:json;not null" json:"contents"`
	CreatedAt       time.Time       `json:"created_at"`
}

func (AcademicHandoff) TableName() string { return "academic_handoffs" }

const AcademicPaperParserVersion = "pdf-reader-v1"

type AcademicPaperContent struct {
	ParserVersion    string          `gorm:"uniqueIndex:uk_academic_content,priority:3;type:varchar(64);not null;default:'pdf-reader-v1'" json:"parser_version"`
	SourceHash       string          `gorm:"type:varchar(64);not null;default:''" json:"source_hash"`
	SourceDatasetID  string          `gorm:"type:varchar(255);not null;default:''" json:"-"`
	SourceDocumentID string          `gorm:"type:varchar(128);not null;default:''" json:"-"`
	ID               string          `gorm:"primaryKey;type:varchar(36)" json:"id"`
	UserID           string          `gorm:"uniqueIndex:uk_academic_content,priority:1;type:varchar(255);not null" json:"-"`
	PaperID          string          `gorm:"uniqueIndex:uk_academic_content,priority:2;type:varchar(36);not null" json:"paper_id"`
	Status           string          `gorm:"type:varchar(16);not null" json:"status"`
	ContentHash      string          `gorm:"type:varchar(64);not null;default:''" json:"content_hash"`
	PagesJSON        json.RawMessage `gorm:"type:json;not null" json:"-"`
	Error            string          `gorm:"type:text;not null;default:''" json:"error,omitempty"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

func (AcademicPaperContent) TableName() string { return "academic_paper_contents" }
