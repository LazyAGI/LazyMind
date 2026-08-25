package settings

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// ConversationExecutionPolicy is the legacy user-level execution policy that
// must be snapshotted when a conversation is created outside the Chat handler.
type ConversationExecutionPolicy struct {
	EnableWorkflow bool
	WorkflowMode   string
	EnableSubagent bool
}

func DefaultConversationExecutionPolicy() ConversationExecutionPolicy {
	return ConversationExecutionPolicy{
		EnableWorkflow: true,
		WorkflowMode:   "dynamic",
		EnableSubagent: true,
	}
}

// LoadConversationExecutionPolicy preserves the historical user-level fallback
// contract. Missing rows and read failures use the upgrade-safe hard defaults.
func LoadConversationExecutionPolicy(ctx context.Context, db *gorm.DB, userID string) ConversationExecutionPolicy {
	policy := DefaultConversationExecutionPolicy()
	if db == nil || strings.TrimSpace(userID) == "" {
		return policy
	}
	var row struct {
		EnableWorkflow bool   `gorm:"column:enable_workflow"`
		WorkflowMode   string `gorm:"column:plugin_mode"`
		EnableSubagent bool   `gorm:"column:enable_subagent"`
	}
	if err := db.WithContext(ctx).Model(&orm.UserChatSettings{}).
		Select("enable_workflow", "plugin_mode", "enable_subagent"). // workflow-naming: persistence
		Where("user_id = ?", strings.TrimSpace(userID)).Take(&row).Error; err != nil {
		return policy
	}
	policy.EnableWorkflow = row.EnableWorkflow
	policy.EnableSubagent = row.EnableSubagent
	mode := strings.ToLower(strings.TrimSpace(row.WorkflowMode))
	if mode == "auto" || mode == "dynamic" {
		policy.WorkflowMode = mode
	}
	return policy
}
