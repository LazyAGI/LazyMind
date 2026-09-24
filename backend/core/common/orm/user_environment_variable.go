package orm

import (
	"time"

	"gorm.io/gorm"
)

type UserEnvironmentVariable struct {
	ID                 string         `gorm:"column:id;type:varchar(64);primaryKey"`
	UserID             string         `gorm:"column:user_id;type:varchar(255);not null;uniqueIndex:idx_user_env_user_name_active,where:deleted_at IS NULL,priority:1;index:idx_user_env_user_enabled,priority:1"`
	Name               string         `gorm:"column:name;type:varchar(128);not null;uniqueIndex:idx_user_env_user_name_active,where:deleted_at IS NULL,priority:2"`
	ValueCiphertext    string         `gorm:"column:value_ciphertext;type:text;not null"`
	CredentialVersion  int            `gorm:"column:credential_version;not null;default:2"`
	CredentialRevision int64          `gorm:"column:credential_revision;not null;default:1"`
	Enabled            bool           `gorm:"column:enabled;not null;index:idx_user_env_user_enabled,priority:2"`
	Description        string         `gorm:"column:description;type:varchar(512);not null;default:''"`
	CreatedAt          time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt          time.Time      `gorm:"column:updated_at;not null"`
	DeletedAt          gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (UserEnvironmentVariable) TableName() string {
	return "user_environment_variables"
}
