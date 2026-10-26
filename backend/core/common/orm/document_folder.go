package orm

import (
	"encoding/json"
	"time"
)

// A folder inventory is an execution-owned, resumable enumeration, not model output.
type DocumentFolderInventory struct {
	ID             string          `gorm:"type:varchar(36);primaryKey" json:"id"`
	UserID         string          `gorm:"type:varchar(255);not null;index" json:"-"`
	ConversationID string          `gorm:"type:varchar(128);not null" json:"-"`
	SourceJSON     json.RawMessage `gorm:"type:json;not null" json:"source"`
	StateJSON      json.RawMessage `gorm:"type:json;not null" json:"state"`
	Status         string          `gorm:"type:varchar(32);not null" json:"status"`
	Version        int64           `gorm:"not null" json:"version"`
	DirectoryID    string          `gorm:"type:varchar(36);not null;default:'';index" json:"directory_id"`
	BatchID        string          `gorm:"type:varchar(36);not null;default:''" json:"batch_id"`
	CreatedAt      time.Time       `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time       `gorm:"not null" json:"updated_at"`
}

type DocumentFolderNode struct {
	ID            string          `gorm:"type:varchar(64);primaryKey" json:"id"`
	InventoryID   string          `gorm:"type:varchar(36);not null;index:idx_folder_parent,priority:1;uniqueIndex:uq_folder_node,priority:1" json:"inventory_id"`
	NodeKey       string          `gorm:"type:varchar(64);not null;uniqueIndex:uq_folder_node,priority:2" json:"node_key"`
	ParentKey     string          `gorm:"type:varchar(64);not null;index:idx_folder_parent,priority:2" json:"parent_key"`
	Path          string          `gorm:"type:text;not null" json:"path"`
	Name          string          `gorm:"type:text;not null" json:"name"`
	Kind          string          `gorm:"type:varchar(32);not null" json:"kind"`
	Listed        bool            `gorm:"not null" json:"listed"`
	ResourceKey   string          `gorm:"type:varchar(64);not null;default:''" json:"resource_key"`
	CandidateJSON json.RawMessage `gorm:"type:json" json:"candidate,omitempty"`
}
