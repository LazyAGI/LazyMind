package localworkspace

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type ContextSnapshot struct {
	WorkspaceID       string
	Root              string
	WorkspaceVersion  int64
	PermissionMode    string
	PermissionVersion int64
	Sources           []map[string]any
}

func ResolveForConversation(ctx context.Context, db *gorm.DB, userID, conversationID string) (*ContextSnapshot, error) {
	if db == nil {
		return nil, errors.New("store not initialized")
	}
	var conversation orm.Conversation
	err := db.WithContext(ctx).Where("id = ? AND create_user_id = ?", conversationID, userID).First(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, Error("workspace_not_found", 404, "resource not found")
	}
	if err != nil {
		return nil, err
	}
	if !conversation.IsTaskConv {
		return nil, ModeError()
	}
	var binding orm.ConversationWorkspaceBinding
	err = db.WithContext(ctx).Where("conversation_id = ?", conversationID).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	workspace, err := ResolveActiveForBinding(ctx, db, userID, binding.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return snapshot(workspace, binding.PermissionMode, binding.PermissionVersion), nil
}

func ResolveForDraft(ctx context.Context, db *gorm.DB, userID, workspaceID, permissionMode string) (*ContextSnapshot, error) {
	if !ValidPermissionMode(permissionMode) {
		return nil, Error("invalid_selection", 400, "invalid request")
	}
	workspace, err := ResolveActiveForBinding(ctx, db, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	return snapshot(workspace, permissionMode, 1), nil
}

func snapshot(workspace orm.LocalWorkspace, mode string, version int64) *ContextSnapshot {
	if !ValidPermissionMode(mode) {
		mode = PermissionAskAsNeeded
	}
	if version < 1 {
		version = 1
	}
	return &ContextSnapshot{WorkspaceID: workspace.ID, Root: workspace.CanonicalPath,
		WorkspaceVersion: workspace.Version, PermissionMode: mode, PermissionVersion: version}
}
