package chat

import (
	"context"
	"gorm.io/gorm"
	"lazymind/core/localworkspace"
	"strings"
)

func workspaceSnapshotForRequest(ctx context.Context, db *gorm.DB, userID string, body map[string]any) (*localworkspace.ContextSnapshot, error) {
	if !localworkspace.Enabled() {
		return nil, nil
	}
	conversationID, _ := body["conversation_id"].(string)
	if strings.TrimSpace(conversationID) != "" {
		return localworkspace.ResolveForConversation(ctx, db, userID, strings.TrimSpace(conversationID))
	}
	workspaceID, _ := body["workspace_id"].(string)
	if strings.TrimSpace(workspaceID) == "" {
		return localworkspace.UnboundContext(), nil
	}
	mode, _ := body["workspace_permission_mode"].(string)
	if strings.TrimSpace(mode) == "" {
		mode = localworkspace.PermissionAskAsNeeded
	}
	return localworkspace.ResolveForDraft(ctx, db, userID, strings.TrimSpace(workspaceID), strings.TrimSpace(mode))
}

func applyWorkspaceRequestContext(ctx context.Context, db *gorm.DB, userID string, body map[string]any) (*localworkspace.ContextSnapshot, error) {
	snapshot, err := workspaceSnapshotForRequest(ctx, db, userID, body)
	if err != nil || snapshot == nil {
		return snapshot, err
	}
	delete(body, "local_fs_sources")
	body["workspace_context"] = snapshot
	query, _ := body["query"].(string)
	body["query"] = localworkspace.BuildRequestQuery(query, snapshot)
	return snapshot, nil
}
