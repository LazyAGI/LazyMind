package orm

import (
	"encoding/json"
	"time"
)

type SkillResource struct {
	ID                    string          `gorm:"column:id;type:varchar(36);primaryKey"`
	OwnerUserID           string          `gorm:"column:owner_user_id;type:varchar(255);not null;index:idx_skill_resources_owner_node_enabled,priority:1;uniqueIndex:uk_skill_resources_owner_relative_path,priority:1"`
	OwnerUserName         string          `gorm:"column:owner_user_name;type:varchar(255);not null;default:''"`
	OriginBuiltinSkillUID string          `gorm:"column:origin_builtin_skill_uid;type:varchar(64);not null;default:'';index:idx_skill_resources_origin_builtin_uid"`
	Category              string          `gorm:"column:category;type:varchar(128);not null;index:idx_skill_resources_owner_node_enabled,priority:4"`
	ParentSkillName       string          `gorm:"column:parent_skill_name;type:varchar(255);not null;default:''"`
	SkillName             string          `gorm:"column:skill_name;type:varchar(255);not null;default:''"`
	NodeType              string          `gorm:"column:node_type;type:varchar(32);not null;index:idx_skill_resources_owner_node_enabled,priority:2"`
	Description           string          `gorm:"column:description;type:text"`
	Tags                  json.RawMessage `gorm:"column:tags;type:json"`
	FileExt               string          `gorm:"column:file_ext;type:varchar(32);not null;default:'md'"`
	RelativePath          string          `gorm:"column:relative_path;type:varchar(1024);not null;uniqueIndex:uk_skill_resources_owner_relative_path,priority:2"`
	Content               string          `gorm:"column:content;type:text;not null;default:''"`
	ContentSize           int64           `gorm:"column:content_size;not null;default:0"`
	MimeType              string          `gorm:"column:mime_type;type:varchar(128);not null;default:'text/plain; charset=utf-8'"`
	ContentHash           string          `gorm:"column:content_hash;type:varchar(64);not null;default:''"`
	Version               int64           `gorm:"column:version;not null;default:1"`
	DraftContent          string          `gorm:"column:draft_content;type:text;not null;default:''"`
	DraftSourceVersion    int64           `gorm:"column:draft_source_version;not null;default:0"`
	DraftStatus           string          `gorm:"column:draft_status;type:varchar(32);not null;default:''"`
	DraftUpdatedAt        *time.Time      `gorm:"column:draft_updated_at"`
	AutoEvo               bool            `gorm:"column:auto_evo;not null;default:false"` // 自动进化
	AutoEvoApplyStatus    string          `gorm:"column:auto_evo_apply_status;type:varchar(32);not null;default:'idle'"`
	AutoEvoGeneration     int64           `gorm:"column:auto_evo_generation;not null;default:0"`
	AutoEvoStartedAt      *time.Time      `gorm:"column:auto_evo_started_at"`
	AutoEvoFinishedAt     *time.Time      `gorm:"column:auto_evo_finished_at"`
	AutoEvoError          string          `gorm:"column:auto_evo_error;type:text;not null;default:''"`
	IsEnabled             bool            `gorm:"column:is_enabled;not null;default:true;index:idx_skill_resources_owner_node_enabled,priority:3"`
	UpdateStatus          string          `gorm:"column:update_status;type:varchar(32);not null;default:'up_to_date'"`
	Ext                   json.RawMessage `gorm:"column:ext;type:json"`
	CreateUserID          string          `gorm:"column:create_user_id;type:varchar(255);not null"`
	CreateUserName        string          `gorm:"column:create_user_name;type:varchar(255);not null;default:''"`
	CreatedAt             time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time       `gorm:"column:updated_at;not null"`
}

func (SkillResource) TableName() string { return "skill_resources" }
