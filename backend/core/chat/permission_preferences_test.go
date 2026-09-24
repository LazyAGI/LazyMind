package chat

import (
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/localworkspace"
	"lazymind/core/store"
)

func TestPermissionPreferenceCASAndNewConversationInheritance(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	patch := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		PatchChatSettings(w, newSettingsRequest("PATCH", "/user/chat-settings", body, "owner", nil))
		return w
	}
	for _, body := range []string{`{"default_permission_mode":"allow_all"}`, `{"default_permission_mode":"invalid","permission_version":1}`} {
		if w := patch(body); w.Code != 400 {
			t.Fatalf("invalid update: %d %s", w.Code, w.Body.String())
		}
	}
	w := patch(`{"default_permission_mode":"allow_all","permission_version":1}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	pref := decodeChatSettingsResponse(t, w)
	if pref.DefaultPermissionMode != "allow_all" || pref.PermissionVersion != 2 {
		t.Fatalf("preference: %+v", pref)
	}
	if w := patch(`{"default_permission_mode":"always_ask","permission_version":1}`); w.Code != 409 {
		t.Fatalf("stale update: %d %s", w.Code, w.Body.String())
	}
	for _, background := range []bool{false, true} {
		id := "quick"
		if background {
			id = "task"
		}
		conv, _, err := ensureConversationWithWorkspace(t.Context(), db.DB, id, id, nil, nil, "owner", "Owner", background, "", nil, nil, map[string]any{"workspace_permission_mode": "always_ask"})
		if err != nil {
			t.Fatal(err)
		}
		if conv.PermissionMode != localworkspace.PermissionAllowAll || conv.PermissionVersion != 1 {
			t.Fatalf("client overwrote default: %+v", conv)
		}
	}
	if w := patch(`{"default_permission_mode":"always_ask","permission_version":2,"enable_tool_retrieval":true}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	existing, err := localworkspace.ResolveForConversation(t.Context(), db.DB, "owner", "quick")
	if err != nil || existing.PermissionMode != localworkspace.PermissionAllowAll {
		t.Fatalf("old conversation changed: %+v %v", existing, err)
	}
	conv, _, err := ensureConversationWithWorkspace(t.Context(), db.DB, "later", "later", nil, nil, "owner", "Owner", false, "", nil, nil, nil)
	if err != nil || conv.PermissionMode != localworkspace.PermissionAlwaysAsk {
		t.Fatalf("latest preference ignored: %+v %v", conv, err)
	}
}
