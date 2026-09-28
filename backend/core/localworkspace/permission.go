package localworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

// UserPermission is shared by both chat entry points, never scoped to a directory.
func UserPermission(ctx context.Context, db *gorm.DB, userID string) (string, int64, error) {
	if db == nil {
		return "", 0, errors.New("store not initialized")
	}
	var row orm.UserChatSettings
	err := db.WithContext(ctx).Select("default_permission_mode", "permission_version").Where("user_id = ?", userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PermissionAlwaysAsk, 1, nil
	}
	if err != nil {
		return "", 0, err
	}
	if !ValidPermissionMode(row.DefaultPermissionMode) {
		return PermissionAlwaysAsk, 1, nil
	}
	return row.DefaultPermissionMode, row.PermissionVersion, nil
}

// SaveUserPermission must run inside the caller's transaction. CAS protects
// against stale composers, including when the settings row does not exist yet.
func SaveUserPermission(ctx context.Context, tx *gorm.DB, userID, mode string, version int64) (int64, error) {
	if !ValidPermissionMode(mode) || version < 1 {
		return 0, Error("invalid_selection", 400, "invalid request")
	}
	seed := orm.UserChatSettings{UserID: userID, DefaultPermissionMode: PermissionAlwaysAsk, PermissionVersion: 1,
		QuickQuestionDefaults: json.RawMessage(`{}`), NewTaskDefaults: json.RawMessage(`{}`),
		EnableWorkflow: true, WorkflowMode: "dynamic", EnableSubagent: true, UpdatedAt: time.Now().UTC()}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return 0, err
	}
	result := tx.Model(&orm.UserChatSettings{}).Where("user_id = ? AND permission_version = ?", userID, version).
		Updates(map[string]any{"default_permission_mode": mode, "permission_version": version + 1, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, Error("binding_conflict", 409, "conflict")
	}
	return version + 1, nil
}

// Legacy fallback supports rows created before the migration. New conversations
// always persist an explicit snapshot; historical unbound rows never inherit trust.
func conversationPermission(conversation orm.Conversation, binding *orm.ConversationWorkspaceBinding) (string, int64) {
	if ValidPermissionMode(conversation.PermissionMode) && conversation.PermissionVersion > 0 {
		return conversation.PermissionMode, conversation.PermissionVersion
	}
	if binding != nil {
		return binding.PermissionMode, binding.PermissionVersion
	}
	return PermissionAlwaysAsk, 1
}
