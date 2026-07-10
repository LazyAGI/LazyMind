package orm

import "time"

type SkillShareTask struct {
	ID                    string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SourceUserID          string    `gorm:"column:source_user_id;type:varchar(255);not null;index:idx_skill_share_tasks_source_user"`
	SourceUserName        string    `gorm:"column:source_user_name;type:varchar(255);not null;default:''"`
	SourceSkillID         string    `gorm:"column:source_skill_id;type:varchar(36);not null"`
	SourceCategory        string    `gorm:"column:source_category;type:varchar(128);not null;default:''"`
	SourceParentSkillName string    `gorm:"column:source_parent_skill_name;type:varchar(255);not null;default:''"`
	SourceRelativeRoot    string    `gorm:"column:source_relative_root;type:varchar(1024);not null;default:''"`
	Message               string    `gorm:"column:message;type:text"`
	CreatedAt             time.Time `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time `gorm:"column:updated_at;not null"`
}

func (SkillShareTask) TableName() string { return "skill_share_tasks" }

type SkillShareItem struct {
	ID                 string     `gorm:"column:id;type:varchar(36);primaryKey"`
	ShareTaskID        string     `gorm:"column:share_task_id;type:varchar(36);not null;index:idx_skill_share_items_target_user,priority:1"`
	SourceSkillID      string     `gorm:"column:source_skill_id;type:varchar(36);not null;default:'';index:idx_skill_share_items_source_skill"`
	TargetUserID       string     `gorm:"column:target_user_id;type:varchar(255);not null;index:idx_skill_share_items_target_user,priority:2"`
	TargetUserName     string     `gorm:"column:target_user_name;type:varchar(255);not null;default:''"`
	Status             string     `gorm:"column:status;type:varchar(32);not null;index:idx_skill_share_items_target_user,priority:3"`
	TargetRelativeRoot string     `gorm:"column:target_relative_root;type:varchar(1024);not null;default:''"`
	AcceptedAt         *time.Time `gorm:"column:accepted_at"`
	RejectedAt         *time.Time `gorm:"column:rejected_at"`
	TargetRootSkillID  string     `gorm:"column:target_root_skill_id;type:varchar(36);not null;default:''"`
	ErrorMessage       string     `gorm:"column:error_message;type:text"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null"`
}

func (SkillShareItem) TableName() string { return "skill_share_items" }
