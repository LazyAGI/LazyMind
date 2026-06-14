package orm

import (
	"encoding/json"
	"time"
)

type SystemMemory struct {
	ID                 string          `gorm:"column:id;type:varchar(36);primaryKey"`
	UserID             string          `gorm:"column:user_id;type:varchar(255);not null;default:'';uniqueIndex:uk_system_memories_user_id"`
	Content            string          `gorm:"column:content;type:text;not null;default:''"`
	ContentHash        string          `gorm:"column:content_hash;type:varchar(64);not null;default:''"`
	Version            int64           `gorm:"column:version;not null;default:1"`
	DraftContent       string          `gorm:"column:draft_content;type:text"`
	DraftSourceVersion int64           `gorm:"column:draft_source_version;not null;default:0"`
	DraftStatus        string          `gorm:"column:draft_status;type:varchar(32);not null;default:''"`
	DraftUpdatedAt     *time.Time      `gorm:"column:draft_updated_at"`
	AutoEvo            bool            `gorm:"column:auto_evo;not null;default:true"` // 自动进化
	AutoEvoApplyStatus string          `gorm:"column:auto_evo_apply_status;type:varchar(32);not null;default:'idle'"`
	AutoEvoGeneration  int64           `gorm:"column:auto_evo_generation;not null;default:0"`
	AutoEvoStartedAt   *time.Time      `gorm:"column:auto_evo_started_at"`
	AutoEvoFinishedAt  *time.Time      `gorm:"column:auto_evo_finished_at"`
	AutoEvoError       string          `gorm:"column:auto_evo_error;type:text;not null;default:''"`
	Ext                json.RawMessage `gorm:"column:ext;type:json"`
	UpdatedBy          string          `gorm:"column:updated_by;type:varchar(255);not null;default:''"`
	UpdatedByName      string          `gorm:"column:updated_by_name;type:varchar(255);not null;default:''"`
	CreatedAt          time.Time       `gorm:"column:created_at;not null"`
	UpdatedAt          time.Time       `gorm:"column:updated_at;not null"`
}

func (SystemMemory) TableName() string { return "system_memories" }
