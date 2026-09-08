package localworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/mux"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

func requireWorkspaceReason(t *testing.T, err error, status int, key, reason string) {
	t.Helper()
	var app *common.AppError
	if !errors.As(err, &app) {
		t.Fatalf("want AppError %s, got %v", reason, err)
	}
	want := common.ResolveAppError(key, status)
	detail, ok := app.Detail.(map[string]any)
	if app.HTTPStatus != status || app.Code != want.Code || !ok || detail["reason"] != reason {
		t.Fatalf("want HTTP=%d code=%d reason=%s, got %+v", status, want.Code, reason, app)
	}
}

func TestWorkspaceEnabledUsesOfficialRuntime(t *testing.T) {
	for _, mode := range []string{"local", "cloud", ""} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("LAZYMIND_RUNTIME_MODE", mode)
			t.Setenv("LAZYMIND_LOCAL_WORKSPACE_RUNTIME", "desktop")
			if got := Enabled(); got != (mode == "local") {
				t.Fatalf("Enabled=%v for runtime %q", got, mode)
			}
		})
	}
}

func workspaceFixture(t *testing.T) (*orm.DB, PublicWorkspace) {
	t.Helper()
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_LOCAL_WORKSPACE_RUNTIME", "")
	db := orm.MigrateAllModelsForTest(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := currentDirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := Register(context.Background(), db.DB, "owner", RegisterInput{
		DisplayName: "project", CanonicalPath: root, DirectoryIdentity: identity, Source: "desktop",
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, grant
}

func TestWorkspaceRegisterAndResolveAreOwnerScoped(t *testing.T) {
	db, grant := workspaceFixture(t)
	if grant.WorkspaceID == "" || grant.Status != "active" || grant.Version != 1 {
		t.Fatalf("invalid grant: %+v", grant)
	}
	if _, err := ResolveActiveForBinding(context.Background(), db.DB, "owner", grant.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveActiveForBinding(context.Background(), db.DB, "other", grant.WorkspaceID)
	requireWorkspaceReason(t, err, 404, "resource not found", "workspace_not_found")
}

func TestWorkspaceResolveNeverFallsBackFromRevokedBinding(t *testing.T) {
	db, grant := workspaceFixture(t)
	now := time.Now().UTC()
	conv := orm.Conversation{ID: "task", IsTaskConv: true, BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	binding := orm.ConversationWorkspaceBinding{ConversationID: conv.ID, WorkspaceID: grant.WorkspaceID, PermissionMode: "always_ask", PermissionVersion: 2, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snapshot, err := ResolveForConversation(ctx, db.DB, "owner", conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.WorkspaceID != grant.WorkspaceID || snapshot.PermissionMode != "always_ask" || snapshot.PermissionVersion != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	_, err = ResolveForConversation(ctx, db.DB, "other", conv.ID)
	if err == nil {
		t.Fatal("cross-owner conversation accepted")
	}
	if err := db.Model(&orm.LocalWorkspace{}).Where("id = ?", grant.WorkspaceID).Updates(map[string]any{"status": "revoked", "version": 2, "revoked_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err = ResolveForConversation(ctx, db.DB, "owner", conv.ID)
	requireWorkspaceReason(t, err, 409, "conflict", "revoked")
	if snapshot != nil {
		t.Fatal("revoked binding returned a snapshot")
	}
	identity, err := currentDirectoryIdentity(grant.Path)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := Register(ctx, db.DB, "owner", RegisterInput{DisplayName: "project", CanonicalPath: grant.Path, DirectoryIdentity: identity, Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.WorkspaceID == grant.WorkspaceID {
		t.Fatal("reauthorization revived the old grant")
	}
	_, err = ResolveForConversation(ctx, db.DB, "owner", conv.ID)
	requireWorkspaceReason(t, err, 409, "conflict", "revoked")
}

func TestWorkspaceDraftResolutionDoesNotPersistBinding(t *testing.T) {
	db, grant := workspaceFixture(t)
	for _, mode := range []string{"always_ask", "ask_as_needed", "allow_all"} {
		snapshot, err := ResolveForDraft(context.Background(), db.DB, "owner", grant.WorkspaceID, mode)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot == nil || snapshot.PermissionMode != mode {
			t.Fatalf("mode %s snapshot=%+v", mode, snapshot)
		}
	}
	for _, table := range []string{"conversations", "conversation_workspace_bindings"} {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("preview created %d rows in %s", count, table)
		}
	}
}

func TestWorkspaceErrorUsesExistingCatalog(t *testing.T) {
	for _, tc := range []struct {
		reason string
		status int
		key    string
	}{
		{"mode_forbidden", 403, "forbidden"}, {"invalid_selection", 400, "invalid request"},
		{"binding_conflict", 409, "conflict"}, {"workspace_not_found", 404, "resource not found"},
		{"picker_unavailable", 503, "service unavailable"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			requireWorkspaceReason(t, Error(tc.reason, tc.status, tc.key), tc.status, tc.key, tc.reason)
		})
	}
}

func TestWorkspaceRevokeRejectsStaleVersionAndOtherOwner(t *testing.T) {
	db, grant := workspaceFixture(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, tc := range []struct {
		user, body  string
		status      int
		key, reason string
	}{
		{"other", `{"version":1}`, 404, "resource not found", "workspace_not_found"},
		{"owner", `{"version":2}`, 409, "conflict", "binding_conflict"},
	} {
		request := httptest.NewRequest(http.MethodPost, "/local-workspaces/"+grant.WorkspaceID+":revoke", strings.NewReader(tc.body))
		request.Header.Set("X-User-Id", tc.user)
		request = mux.SetURLVars(request, map[string]string{"workspace_id": grant.WorkspaceID})
		response := httptest.NewRecorder()
		Revoke(response, request)
		var envelope struct {
			Code int
			Data struct{ Detail map[string]any }
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if response.Code != tc.status || envelope.Code != common.ResolveAppError(tc.key, tc.status).Code || envelope.Data.Detail["reason"] != tc.reason {
			t.Fatalf("revoke response=%d %s", response.Code, response.Body.String())
		}
		row, err := ResolveActiveForBinding(context.Background(), db.DB, "owner", grant.WorkspaceID)
		if err != nil || row.Version != 1 {
			t.Fatalf("rejected revoke changed grant: %+v %v", row, err)
		}
	}
}

func TestWorkspaceDirectoryReplacementInvalidatesGrant(t *testing.T) {
	db, grant := workspaceFixture(t)
	// Keep the old directory alive so the filesystem cannot reuse its identity.
	moved := grant.Path + "-old"
	if err := os.Rename(grant.Path, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	if err := os.Mkdir(grant.Path, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveActiveForBinding(context.Background(), db.DB, "owner", grant.WorkspaceID)
	requireWorkspaceReason(t, err, 409, "conflict", "path_unavailable")
}

func TestWorkspacePermissionUpdateUsesSavedVersion(t *testing.T) {
	db, grant := workspaceFixture(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	conv := orm.Conversation{ID: "permission-task", IsTaskConv: true, BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	binding := orm.ConversationWorkspaceBinding{ConversationID: conv.ID, WorkspaceID: grant.WorkspaceID, PermissionMode: "always_ask", PermissionVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	update := func(user, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/conversations/"+conv.ID+":workspace-permission", strings.NewReader(body))
		req.Header.Set("X-User-Id", user)
		req = mux.SetURLVars(req, map[string]string{"conversation_id": conv.ID})
		response := httptest.NewRecorder()
		UpdateConversationPermission(response, req)
		return response
	}
	denied := update("other", `{"permission_mode":"allow_all","version":1}`)
	if denied.Code != 404 {
		t.Fatalf("cross-owner update=%d %s", denied.Code, denied.Body.String())
	}
	response := update("owner", `{"permission_mode":"ask_as_needed","version":1}`)
	var envelope struct {
		Code int
		Data struct {
			PermissionMode    string `json:"permission_mode"`
			PermissionVersion int64  `json:"permission_version"`
			EffectiveAt       string `json:"effective_at"`
		}
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || envelope.Code != 0 || envelope.Data.PermissionMode != "ask_as_needed" || envelope.Data.PermissionVersion != 2 || envelope.Data.EffectiveAt != "next_request" {
		t.Fatalf("update=%d %s", response.Code, response.Body.String())
	}
	stale := update("owner", `{"permission_mode":"allow_all","version":1}`)
	var conflict struct {
		Code int
		Data struct{ Detail map[string]any }
	}
	if err := json.Unmarshal(stale.Body.Bytes(), &conflict); err != nil {
		t.Fatal(err)
	}
	if stale.Code != 409 || conflict.Code != common.ResolveAppError("conflict", 409).Code || conflict.Data.Detail["reason"] != "binding_conflict" {
		t.Fatalf("stale update=%d %s", stale.Code, stale.Body.String())
	}
	var saved orm.ConversationWorkspaceBinding
	if err := db.Where("conversation_id = ?", conv.ID).First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.PermissionMode != "ask_as_needed" || saved.PermissionVersion != 2 {
		t.Fatalf("stale request changed binding: %+v", saved)
	}
	if err := db.Model(&orm.LocalWorkspace{}).Where("id = ?", grant.WorkspaceID).Updates(map[string]any{"status": "revoked", "version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	rejected := update("owner", `{"permission_mode":"allow_all","version":2}`)
	if rejected.Code != 409 {
		t.Fatalf("revoked update=%d %s", rejected.Code, rejected.Body.String())
	}
	if err := db.Where("conversation_id = ?", conv.ID).First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.PermissionVersion != 2 {
		t.Fatalf("revoked request changed version: %+v", saved)
	}
}
