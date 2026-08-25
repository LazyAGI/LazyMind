package orm

import "time"

// WriterDownloadConversion indexes a derived Writer download stored on disk.
// The converted bytes remain in the subagent workspace, matching normal Writer
// artifacts; this row only makes the derived file reusable across sessions.
type WriterDownloadConversion struct {
	ID           string    `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID       string    `gorm:"column:user_id;type:varchar(255);not null;uniqueIndex:uk_writer_download_conversion,priority:1"`
	SourceHash   string    `gorm:"column:source_hash;type:varchar(64);not null;uniqueIndex:uk_writer_download_conversion,priority:2"`
	TargetFormat string    `gorm:"column:target_format;type:varchar(16);not null;uniqueIndex:uk_writer_download_conversion,priority:3"`
	Filename     string    `gorm:"column:filename;type:varchar(255);not null"`
	MIMEType     string    `gorm:"column:mime_type;type:varchar(128);not null"`
	StoragePath  string    `gorm:"column:storage_path;type:varchar(1024);not null"`
	Size         int64     `gorm:"column:size;not null"`
	ContentHash  string    `gorm:"column:content_hash;type:varchar(64);not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

func (WriterDownloadConversion) TableName() string { return "writer_download_conversions" }
