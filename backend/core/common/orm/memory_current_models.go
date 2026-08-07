package orm

import "time"

// MemoryCurrentEntry stores the current, unversioned RemoteFS view mounted at
// memory/ for one user. Directories and files share the same per-user path
// namespace; file content is stored inline because these entries are only the
// current working state, not revision history.
type MemoryCurrentEntry struct {
	UserID    string    `gorm:"column:user_id;type:varchar(255);primaryKey"`
	Path      string    `gorm:"column:path;type:varchar(1024);primaryKey"`
	EntryType string    `gorm:"column:entry_type;type:varchar(16);not null"`
	Content   []byte    `gorm:"column:content;type:bytea"`
	Size      int64     `gorm:"column:size;not null;default:0"`
	Mime      string    `gorm:"column:mime;type:varchar(128);not null;default:''"`
	FileType  string    `gorm:"column:file_type;type:varchar(32);not null;default:'unknown'"`
	Binary    bool      `gorm:"column:binary;not null;default:false"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (MemoryCurrentEntry) TableName() string { return "memory_current_entries" }
