package orm

import "encoding/json"

// UploadedFile stores a standalone uploaded file record before it is bound to a document/task.
type UploadedFile struct {
	ID           int64  `gorm:"column:id;primaryKey;autoIncrement"`
	UploadFileID string `gorm:"column:upload_file_id;type:varchar(128);not null;uniqueIndex"`

	DatasetID   string `gorm:"column:dataset_id;type:varchar(255);not null;index"`
	TenantID    string `gorm:"column:tenant_id;type:varchar(36);not null;index"`
	ContentHash string `gorm:"column:content_hash;type:varchar(64);not null;default:''"`

	TaskID     string          `gorm:"column:task_id;type:varchar(128);not null;default:'';index"`
	DocumentID string          `gorm:"column:document_id;type:varchar(128);not null;default:'';index"`
	Status     string          `gorm:"column:status;type:varchar(64);not null;default:'';index"`
	Ext        json.RawMessage `gorm:"column:ext;type:json"`

	BaseModel
}

func (UploadedFile) TableName() string { return "uploaded_files" }
