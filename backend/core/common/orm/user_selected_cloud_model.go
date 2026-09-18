package orm

import "time"

// UserSelectedCloudModel stores an account-scoped explicit Cloud model choice.
// It contains public catalog metadata only; Cloud credentials and endpoints are
// resolved for each request and are never persisted here.
type UserSelectedCloudModel struct {
	ID                      int64     `gorm:"column:id;primaryKey;autoIncrement"`
	UserID                  string    `gorm:"column:user_id;type:varchar(255);not null;uniqueIndex:uk_user_selected_cloud_models_user_type,priority:1"`
	UserName                string    `gorm:"column:user_name;type:varchar(255);not null;default:''"`
	ModelType               string    `gorm:"column:model_type;type:varchar(64);not null;uniqueIndex:uk_user_selected_cloud_models_user_type,priority:2"`
	PublicModelKey          string    `gorm:"column:public_model_key;type:varchar(96);not null;index:idx_user_selected_cloud_models_public_key"`
	DisplayNameSnapshot     string    `gorm:"column:display_name_snapshot;type:varchar(128);not null"`
	CatalogRevisionSnapshot string    `gorm:"column:catalog_revision_snapshot;type:varchar(128)"`
	CreatedAt               time.Time `gorm:"column:created_at;not null"`
	UpdatedAt               time.Time `gorm:"column:updated_at;not null"`
}

func (UserSelectedCloudModel) TableName() string { return "user_selected_cloud_models" }
