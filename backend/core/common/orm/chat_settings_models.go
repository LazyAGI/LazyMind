package orm

import "time"

// UserChatSettings stores per-user global defaults for plugin/subagent configuration.
// When a conversation has no explicit override, these values are used.
type UserChatSettings struct {
	UserID         string    `gorm:"column:user_id;type:varchar(255);primaryKey"`
	EnableWorkflow bool      `gorm:"column:enable_workflow;not null;default:true"`
	WorkflowMode   string    `gorm:"column:plugin_mode;type:varchar(16);not null;default:dynamic"` // dynamic | auto
	EnableSubagent bool      `gorm:"column:enable_subagent;not null;default:true"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (UserChatSettings) TableName() string { return "user_chat_settings" }
