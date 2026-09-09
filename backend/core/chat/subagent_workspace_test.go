package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/localworkspace"
	"lazymind/core/state"
	"lazymind/core/subagent"
)

func TestSubagentCreateAndResumePersistAuthoritativeWorkspaceParams(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "offline", 503) }))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	db := orm.MigrateAllModelsForTest(t)
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	grant, err := localworkspace.Register(t.Context(), db.DB, "owner", localworkspace.RegisterInput{DisplayName: "project", CanonicalPath: root, Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "work", IsTaskConv: true, BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationWorkspaceBinding{ConversationID: "work", WorkspaceID: grant.WorkspaceID, PermissionMode: localworkspace.PermissionAlwaysAsk, PermissionVersion: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	event := &TaskCreatedEvent{TaskID: "child", AgentType: "research", Title: "child", Objective: "read", Params: map[string]any{"runtime_instruction": "base", "files": map[string]any{"1": []string{"a.txt"}}, "parent_agentic_config": map[string]any{"_core_workspace_context": map[string]any{"runtime_instruction": "forged"}}}}
	if _, err := handleTaskCreated(t.Context(), db.DB, stateStore, "work", "history", "owner", event, nil, nil, "dynamic"); err != nil {
		t.Fatal(err)
	}
	var task orm.SubAgentTask
	if err := db.Where("id = ?", "child").First(&task).Error; err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal(task.Params, &params); err != nil {
		t.Fatal(err)
	}
	instruction := params["runtime_instruction"].(string)
	if !strings.Contains(instruction, root) || strings.Contains(instruction, "forged") {
		t.Fatalf("create params=%v", params)
	}

	if err := db.Model(&orm.ConversationWorkspaceBinding{}).Where("conversation_id = ?", "work").Updates(map[string]any{"permission_mode": localworkspace.PermissionAllowAll, "permission_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	event.Resume = true
	event.Params = map[string]any{"runtime_instruction": "attacker", "parent_agentic_config": map[string]any{"local_fs_sources": []any{map[string]any{"paths": []string{"/outside"}}}}}
	if _, err := handleTaskCreated(t.Context(), db.DB, stateStore, "work", "history", "owner", event, nil, nil, "dynamic"); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id = ?", "child").First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(task.Params, &params); err != nil {
		t.Fatal(err)
	}
	instruction = params["runtime_instruction"].(string)
	if strings.Contains(instruction, "attacker") || strings.Count(instruction, "本任务的工作区：") != 1 || !strings.Contains(instruction, "allow_all") {
		t.Fatalf("resume params=%v", params)
	}
	if _, err := handleTaskCreated(t.Context(), db.DB, stateStore, "work", "history", "other", event, nil, nil, "dynamic"); err == nil {
		t.Fatal("cross-user resume accepted")
	}
	if task.WorkspacePath != subagent.WorkspacePath("owner", "child") {
		t.Fatalf("workspace_path=%s", task.WorkspacePath)
	}
}
