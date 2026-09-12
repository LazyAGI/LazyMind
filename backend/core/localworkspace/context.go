package localworkspace

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

type ContextSnapshot struct {
	WorkspaceID       string           `json:"workspace_id"`
	Root              string           `json:"root,omitempty"`
	DirectoryIdentity string           `json:"directory_identity,omitempty"`
	WorkspaceVersion  int64            `json:"workspace_version"`
	PermissionMode    string           `json:"permission_mode"`
	PermissionVersion int64            `json:"permission_version"`
	Sources           []map[string]any `json:"sources,omitempty"`
}

func SnapshotFromMetadata(value any) *ContextSnapshot {
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var snapshot ContextSnapshot
	if json.Unmarshal(body, &snapshot) != nil || snapshot.WorkspaceID == "" || snapshot.WorkspaceVersion < 1 ||
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
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !conversation.IsTaskConv {
		return nil, ModeError()
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
		DirectoryIdentity: workspace.DirectoryIdentity, WorkspaceVersion: workspace.Version,
		PermissionMode: mode, PermissionVersion: version,
		Sources: []map[string]any{{"source_id": "local-workspace:" + workspace.ID,
			"paths": []string{workspace.CanonicalPath}, "file_extensions": common.TextFileExtensions()}},
	}
}

func ModelNotice(snapshot ContextSnapshot) string {
	data, _ := json.Marshal(map[string]any{"root": snapshot.Root,
		"permission_mode": snapshot.PermissionMode})
	return "本任务的工作区：" + string(data) +
		"\n相对路径以工作区为基准。使用 local_fs 读取、创建、修改、追加或删除文件；Core 负责权限检查，需要批准时整批工具会等待用户决定，然后由本地文件工具执行。" +
		"\n工作区外的绝对路径需要用户批准本次操作。只根据工具实际结果报告成功。拒绝、冲突或结果未知时说明原因，不使用其他工具绕过。"
}

func BuildRequestQuery(original string, snapshot *ContextSnapshot) string {
	if snapshot == nil {
		return original
	}
	return ModelNotice(*snapshot) + "\n\n" + original
}
