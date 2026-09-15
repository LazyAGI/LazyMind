package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/localworkspace"
)

func requestedWorkspaceID(raw map[string]any) (string, bool) {
	value, present := raw["workspace_id"]
	if !present {
		return "", false
	}
	id, ok := value.(string)
	if !ok {
		return "", true
	}
	return strings.TrimSpace(id), true
}

func requestedWorkspacePermissionMode(raw map[string]any) (string, bool) {
	value, present := raw["workspace_permission_mode"]
	if !present {
		return localworkspace.PermissionAskAsNeeded, false
	}
	mode, ok := value.(string)
	if !ok {
		return "", true
	}
	return strings.TrimSpace(mode), true
}

func validateWorkspaceRequestMode(raw map[string]any) *common.AppError {
	workspaceID, workspacePresent := requestedWorkspaceID(raw)
	permissionMode, permissionPresent := requestedWorkspacePermissionMode(raw)
	if !workspacePresent && !permissionPresent {
		return nil
	}
	if !workspacePresent || workspaceID == "" || len(workspaceID) > 128 ||
		!localworkspace.ValidPermissionMode(permissionMode) {
		return localworkspace.Error("invalid_selection", 400, "invalid request")
	}
	if !localworkspace.Enabled() {
		return localworkspace.ModeError()
	}
	return nil
}

func ensureConversationWithWorkspace(
	ctx context.Context, db *gorm.DB, convID, displayName string,
	searchConfig, models json.RawMessage, userID, userName string,
	runInBackground bool, requestedThinkingDepth string,
	conversationSettings map[string]any, initialModelSelection *initialChatModelSelection,
	raw map[string]any,
) (*orm.Conversation, int, error) {
	var conversation *orm.Conversation
	var seq int
	err := conversationgroup.UserTransaction(ctx, db, userID, func(tx *gorm.DB) error {
		var err error
		conversation, seq, err = ensureConversationWithWorkspaceTx(ctx, tx, convID, displayName,
			searchConfig, models, userID, userName, runInBackground, requestedThinkingDepth,
			conversationSettings, initialModelSelection, raw)
		return err
	})
	return conversation, seq, err
}

// ensureConversationWithWorkspaceTx performs the workspace binding and
// conversation creation on an already-open transaction. Conversation creation
// with a group uses the caller's user transaction so membership and workspace
// binding remain atomic without attempting a nested SQLite transaction.
func ensureConversationWithWorkspaceTx(
	ctx context.Context, tx *gorm.DB, convID, displayName string,
	searchConfig, models json.RawMessage, userID, userName string,
	runInBackground bool, requestedThinkingDepth string,
	conversationSettings map[string]any, initialModelSelection *initialChatModelSelection,
	raw map[string]any,
) (*orm.Conversation, int, error) {
	workspaceID, workspacePresent := requestedWorkspaceID(raw)
	permissionMode, _ := requestedWorkspacePermissionMode(raw)
	var conversation *orm.Conversation
	var seq int
	var existing orm.Conversation
	existingErr := tx.Where("id = ? AND create_user_id = ?", convID, userID).First(&existing).Error
	exists := existingErr == nil
	if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
		return nil, 0, existingErr
	}
	if exists && workspacePresent {
		var binding orm.ConversationWorkspaceBinding
		err := tx.Where("conversation_id = ?", convID).First(&binding).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && binding.WorkspaceID != workspaceID) {
			return nil, 0, localworkspace.Error("binding_locked", 409, "conflict")
		}
		if err != nil {
			return nil, 0, err
		}
	}
	groupID, _ := raw["group_id"].(string)
	groupID = strings.TrimSpace(groupID)
	if !exists && groupID != "" {
		var group orm.ConversationGroup
		if err := tx.Where("id=? AND user_id=? AND deleted_at IS NULL", groupID, userID).Take(&group).Error; err != nil {
			return nil, 0, conversationgroup.ErrConversationGroupNotFound
		}
		if group.Kind == conversationgroup.KindProject {
			if group.WorkspaceID == nil || workspacePresent && workspaceID != *group.WorkspaceID {
				return nil, 0, common.ResolveAppError("conversation project directory_conflict", 409)
			}
			workspaceID = *group.WorkspaceID
			workspacePresent = true
		} else if workspacePresent {
			return nil, 0, common.ResolveAppError("conversation project directory_conflict", 409)
		}
	}
	if !exists && workspacePresent {
		name, _ := raw["project_name"].(string)
		if value, present := raw["project_name"]; present && value != nil {
			typed, ok := value.(string)
			if !ok || strings.TrimSpace(typed) == "" {
				return nil, 0, common.ResolveAppError("conversation project invalid_name", 400)
			}
		}
		project, err := conversationgroup.EnsureProject(ctx, tx, userID, workspaceID, name)
		if err != nil {
			return nil, 0, err
		}
		if groupID != "" && groupID != project.ID {
			return nil, 0, common.ResolveAppError("conversation project directory_conflict", 409)
		}
		groupID = project.ID
	}
	if !exists && workspacePresent {
		if _, err := localworkspace.ResolveActiveForBinding(ctx, tx, userID, workspaceID); err != nil {
			return nil, 0, err
		}
	}
	created, nextSeq, err := ensureConversation(ctx, tx, convID, displayName,
		searchConfig, models, userID, userName, runInBackground,
		requestedThinkingDepth, conversationSettings, initialModelSelection)
	if err != nil {
		return nil, 0, err
	}
	conversation, seq = created, nextSeq
	if !exists && runInBackground {
		if err := tx.Model(&orm.Conversation{}).Where("id = ?", convID).Update("is_task_conv", true).Error; err != nil {
			return nil, 0, err
		}
		conversation.IsTaskConv = true
	}
	if !exists && workspacePresent {
		now := time.Now().UTC()
		binding := orm.ConversationWorkspaceBinding{ConversationID: convID, WorkspaceID: workspaceID,
			PermissionMode: permissionMode, PermissionVersion: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&binding).Error; err != nil {
			return nil, 0, localworkspace.Error("binding_conflict", 409, "conflict")
		}
		if err := tx.Model(&orm.LocalWorkspace{}).Where("id = ?", workspaceID).
			Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error; err != nil {
			return nil, 0, err
		}
	}
	if !exists && groupID != "" {
		if err := conversationgroup.AttachNewConversation(ctx, tx, userID, convID, groupID); err != nil {
			return nil, 0, err
		}
	}
	return conversation, seq, nil
}

func mergeWorkspaceContextIntoExt(raw json.RawMessage, snapshot *localworkspace.ContextSnapshot) json.RawMessage {
	if snapshot == nil {
		return raw
	}
	ext := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &ext)
	}
	ext["workspace_context"] = map[string]any{
		"workspace_id":       snapshot.WorkspaceID,
		"workspace_version":  snapshot.WorkspaceVersion,
		"permission_mode":    snapshot.PermissionMode,
		"permission_version": snapshot.PermissionVersion,
	}
	body, err := json.Marshal(ext)
	if err != nil {
		return raw
	}
	return body
}
