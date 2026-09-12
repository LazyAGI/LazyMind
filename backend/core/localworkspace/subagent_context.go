package localworkspace

import (
	"context"
	"encoding/json"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

const coreWorkspaceContextKey = "_core_workspace_context"

func RebuildSubagentParams(ctx context.Context, db *gorm.DB, userID, conversationID string, original map[string]any) (map[string]any, error) {
	params := cloneMap(original)
	if db == nil || !db.Migrator().HasTable(&orm.ConversationWorkspaceBinding{}) {
		return params, nil
	}
	var bindingCount int64
	if err := db.WithContext(ctx).Model(&orm.ConversationWorkspaceBinding{}).
		Where("conversation_id = ?", conversationID).Count(&bindingCount).Error; err != nil {
		return nil, err
	}
	if bindingCount == 0 {
		return params, nil
	}
	snapshot, err := ResolveForConversation(ctx, db, userID, conversationID)
	if err != nil || snapshot == nil {
		return params, err
	}
	parent, _ := params["parent_agentic_config"].(map[string]any)
	parent = cloneMap(parent)
	base, _ := params["runtime_instruction"].(string)
	if metadata, ok := parent[coreWorkspaceContextKey].(map[string]any); ok {
		if saved, ok := metadata["runtime_instruction"].(string); ok {
			base = saved
		}
	}
	parent["user_id"] = userID
	parent["conversation_id"] = conversationID
	parent["local_fs_sources"] = snapshot.Sources
	parent[coreWorkspaceContextKey] = map[string]any{
		"runtime_instruction": base, "workspace_id": snapshot.WorkspaceID,
		"workspace_version": snapshot.WorkspaceVersion, "permission_mode": snapshot.PermissionMode,
		"permission_version": snapshot.PermissionVersion,
	}
	params["user_id"] = userID
	params["conversation_id"] = conversationID
	attachment, _ := params["attachment_context"].(map[string]any)
	attachment = cloneMap(attachment)
	attachment["user_id"] = userID
	attachment["conversation_id"] = conversationID
	params["attachment_context"] = attachment
	params["parent_agentic_config"] = parent
	notice := ModelNotice(*snapshot)
	if strings.TrimSpace(base) == "" {
		params["runtime_instruction"] = notice
	} else {
		params["runtime_instruction"] = base + "\n\n" + notice
	}
	return params, nil
}

func StripUntrustedWorkspaceMetadata(params map[string]any) map[string]any {
	result := cloneMap(params)
	if parent, ok := result["parent_agentic_config"].(map[string]any); ok {
		parent = cloneMap(parent)
		delete(parent, coreWorkspaceContextKey)
		result["parent_agentic_config"] = parent
	}
	return result
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	body, _ := json.Marshal(input)
	result := map[string]any{}
	_ = json.Unmarshal(body, &result)
	return result
}
