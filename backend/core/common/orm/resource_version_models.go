package orm

import "time"

type ResourceVersion struct {
	ID            string    `gorm:"column:id;type:varchar(36);primaryKey"`
	ResourceType  string    `gorm:"column:resource_type;type:varchar(32);not null;index:idx_resource_versions_resource,priority:1;index:idx_resource_versions_user_resource,priority:2"`
	ResourceID    string    `gorm:"column:resource_id;type:varchar(128);not null;index:idx_resource_versions_resource,priority:2;index:idx_resource_versions_user_resource,priority:3"`
	UserID        string    `gorm:"column:user_id;type:varchar(255);not null;index:idx_resource_versions_user_resource,priority:1"`
	ChangeSource  string    `gorm:"column:change_source;type:varchar(32);not null"`
	FromVersion   int64     `gorm:"column:from_version;not null;default:0"`
	ToVersion     int64     `gorm:"column:to_version;not null;default:0"`
	SourceRefType string    `gorm:"column:source_ref_type;type:varchar(64);not null;default:''"`
	SourceRefID   string    `gorm:"column:source_ref_id;type:varchar(128);not null;default:''"`
	BeforeContent string    `gorm:"column:before_content;type:text;not null;default:''"`
	AfterContent  string    `gorm:"column:after_content;type:text;not null;default:''"`
	Diff          string    `gorm:"column:diff;type:text;not null;default:''"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;index:idx_resource_versions_resource,priority:3;index:idx_resource_versions_user_resource,priority:4"`
}

func (ResourceVersion) TableName() string { return "resource_versions" }
