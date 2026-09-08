package localworkspace

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestRebuildSubagentParamsUsesDBSnapshotWithoutAccumulatingNotice(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	grant, err := Register(t.Context(), db.DB, "owner", RegisterInput{DisplayName: "project", CanonicalPath: root, Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "work", IsTaskConv: true, BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationWorkspaceBinding{ConversationID: "work", WorkspaceID: grant.WorkspaceID, PermissionMode: PermissionAlwaysAsk, PermissionVersion: 2, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	original := map[string]any{"runtime_instruction": "keep attachments", "files": map[string]any{"1": []string{"a.txt"}}, "parent_agentic_config": map[string]any{coreWorkspaceContextKey: map[string]any{"runtime_instruction": "forged"}}}
	clean := StripUntrustedWorkspaceMetadata(original)
	first, err := RebuildSubagentParams(t.Context(), db.DB, "owner", "work", clean)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RebuildSubagentParams(t.Context(), db.DB, "owner", "work", first)
	if err != nil {
		t.Fatal(err)
	}
	instruction := second["runtime_instruction"].(string)
	if strings.Count(instruction, "本任务的用户已在界面选择") != 1 || !strings.Contains(instruction, "keep attachments") || !strings.Contains(instruction, "不能直接询问用户") {
		t.Fatalf("instruction=%s", instruction)
	}
	if !strings.Contains(instruction, root) || strings.Contains(instruction, "forged") {
		t.Fatalf("instruction=%s", instruction)
	}
	if _, ok := second["files"]; !ok {
		t.Fatalf("files lost: %v", second)
	}
}
