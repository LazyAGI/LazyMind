package orm

import "time"

// UserSelectedModel stores per-user selected model by model_type.
type UserSelectedModel struct {
	ID                            int64     `gorm:"column:id;primaryKey;autoIncrement"`
	UserID                        string    `gorm:"column:user_id;type:varchar(255);not null;uniqueIndex:uk_user_selected_models_user_type,priority:1"`
	UserName                      string    `gorm:"column:user_name;type:varchar(255);not null;default:''"`
	ModelType                     string    `gorm:"column:model_type;type:varchar(64);not null;uniqueIndex:uk_user_selected_models_user_type,priority:2"`
	UserModelProviderGroupModelID string    `gorm:"column:user_model_provider_group_model_id;type:varchar(64);not null"`
	Share                         bool      `gorm:"column:share;type:boolean;not null;default:false"`
	CreatedAt                     time.Time `gorm:"column:created_at;not null"`
	UpdatedAt                     time.Time `gorm:"column:updated_at;not null"`
}

func (UserSelectedModel) TableName() string { return "user_selected_models" }
