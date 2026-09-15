package localworkspace

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type ContextSnapshot struct {
	OpaqueToolGrants  []string `json:"opaque_tool_grants,omitempty"`
	WorkspaceID       string   `json:"workspace_id"`
	Root              string   `json:"root,omitempty"`
	DirectoryIdentity string   `json:"directory_identity,omitempty"`
	WorkspaceVersion  int64    `json:"workspace_version"`
	PermissionMode    string   `json:"permission_mode"`
	PermissionVersion int64    `json:"permission_version"`
}

// UnboundContext is a Core-issued snapshot; it never grants implicit write access.
func UnboundContext() *ContextSnapshot {
	if !Enabled() {
		return nil
	}
	return &ContextSnapshot{PermissionMode: PermissionAlwaysAsk, PermissionVersion: 1}
}

func SnapshotFromMetadata(value any) *ContextSnapshot {
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var snapshot ContextSnapshot
	if json.Unmarshal(body, &snapshot) != nil || (snapshot.WorkspaceID != "" && snapshot.WorkspaceVersion < 1) ||
		(snapshot.WorkspaceID == "" && (snapshot.WorkspaceVersion != 0 || snapshot.PermissionMode != PermissionAlwaysAsk || snapshot.Root != "")) ||
		!ValidPermissionMode(snapshot.PermissionMode) || snapshot.PermissionVersion < 1 {
		return nil
	}
	return &snapshot
}

func SnapshotFromParams(params map[string]any) *ContextSnapshot {
	parent, _ := params["parent_agentic_config"].(map[string]any)
	return SnapshotFromMetadata(parent[coreWorkspaceContextKey])
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
	var binding orm.ConversationWorkspaceBinding
	err = db.WithContext(ctx).Where("conversation_id = ?", conversationID).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return withToolGrants(ctx, db, userID, conversationID, UnboundContext())
	}
	if err != nil {
		return nil, err
	}
	workspace, err := ResolveActiveForBinding(ctx, db, userID, binding.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return withToolGrants(ctx, db, userID, conversationID, snapshot(workspace, binding.PermissionMode, binding.PermissionVersion))
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
		DirectoryIdentity: workspace.DirectoryIdentity, WorkspaceVersion: workspace.Version,
		PermissionMode: mode, PermissionVersion: version,
	}
}

func ModelNotice(snapshot ContextSnapshot) string {
	data, _ := json.Marshal(map[string]any{"root": snapshot.Root,
		"permission_mode": snapshot.PermissionMode})
	return "本任务的工作区：" + string(data) +
		"\n相对路径以工作区为基准。使用 read/write/edit/ls/glob/grep/mkdir/move/remove/stat 操作文件；权限由工作区策略决定，需要批准时等待用户决定后再执行。" +
		"\n读取放行；写入和删除按权限模式审批，未绑定工作区时默认逐次审批。只根据工具实际结果报告成功。拒绝、冲突或结果未知时说明原因，不使用其他工具绕过。"
}

func BuildRequestQuery(original string, snapshot *ContextSnapshot) string {
	if snapshot == nil {
		return original
	}
	return ModelNotice(*snapshot) + "\n\n" + original
}

func withToolGrants(ctx context.Context, db *gorm.DB, userID, conversationID string, value *ContextSnapshot) (*ContextSnapshot, error) {
	if value == nil {
		return nil, nil
	}
	var grants []string
	if err := db.WithContext(ctx).Model(&orm.ConversationToolGrant{}).
		Where("conversation_id = ? AND create_user_id = ?", conversationID, userID).
		Pluck("capability", &grants).Error; err != nil {
		return nil, err
	}
	value.OpaqueToolGrants = grants
	return value, nil
}
