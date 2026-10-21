package orm

import (
	"encoding/json"
	"time"
)

// Document directories own references, never source documents or credentials.
type DocumentDirectory struct {
	ID           string          `gorm:"type:varchar(36);primaryKey" json:"id"`
	UserID       string          `gorm:"type:varchar(255);not null;index" json:"-"`
	Name         string          `gorm:"type:varchar(255);not null" json:"name"`
	Intent       string          `gorm:"type:text;not null" json:"intent"`
	Organization string          `gorm:"type:text;not null" json:"organization"`
	AutoAdd      bool            `gorm:"not null;default:true" json:"auto_add"`
	Pinned       bool            `gorm:"not null;default:false" json:"pinned"`
	Ready        bool            `gorm:"not null;default:false" json:"ready"`
	Version      int64           `gorm:"not null;default:1" json:"version"`
	LastUsedAt   time.Time       `gorm:"not null" json:"last_used_at"`
	UseUntil     *time.Time      `json:"use_until,omitempty"`
	DeletedAt    *time.Time      `json:"-"`
	OrganizedAt  *time.Time      `json:"organized_at,omitempty"`
	CoverageJSON json.RawMessage `gorm:"type:json" json:"coverage"`
	CreatedAt    time.Time       `gorm:"not null" json:"created_at"`
	UpdatedAt    time.Time       `gorm:"not null" json:"updated_at"`
}

type DocumentDirectoryGroup struct {
	ID          string `gorm:"type:varchar(36);primaryKey" json:"id"`
	DirectoryID string `gorm:"type:varchar(36);not null;index" json:"directory_id"`
	ParentID    string `gorm:"type:varchar(36);not null;default:''" json:"parent_id"`
	Name        string `gorm:"type:varchar(255);not null" json:"name"`
	Position    int    `gorm:"not null;default:0" json:"position"`
}

type DocumentDirectoryEntry struct {
	ID             string          `gorm:"type:varchar(36);primaryKey" json:"id"`
	DirectoryID    string          `gorm:"type:varchar(36);not null;uniqueIndex:uq_directory_resource,priority:1" json:"directory_id"`
	ResourceKey    string          `gorm:"type:varchar(64);not null;uniqueIndex:uq_directory_resource,priority:2" json:"resource_key"`
	GroupID        string          `gorm:"type:varchar(36);not null;default:''" json:"group_id"`
	Source         string          `gorm:"type:varchar(32);not null" json:"source"`
	Namespace      string          `gorm:"type:text;not null" json:"namespace"`
	ResourceID     string          `gorm:"type:text;not null" json:"resource_id"`
	LocatorJSON    json.RawMessage `gorm:"type:json" json:"locator"`
	Title          string          `gorm:"type:text;not null" json:"title"`
	Pinned         bool            `gorm:"not null;default:false" json:"pinned"`
	Excluded       bool            `gorm:"not null;default:false" json:"excluded"`
	ReadState      string          `gorm:"type:varchar(32);not null;default:'unread'" json:"read_state"`
	Confirmed      bool            `gorm:"not null;default:false" json:"confirmed"`
	AccessState    string          `gorm:"type:varchar(32);not null;default:'unknown'" json:"access_state"`
	ContentState   string          `gorm:"type:varchar(32);not null;default:'unknown'" json:"content_state"`
	ContentVersion string          `gorm:"type:text;not null;default:''" json:"content_version"`
	ReadAt         *time.Time      `json:"read_at,omitempty"`
	EvidenceJSON   json.RawMessage `gorm:"type:json" json:"evidence"`
	CreatedAt      time.Time       `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time       `gorm:"not null" json:"updated_at"`
}

type DocumentObservation struct {
	ID             string          `gorm:"type:varchar(64);primaryKey" json:"id"`
	UserID         string          `gorm:"type:varchar(255);not null;index" json:"-"`
	ConversationID string          `gorm:"type:varchar(128);not null;index:idx_document_observation_pending,priority:1" json:"conversation_id"`
	HistoryID      string          `gorm:"type:varchar(128);not null" json:"history_id"`
	RunID          string          `gorm:"type:varchar(128);not null" json:"run_id"`
	CallID         string          `gorm:"type:varchar(255);not null" json:"call_id"`
	Query          string          `gorm:"type:text;not null" json:"query"`
	PayloadJSON    json.RawMessage `gorm:"type:json;not null" json:"payload"`
	BatchID        string          `gorm:"type:varchar(36);not null;default:'';index:idx_document_observation_pending,priority:2" json:"batch_id"`
	CreatedAt      time.Time       `gorm:"not null" json:"created_at"`
}

type DocumentDirectoryBatch struct {
	ID           string          `gorm:"type:varchar(36);primaryKey" json:"id"`
	UserID       string          `gorm:"type:varchar(255);not null;index" json:"-"`
	ParentID     string          `gorm:"type:varchar(36);not null;default:'';index" json:"parent_id"`
	DirectoryID  string          `gorm:"type:varchar(36);not null;default:'';index" json:"directory_id"`
	JobID        string          `gorm:"type:varchar(64);not null;default:''" json:"job_id"`
	TriggerID    string          `gorm:"type:varchar(255);not null;uniqueIndex:uq_directory_batch_trigger,priority:2" json:"-"`
	OwnerKey     string          `gorm:"type:varchar(255);not null;uniqueIndex:uq_directory_batch_trigger,priority:1" json:"-"`
	InputJSON    json.RawMessage `gorm:"type:json;not null" json:"-"`
	Status       string          `gorm:"type:varchar(32);not null" json:"status"`
	ResultJSON   json.RawMessage `gorm:"type:json" json:"result"`
	Acknowledged bool            `gorm:"not null;default:false" json:"acknowledged"`
	CreatedAt    time.Time       `gorm:"not null" json:"created_at"`
	UpdatedAt    time.Time       `gorm:"not null" json:"updated_at"`
}
