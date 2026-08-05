package orm

import (
	"encoding/json"
	"time"
)

// KnowledgeMarketItem is a single official knowledge base entry shown in the
// knowledge plaza. The catalog is the single source of truth
// (config/knowledge_market_catalog.yaml): it is upserted at startup and
// refreshed on every system release. There is no admin console.
type KnowledgeMarketItem struct {
	ID          string          `gorm:"column:id;type:varchar(64);primaryKey"`
	Category    string          `gorm:"column:category;type:varchar(32);not null;index:idx_knowledge_market_items_category_status,priority:2"` // industry | evaluation
	Name        string          `gorm:"column:name;type:varchar(255);not null"`
	Description string          `gorm:"column:description;type:text;not null;default:''"`
	Icon        string          `gorm:"column:icon;type:text;not null;default:''"`
	Domain      string          `gorm:"column:domain;type:varchar(64);not null;default:''"`
	Tags        json.RawMessage `gorm:"column:tags;type:json;not null;default:'[]'"`

	Version     string `gorm:"column:version;type:varchar(32);not null;default:''"`
	VersionDate string `gorm:"column:version_date;type:varchar(10);not null;default:''"`
	VersionNote string `gorm:"column:version_note;type:text;not null;default:''"`

	PackageURL    string `gorm:"column:package_url;type:text;not null;default:''"`
	PackageSHA256 string `gorm:"column:package_sha256;type:varchar(64);not null;default:''"`
	PackageSize   int64  `gorm:"column:package_size;not null;default:0"`
	DocCount      int64  `gorm:"column:doc_count;not null;default:0"`
	DataSource    string `gorm:"column:data_source;type:text;not null;default:''"`

	Files           json.RawMessage `gorm:"column:files;type:json;not null;default:'[]'"`
	SampleQuestions json.RawMessage `gorm:"column:sample_questions;type:json;not null;default:'[]'"`
	Status          string          `gorm:"column:status;type:varchar(32);not null;default:'published';index:idx_knowledge_market_items_category_status,priority:1"` // published | offline
	SortOrder       int             `gorm:"column:sort_order;not null;default:0"`

	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (KnowledgeMarketItem) TableName() string { return "knowledge_market_items" }

// KnowledgeMarketInstall records one user's installation of an official
// knowledge base. It intentionally stays minimal until the M2 install flow
// adds dataset_id, install_state, config and usage statistics.
type KnowledgeMarketInstall struct {
	MarketItemID     string    `gorm:"column:market_item_id;type:varchar(64);primaryKey;index:idx_knowledge_market_installs_user,priority:2"`
	UserID           string    `gorm:"column:user_id;type:varchar(255);primaryKey;index:idx_knowledge_market_installs_user,priority:1"`
	InstalledVersion string    `gorm:"column:installed_version;type:varchar(32);not null;default:''"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null"`
}

func (KnowledgeMarketInstall) TableName() string { return "knowledge_market_installs" }
