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
	return snapshotForValues(workspace.ID, workspace.CanonicalPath, workspace.Version, mode, version)
}

func snapshotForValues(id, root string, workspaceVersion int64, mode string, permissionVersion int64) *ContextSnapshot {
	return &ContextSnapshot{WorkspaceID: id, Root: root, WorkspaceVersion: workspaceVersion,
		PermissionMode: mode, PermissionVersion: permissionVersion,
		Sources: []map[string]any{{"source_id": "local-workspace:" + id,
			"paths": []string{root}, "file_extensions": common.TextFileExtensions()}},
	}
}

func BuildRequestQuery(original string, snapshot *ContextSnapshot) string {
	if snapshot == nil {
		return original
	}
	data, _ := json.Marshal(map[string]any{"root": snapshot.Root,
		"permission_mode": snapshot.PermissionMode, "permission_version": snapshot.PermissionVersion})
	rule := "文件修改、命令、联网及应用副作用按当前权限规则执行。"
	if snapshot.PermissionMode == PermissionAlwaysAsk {
		rule = "对文件修改、命令、联网及应用副作用先使用已有 ask_user 询问并等待用户回答。"
	} else if snapshot.PermissionMode == PermissionAllowAll {
		rule = "用户已允许本任务在工作区内执行操作；仍不得访问工作区外目录。"
	}
	notice := "本任务的用户已在界面选择并授权以下本地工作区。\n工作区数据：" + string(data) +
		"\n用户请求中的相对本地文件路径以该目录为基准。优先使用现有 local_fs 工具列出、搜索、读取和精确修改匹配类型的文件。" +
		"\n创建、覆盖或追加文件只使用当前运行环境实际提供的能力；工具拒绝时说明原因。\n工作区之外的目录不在本任务授权范围。\n权限规则：" + rule
	return notice + "\n\n" + original
}
