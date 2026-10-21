package orm

import (
	"encoding/json"
	"time"
)

// PaperLibrary stores the complete editable library graph in its dataset scope.
// Revision is compared atomically when replacing DataJSON to prevent lost edits.
type PaperLibrary struct {
	DatasetID string          `gorm:"column:dataset_id;type:varchar(255);primaryKey;not null"`
	DataJSON  json.RawMessage `gorm:"column:data_json;type:json;not null"`
	Revision  int64           `gorm:"column:revision;not null;default:0;check:revision >= 0"`
	CreatedAt time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt time.Time       `gorm:"column:updated_at;not null"`
}

func (PaperLibrary) TableName() string { return "paper_libraries" }
