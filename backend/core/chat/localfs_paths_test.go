package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestApplyLocalFSPathsForChatAddsActiveLocalBindings(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/localfs-chat.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.LocalFSChatSetting{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
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
		if got := r.Header.Get("X-Tenant-ID"); got != defaultScanTenantID {
			t.Errorf("unexpected X-Tenant-ID: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/scan/sources":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{
					{"source_id": "source-1", "status": "ACTIVE"},
					{"source_id": "source-2", "status": "ACTIVE"},
				},
				"total": 2,
			})
		case "/api/scan/sources/source-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"bindings": []map[string]any{
					{"connector_type": "local_fs", "target_type": "local_path", "target_ref": "/container/a", "status": "ACTIVE"},
					{"connector_type": "feishu", "target_type": "wiki_node", "target_ref": "wiki:space:node", "status": "ACTIVE"},
				},
			})
		case "/api/scan/sources/source-2":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"bindings": []map[string]any{
					{"connector_type": "local_fs", "target_type": "local_path", "target_ref": "/container/a", "status": "ACTIVE"},
					{"connector_type": "local_fs", "target_type": "local_path", "target_ref": "/container/b", "status": "ACTIVE"},
					{"connector_type": "local_fs", "target_type": "local_path", "target_ref": "/container/paused", "status": "PAUSED"},
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

	got, ok := reqBody["localfs_paths"].([]string)
	if !ok {
		t.Fatalf("localfs_paths missing or wrong type: %#v", reqBody["localfs_paths"])
	}
	want := []string{"/container/a", "/container/b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("localfs_paths = %#v, want %#v", got, want)
	}
}

func TestApplyLocalFSPathsForChatDisabledOmitsParameter(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/localfs-chat-disabled.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.LocalFSChatSetting{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/core/conversations:chat", nil)
	req.Header.Set("X-User-Id", "u1")
	reqBody := map[string]any{}
	if err := applyLocalFSPathsForChat(req.Context(), req, db.DB, "u1", reqBody); err != nil {
		t.Fatalf("apply local fs paths: %v", err)
	}
	if _, ok := reqBody["localfs_paths"]; ok {
		t.Fatalf("localfs_paths should be omitted when setting is disabled: %#v", reqBody)
	}
}
