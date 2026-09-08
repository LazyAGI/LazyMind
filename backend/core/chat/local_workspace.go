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
	runInBackground, _ := raw["run_in_background"].(bool)
	if !runInBackground || !localworkspace.Enabled() {
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
	workspaceID, workspacePresent := requestedWorkspaceID(raw)
	permissionMode, _ := requestedWorkspacePermissionMode(raw)
	var conversation *orm.Conversation
	var seq int
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing orm.Conversation
		existingErr := tx.Where("id = ? AND create_user_id = ?", convID, userID).First(&existing).Error
		exists := existingErr == nil
		if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}
		if exists && workspacePresent {
			if !existing.IsTaskConv {
				return localworkspace.ModeError()
			}
			var binding orm.ConversationWorkspaceBinding
			err := tx.Where("conversation_id = ?", convID).First(&binding).Error
			if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && binding.WorkspaceID != workspaceID) {
				return localworkspace.Error("binding_locked", 409, "conflict")
			}
			if err != nil {
				return err
			}
		}
		if !exists && workspacePresent {
			if _, err := localworkspace.ResolveActiveForBinding(ctx, tx, userID, workspaceID); err != nil {
				return err
			}
		}
		created, nextSeq, err := ensureConversation(ctx, tx, convID, displayName,
			searchConfig, models, userID, userName, runInBackground,
			requestedThinkingDepth, conversationSettings, initialModelSelection)
		if err != nil {
			return err
		}
		conversation, seq = created, nextSeq
		if !exists && runInBackground {
			if err := tx.Model(&orm.Conversation{}).Where("id = ?", convID).Update("is_task_conv", true).Error; err != nil {
				return err
			}
			conversation.IsTaskConv = true
		}
		if !exists && workspacePresent {
			now := time.Now().UTC()
			binding := orm.ConversationWorkspaceBinding{ConversationID: convID, WorkspaceID: workspaceID,
				PermissionMode: permissionMode, PermissionVersion: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&binding).Error; err != nil {
				return localworkspace.Error("binding_conflict", 409, "conflict")
			}
			return tx.Model(&orm.LocalWorkspace{}).Where("id = ?", workspaceID).
				Updates(map[string]any{"last_used_at": now, "updated_at": now}).Error
		}
		return nil
	})
	return conversation, seq, err
}
