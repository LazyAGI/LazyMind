package orm

import "time"

// UserSelectedModel stores the per-user selected model keyed by runtime_models.yaml role key.
// ModelKey corresponds to role keys from runtime_models.yaml (e.g. "llm", "evo_llm", "embed_main").
// It is distinct from UserModelProviderGroupModel.ModelType which stores lazyllm technical types
// (e.g. "llm", "embed", "rerank"). The DB column is named model_type for historical reasons.
type UserSelectedModel struct {
	ID                            int64     `gorm:"column:id;primaryKey;autoIncrement"`
	UserID                        string    `gorm:"column:user_id;type:varchar(255);not null;uniqueIndex:uk_user_selected_models_user_type,priority:1"`
	UserName                      string    `gorm:"column:user_name;type:varchar(255);not null;default:''"`
	ModelKey                      string    `gorm:"column:model_type;type:varchar(64);not null;uniqueIndex:uk_user_selected_models_user_type,priority:2"`
	UserModelProviderGroupModelID string    `gorm:"column:user_model_provider_group_model_id;type:varchar(64);not null"`
	Share                         bool      `gorm:"column:share;type:boolean;not null;default:false"`
	CreatedAt                     time.Time `gorm:"column:created_at;not null"`
	UpdatedAt                     time.Time `gorm:"column:updated_at;not null"`
}

func (UserSelectedModel) TableName() string { return "user_selected_models" }
