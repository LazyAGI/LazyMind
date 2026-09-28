package localworkspace

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestUnboundPermissionPersistsUserDefaultAndClearsGrants(t *testing.T) {
	db, _ := workspaceFixture(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, id := range []string{"current", "other"} {
		if err := db.Create(&orm.Conversation{ID: id, PermissionMode: PermissionAlwaysAsk, PermissionVersion: 1, BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	update := func(mode string, version, userVersion int) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/", strings.NewReader(fmt.Sprintf(`{"permission_mode":%q,"version":%d,"user_permission_version":%d}`, mode, version, userVersion)))
		req.Header.Set("X-User-Id", "owner")
		req = mux.SetURLVars(req, map[string]string{"conversation_id": "current"})
		w := httptest.NewRecorder()
		UpdateConversationPermission(w, req)
		return w
	}
	for i, mode := range []string{PermissionAskAsNeeded, PermissionAllowAll, PermissionAlwaysAsk} {
		if err := db.Create(&orm.ConversationToolGrant{ConversationID: "current", Capability: "shell", CreateUserID: "owner"}).Error; err != nil {
			t.Fatal(err)
		}
		w := update(mode, i+1, i+1)
		if w.Code != 200 {
			t.Fatalf("update: %d %s", w.Code, w.Body.String())
		}
		snapshot, err := ResolveForConversation(t.Context(), db.DB, "owner", "current")
		if err != nil || snapshot.PermissionMode != mode || snapshot.WorkspaceID != "" || len(snapshot.OpaqueToolGrants) != 0 {
			t.Fatalf("snapshot: %+v %v", snapshot, err)
		}
		if SnapshotFromMetadata(snapshot) == nil {
			t.Fatal("unbound mode rejected")
		}
		pref, v, err := UserPermission(t.Context(), db.DB, "owner")
		if err != nil || pref != mode || v != int64(i+2) {
			t.Fatalf("preference: %s %d %v", pref, v, err)
		}
		other, err := ResolveForConversation(t.Context(), db.DB, "owner", "other")
		if err != nil || other.PermissionMode != PermissionAlwaysAsk {
			t.Fatalf("other conversation changed: %+v %v", other, err)
		}
		if w := update(PermissionAllowAll, i+2, i+1); w.Code != 409 {
			t.Fatalf("stale user preference accepted: %d %s", w.Code, w.Body.String())
		}
	}
	mode, _, err := UserPermission(t.Context(), db.DB, "other-user")
	if err != nil || mode != PermissionAlwaysAsk {
		t.Fatalf("cross user default: %s %v", mode, err)
	}
}

func TestUnboundShellAndMCPFutureGrant(t *testing.T) {
	for _, capability := range []string{"shell", "tool"} {
		t.Run(capability, func(t *testing.T) {
			db, grant, states, conversation := operationFixture(t, PermissionAskAsNeeded)
			if err := db.Where("conversation_id = ?", conversation).Delete(&orm.ConversationWorkspaceBinding{}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.Conversation{}).Where("id = ?", conversation).Updates(map[string]any{"permission_mode": PermissionAskAsNeeded, "permission_version": 1}).Error; err != nil {
				t.Fatal(err)
			}
			req := hostRequest(grant, conversation, "unused", OperationShell)
			req.WorkspaceID, req.Path, req.Capability, req.ToolName, req.Command = "", "", "shell", "shell", "pwd"
			if capability == "tool" {
				req.Capability, req.Operation, req.ToolName, req.Command, req.ToolIdentity = "tool", OperationTool, "search", "", "mcp:v1:"+strings.Repeat("a", 64)
			}
			prepared, err := PrepareOperation(t.Context(), db.DB, states, req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecideOperation(t.Context(), db.DB, states, prepared.OperationID, "allow_future", "owner"); err != nil {
				t.Fatal(err)
			}
			snapshot, err := ResolveForConversation(t.Context(), db.DB, "owner", conversation)
			if err != nil || len(snapshot.OpaqueToolGrants) != 1 {
				t.Fatalf("grants: %+v %v", snapshot, err)
			}
		})
	}
}
