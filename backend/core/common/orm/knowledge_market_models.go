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

	// PackageURL is the only download entry point (a git repo URL or a direct
	// file URL); PackageRevision optionally pins a git branch/tag.
	PackageURL      string `gorm:"column:package_url;type:text;not null;default:''"`
	PackageRevision string `gorm:"column:package_revision;type:varchar(64);not null;default:''"`
	// OnlineAccessURL is the public web page used by the P1 online query
	// feature. It must be directly fetchable by the model (url_fetch), so it
	// is decoupled from PackageURL and may stay empty to hide online query.
	OnlineAccessURL string `gorm:"column:online_access_url;type:varchar(1024);not null;default:''"`
	DataSource      string `gorm:"column:data_source;type:text;not null;default:''"`

	SampleQuestions json.RawMessage `gorm:"column:sample_questions;type:json;not null;default:'[]'"`
	Status          string          `gorm:"column:status;type:varchar(32);not null;default:'published';index:idx_knowledge_market_items_category_status,priority:1"` // published | offline
	SortOrder       int             `gorm:"column:sort_order;not null;default:0"`

	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (KnowledgeMarketItem) TableName() string { return "knowledge_market_items" }

// InstallState describes the lifecycle of one user's knowledge base install.
type InstallState string

const (
	InstallStatePending     InstallState = "pending"
	InstallStateDownloading InstallState = "downloading"
	InstallStateImporting   InstallState = "importing"
	InstallStateVectorizing InstallState = "vectorizing"
	InstallStateDone        InstallState = "done"
	InstallStateFailed      InstallState = "failed"
)

// KnowledgeMarketInstall records one user's installation of an official
// knowledge base: the resulting personal dataset plus the runtime file
// snapshot (config) used by later update diffs.
type KnowledgeMarketInstall struct {
	MarketItemID     string          `gorm:"column:market_item_id;type:varchar(64);primaryKey;index:idx_knowledge_market_installs_user,priority:2"`
	UserID           string          `gorm:"column:user_id;type:varchar(255);primaryKey;index:idx_knowledge_market_installs_user,priority:1"`
	InstalledVersion string          `gorm:"column:installed_version;type:varchar(32);not null;default:''"`
	DatasetID        string          `gorm:"column:dataset_id;type:varchar(64);not null;default:''"`
	InstallState     string          `gorm:"column:install_state;type:varchar(32);not null;default:'pending'"`
	InstalledAt      *time.Time      `gorm:"column:installed_at"`
	Config           json.RawMessage `gorm:"column:config;type:json;not null;default:'{}'"`
	CreatedAt        time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt        time.Time       `gorm:"column:updated_at;not null"`
}

func (KnowledgeMarketInstall) TableName() string { return "knowledge_market_installs" }
