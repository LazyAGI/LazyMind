package orm

import "time"

type UserUIPreferences struct {
	UserID                        string    `gorm:"column:user_id;type:varchar(255);primaryKey"`
	ChatPreferenceNoticeDismissed bool      `gorm:"column:chat_preference_notice_dismissed;not null;default:false"`
	DeveloperModeActive           bool      `gorm:"column:developer_mode_active;not null;default:false"`
	CreatedAt                     time.Time `gorm:"column:created_at;not null"`
	UpdatedAt                     time.Time `gorm:"column:updated_at;not null"`
}

func (UserUIPreferences) TableName() string { return "user_ui_preferences" }
