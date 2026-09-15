package orm

import (
	"encoding/json"
	"time"
)

// Dataset
// text ragservice DDL text，text dbmigrate text/text DDL。
type Dataset struct {
	ID string `gorm:"primaryKey;column:id;type:varchar(255)"`

	// KbID textCreatetext kb_id。
	KbID string `gorm:"column:kb_id;type:varchar(255);not null;index:idx_datasets_kb_id"`

	DisplayName string `gorm:"column:display_name;type:varchar(255);not null"`
	Desc        string `gorm:"column:desc;type:text;not null"`
	CoverImage  string `gorm:"column:cover_image;type:varchar(255);not null"`

	ResourceUID string `gorm:"column:resource_uid;type:varchar(36);not null;index:idx_resource_uid"`
	BucketName  string `gorm:"column:bucket_name;type:varchar(255);not null"`
	OssPath     string `gorm:"column:oss_path;type:varchar(255);not null"`

	DatasetInfo            json.RawMessage `gorm:"column:dataset_info;type:json"`
	DatasetState           uint8           `gorm:"column:dataset_state;not null"`
	ProcessingLevel        string          `gorm:"column:processing_level;type:varchar(16);not null;default:'indexed';index"`
	ProcessingRevision     int64           `gorm:"column:processing_revision;not null;default:1"`
	TransitionStatus       string          `gorm:"column:transition_status;type:varchar(32);not null;default:'idle'"`
	ReaderFallbackAccepted bool            `gorm:"column:reader_fallback_accepted;not null;default:false"`
	ProcessingConfig       json.RawMessage `gorm:"column:processing_config;type:json"`

	EmbeddingModel         string `gorm:"column:embedding_model;type:varchar(255);not null"`
	EmbeddingModelProvider string `gorm:"column:embedding_model_provider;type:varchar(255);not null"`

	ShareType uint8 `gorm:"column:share_type;not null"`
	// shared_at / tenant_id / is_demonstrate / type / ext text DDL text，
	// text DDL text。
	SharedAt *time.Time `gorm:"column:shared_at"`

	TenantID      string `gorm:"column:tenant_id;type:varchar(36);not null"`
	IsDemonstrate bool   `gorm:"column:is_demonstrate;not null;default:false"`
	Type          uint8  `gorm:"column:type;not null;default:1"`

	Ext json.RawMessage `gorm:"column:ext;type:json"`

	BaseModel
}

func (Dataset) TableName() string { return "datasets" }

// DefaultDataset
type DefaultDataset struct {
	ID int64 `gorm:"primaryKey;column:id;autoIncrement"`

	DatasetID   string `gorm:"column:dataset_id;type:varchar(64);not null;uniqueIndex:ukx_create_user_id_dataset_id,priority:2"`
	DatasetName string `gorm:"column:dataset_name;type:varchar(255);not null"`

	BaseModel
}

func (DefaultDataset) TableName() string { return "default_datasets" }
