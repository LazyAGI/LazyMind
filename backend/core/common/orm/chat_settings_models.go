package orm

import (
	"encoding/json"
	"time"
)

// UserChatSettings stores per-user quick-question/new-task defaults.
// Legacy flat columns remain the compatibility defaults for older clients.
type UserChatSettings struct {
	UserID         string `gorm:"column:user_id;type:varchar(255);primaryKey"`
	EnableWorkflow bool   `gorm:"column:enable_workflow;not null;default:true"`
	WorkflowMode   string `gorm:"column:plugin_mode;type:varchar(16);not null;default:dynamic"` // dynamic | auto
	EnableSubagent bool   `gorm:"column:enable_subagent;not null;default:true"`
	// Entry defaults are stored as complete JSON snapshots because the quick-question
	// and new-task composers read and update them independently.
	QuickQuestionDefaults json.RawMessage `gorm:"column:quick_question_defaults;type:json;not null;default:'{}'"`
	NewTaskDefaults       json.RawMessage `gorm:"column:new_task_defaults;type:json;not null;default:'{}'"`
	UpdatedAt             time.Time       `gorm:"column:updated_at;not null"`
}

func (UserChatSettings) TableName() string { return "user_chat_settings" }
