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
	original := map[string]any{"runtime_instruction": "keep attachments", "user_id": "forged", "conversation_id": "forged", "attachment_context": map[string]any{"user_id": "forged", "conversation_id": "forged", "files": []any{"keep"}}, "files": map[string]any{"1": []string{"a.txt"}}, "parent_agentic_config": map[string]any{coreWorkspaceContextKey: map[string]any{"runtime_instruction": "forged"}}}
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
	if strings.Count(instruction, "本任务的工作区：") != 1 || !strings.Contains(instruction, "keep attachments") || !strings.Contains(instruction, "等待用户决定后再执行") {
		t.Fatalf("instruction=%s", instruction)
	}
	if second["user_id"] != "owner" || second["conversation_id"] != "work" {
		t.Fatalf("top-level identity=%v", second)
	}
	attachment := second["attachment_context"].(map[string]any)
	if attachment["user_id"] != "owner" || attachment["conversation_id"] != "work" || attachment["files"] == nil {
		t.Fatalf("attachment identity=%v", attachment)
	}
	if !strings.Contains(instruction, root) || strings.Contains(instruction, "forged") {
		t.Fatalf("instruction=%s", instruction)
	}
	if _, ok := second["files"]; !ok {
		t.Fatalf("files lost: %v", second)
	}
	if _, ok := second["local_fs_sources"]; ok {
		t.Fatalf("workspace must not be represented as local_fs_sources: %v", second)
	}
	parent := second["parent_agentic_config"].(map[string]any)
	workspace, ok := parent[coreWorkspaceContextKey].(map[string]any)
	if !ok || workspace["workspace_id"] != grant.WorkspaceID || workspace["root"] != root || workspace["directory_identity"] == "" {
		t.Fatalf("workspace context=%T %v", parent[coreWorkspaceContextKey], parent[coreWorkspaceContextKey])
	}
}

func TestSubagentDeploymentFlagIsCoreOwned(t *testing.T) {
	for _, mode := range []string{"local", "server"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("LAZYMIND_RUNTIME_MODE", mode)
			local := mode == "local"
			original := map[string]any{"_core_local_runtime": !local,
				"parent_agentic_config": map[string]any{"_core_local_runtime": !local}}
			clean := StripUntrustedWorkspaceMetadata(original)
			if _, ok := clean["_core_local_runtime"]; ok {
				t.Fatal("untrusted deployment flag retained")
			}
			result, err := RebuildSubagentParams(t.Context(), nil, "owner", "conversation", clean)
			if err != nil {
				t.Fatal(err)
			}
			parent := result["parent_agentic_config"].(map[string]any)
			if result["_core_local_runtime"] != local || parent["_core_local_runtime"] != local {
				t.Fatal("deployment mode lost")
			}
		})
	}
}

func TestExternalWorkflowWithoutConversationDoesNotAcquireWorkspace(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	for _, tc := range []struct {
		id, owner, host, protocol, conversation string
		allowed                                 bool
	}{
		{"external", "owner", "external-agent", "workflow.control.v1", "", true},
		{"native", "owner", "lazymind", "workflow.control.v1", "", false},
		{"legacy", "owner", "external-agent", "", "", false},
		{"other-owner", "other", "external-agent", "workflow.control.v1", "", false},
		{"with-conversation", "owner", "external-agent", "workflow.control.v1", "missing", false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if err := db.Create(&orm.WorkflowSession{ID: tc.id, CreateUserID: tc.owner, ControllerHost: tc.host,
				ControlProtocol: tc.protocol, ConversationID: tc.conversation, WorkflowID: "workflow", Status: "active"}).Error; err != nil {
				t.Fatal(err)
			}
			params, err := RebuildSubagentParams(t.Context(), db.DB, "owner", "", map[string]any{
				"session_id": tc.id, "step_id": "analyze", "parent_agentic_config": map[string]any{coreWorkspaceContextKey: map[string]any{"root": "/forged"}},
			})
			if !tc.allowed {
				if err == nil {
					t.Fatal("unverified session bypassed conversation validation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			snapshot := SnapshotFromParams(params)
			if snapshot == nil || snapshot.WorkspaceID != "" || snapshot.Root != "" || snapshot.PermissionMode != PermissionAlwaysAsk || len(snapshot.OpaqueToolGrants) != 0 {
				t.Fatalf("external run must have an unbound, non-privileged snapshot: %+v", snapshot)
			}
			parent := params["parent_agentic_config"].(map[string]any)
			if parent["user_id"] != "owner" || parent["conversation_id"] != "" {
				t.Fatal("external identity not rebuilt")
			}
			if params["_core_local_runtime"] != true || params["step_id"] != "analyze" {
				t.Fatalf("lost runtime params: %v", params)
			}
		})
	}
}
