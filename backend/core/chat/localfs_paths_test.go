package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/localworkspace"
)

func TestApplyLocalFSPathsForChatAddsActiveLocalBindings(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.LocalFSChatSetting{})
	now := time.Now()
	if err := db.Create(&orm.LocalFSChatSetting{
		CreateUserID:   "u1",
		CreateUserName: "User 1",
		Enabled:        true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create setting: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-User-ID"); got != "u1" {
			t.Errorf("unexpected X-User-ID: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/scan/sources":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"source_id": "source-1", "status": "ACTIVE", "dataset_id": "ds-1", "tenant_id": ""},
					{"source_id": "source-2", "status": "ACTIVE", "dataset_id": "ds-2", "tenant_id": ""},
				},
				"total": 2,
			})
		case "/api/scan/sources/source-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"bindings": []map[string]any{
					{"binding_id": "bind-1a", "connector_type": "local_fs", "target_type": "local_path", "target_ref": "/Users/me/docs", "status": "ACTIVE", "chat_enabled": true},
					{"binding_id": "bind-1b", "connector_type": "feishu", "target_type": "wiki_node", "target_ref": "wiki:space:node", "status": "ACTIVE", "chat_enabled": true},
				},
			})
		case "/api/scan/sources/source-2":
			_ = json.NewDecoder(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"bindings": []map[string]any{
					{"binding_id": "bind-2a", "connector_type": "local_fs", "target_type": "local_path", "target_ref": "/Users/me/projects", "status": "ACTIVE", "chat_enabled": true, "include_extensions": []string{"pdf", "md", "txt", "go", "tsx", "toml"}},
					{"binding_id": "bind-2b", "connector_type": "local_fs", "target_type": "local_path", "target_ref": "/Users/me/downloads", "status": "ACTIVE", "chat_enabled": true, "include_extensions": []string{"doc", "docx"}},
					{"binding_id": "bind-2c", "connector_type": "local_fs", "target_type": "local_path", "target_ref": "/tmp/paused", "status": "PAUSED", "chat_enabled": true, "include_extensions": []string{"txt"}},
					{"binding_id": "bind-2d", "connector_type": "local_fs", "target_type": "local_path", "target_ref": "/tmp/disabled", "status": "ACTIVE", "chat_enabled": false, "include_extensions": []string{"xls"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", server.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/core/conversations:chat", nil)
	req.Header.Set("X-User-Id", "u1")
	reqBody := map[string]any{}
	if err := applyLocalFSPathsForChat(req.Context(), req, db.DB, "u1", reqBody); err != nil {
		t.Fatalf("apply local fs paths: %v", err)
	}

	got, ok := reqBody["local_fs_sources"].([]map[string]any)
	if !ok {
		t.Fatalf("local_fs_sources missing or wrong type: %#v", reqBody["local_fs_sources"])
	}
	// source-1: one local_fs binding but no include_extensions → filtered out.
	// source-2: two chat_enabled=true local_fs bindings with extensions.
	want := []map[string]any{
		{"source_id": "source-2", "paths": []string{"/Users/me/projects", "/Users/me/downloads"}, "file_extensions": []string{"doc", "docx", "go", "md", "pdf", "toml", "tsx", "txt"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("local_fs_sources = %#v, want %#v", got, want)
	}
}

func TestApplyLocalFSPathsForChatFiltersByBindingChatEnabled(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.LocalFSChatSetting{})
	now := time.Now()
	if err := db.Create(&orm.LocalFSChatSetting{
		CreateUserID:   "u1",
		CreateUserName: "User 1",
		Enabled:        true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("create setting: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/scan/sources":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"source_id": "source-1", "status": "ACTIVE", "dataset_id": "ds-1", "tenant_id": ""},
				},
				"total": 1,
			})
		case "/api/scan/sources/source-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"bindings": []map[string]any{
					{"binding_id": "bind-1", "connector_type": "local_fs", "target_type": "local_path", "target_ref": "/container/enabled", "status": "ACTIVE", "chat_enabled": true, "include_extensions": []string{"pdf"}},
					{"binding_id": "bind-2", "connector_type": "local_fs", "target_type": "local_path", "target_ref": "/container/disabled", "status": "ACTIVE", "chat_enabled": false, "include_extensions": []string{"csv"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", server.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/core/conversations:chat", nil)
	req.Header.Set("X-User-Id", "u1")
	reqBody := map[string]any{}
	if err := applyLocalFSPathsForChat(req.Context(), req, db.DB, "u1", reqBody); err != nil {
		t.Fatalf("apply local fs paths: %v", err)
	}

	got, ok := reqBody["local_fs_sources"].([]map[string]any)
	if !ok {
		t.Fatalf("local_fs_sources missing or wrong type: %#v", reqBody)
	}
	// Only bind-1 (chat_enabled=true) should appear, with its target_ref as path.
	if len(got) != 1 {
		t.Fatalf("expected 1 source, got %d: %#v", len(got), got)
	}
	entry := got[0]
	if entry["source_id"] != "source-1" {
		t.Fatalf("expected source-1, got %v", entry["source_id"])
	}
	paths := entry["paths"].([]string)
	if len(paths) != 1 || paths[0] != "/container/enabled" {
		t.Fatalf("unexpected paths: %#v, want [/container/enabled]", paths)
	}
	exts := entry["file_extensions"].([]string)
	if len(exts) != 1 || exts[0] != "pdf" {
		t.Fatalf("unexpected file_extensions: %#v, want [pdf]", exts)
	}
}

func TestApplyLocalFSPathsForBoundWorkSkipsGlobalScan(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	grant, err := localworkspace.Register(t.Context(), db.DB, "u1", localworkspace.RegisterInput{
		DisplayName: "project", CanonicalPath: root, Source: "desktop",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "work", IsTaskConv: true,
		BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationWorkspaceBinding{ConversationID: "work", WorkspaceID: grant.WorkspaceID,
		PermissionMode: localworkspace.PermissionAlwaysAsk, PermissionVersion: 2, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	var scans int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { scans++; http.Error(w, "must not scan", 500) }))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", server.URL)
	request := httptest.NewRequest(http.MethodPost, "/conversations:chat", nil)
	body := map[string]any{"conversation_id": "work", "query": "read notes", "user_query": "read notes"}
	if err := applyLocalFSPathsForChat(request.Context(), request, db.DB, "u1", body); err != nil {
		t.Fatal(err)
	}
	if scans != 0 {
		t.Fatalf("bound Work scanned %d global sources", scans)
	}
	sources, ok := body["local_fs_sources"].([]map[string]any)
	if !ok || len(sources) != 1 || sources[0]["source_id"] != "local-workspace:"+grant.WorkspaceID {
		t.Fatalf("sources=%T %v", body["local_fs_sources"], body["local_fs_sources"])
	}
	snapshot, err := applyWorkspaceRequestContext(request.Context(), db.DB, "u1", body)
	if err != nil || snapshot == nil {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if body["user_query"] != "read notes" || !strings.Contains(body["query"].(string), root) {
		t.Fatalf("body=%v", body)
	}
}

func TestWorkspaceDraftSnapshotRequiresBackgroundAndDoesNotPersist(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	grant, err := localworkspace.Register(t.Context(), db.DB, "u1", localworkspace.RegisterInput{
		DisplayName: "draft", CanonicalPath: root, Source: "desktop",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"workspace_id": grant.WorkspaceID,
		"workspace_permission_mode": localworkspace.PermissionAllowAll, "run_in_background": true, "query": "draft"}
	snapshot, err := workspaceSnapshotForRequest(t.Context(), db.DB, "u1", body)
	if err != nil || snapshot == nil || snapshot.PermissionMode != localworkspace.PermissionAllowAll {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	var count int64
	if err := db.Table("conversation_workspace_bindings").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("draft persisted binding count=%d err=%v", count, err)
	}
	body["run_in_background"] = false
	if _, err := workspaceSnapshotForRequest(t.Context(), db.DB, "u1", body); err == nil {
		t.Fatal("Chat draft accepted workspace")
	}
}
