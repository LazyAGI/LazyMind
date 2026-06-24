package orm

import "time"

type LocalFSChatSetting struct {
	ID             int64     `gorm:"column:id;primaryKey;autoIncrement"`
	CreateUserID   string    `gorm:"column:create_user_id;type:varchar(255);not null;uniqueIndex:uk_local_fs_chat_settings_user"`
	CreateUserName string    `gorm:"column:create_user_name;type:varchar(255);not null;default:''"`
	Enabled        bool      `gorm:"column:enabled;not null;default:false"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (LocalFSChatSetting) TableName() string { return "local_fs_chat_settings" }
