package orm

import (
	"encoding/json"
	"time"
)

type EvalSetImportPreview struct {
	Token            string          `gorm:"column:token;type:varchar(64);primaryKey"`
	Status           string          `gorm:"column:status;type:varchar(32);not null;default:'ready';index"`
	FileName         string          `gorm:"column:file_name;type:varchar(512);not null;default:''"`
	FileType         string          `gorm:"column:file_type;type:varchar(16);not null"`
	TempPath         string          `gorm:"column:temp_path;type:text;not null;default:''"`
	TotalRows        int64           `gorm:"column:total_rows;not null;default:0"`
	EmptyRows        int64           `gorm:"column:empty_rows;not null;default:0"`
	ValidRows        int64           `gorm:"column:valid_rows;not null;default:0"`
	PreviewRowsJSON  json.RawMessage `gorm:"column:preview_rows_json;type:json"`
	ErrorDetailsJSON json.RawMessage `gorm:"column:error_details_json;type:json"`
	CreateUserID     string          `gorm:"column:create_user_id;type:varchar(255);not null"`
	CreateUserName   string          `gorm:"column:create_user_name;type:varchar(255);not null;default:''"`
	CreatedAt        time.Time       `gorm:"column:created_at;not null"`
	ExpiresAt        time.Time       `gorm:"column:expires_at;not null;index"`
	ConsumedAt       *time.Time      `gorm:"column:consumed_at"`
}

func (EvalSetImportPreview) TableName() string { return "eval_set_import_previews" }
