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
	parentRuntime, _ := params["parent_agentic_config"].(map[string]any)
	parentRuntime = cloneMap(parentRuntime)
	parentRuntime["_core_local_runtime"] = Enabled()
	params["parent_agentic_config"] = parentRuntime
	params["_core_local_runtime"] = Enabled()
	if db == nil || !db.Migrator().HasTable(&orm.ConversationWorkspaceBinding{}) {
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
	body, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	metadata := map[string]any{}
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil, err
	}
	metadata["runtime_instruction"] = base
	parent[coreWorkspaceContextKey] = metadata
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
	delete(result, "_core_local_runtime")
	if parent, ok := result["parent_agentic_config"].(map[string]any); ok {
		parent = cloneMap(parent)
		delete(parent, coreWorkspaceContextKey)
		delete(parent, "_core_local_runtime")
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
