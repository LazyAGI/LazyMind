package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestChatFeishuQueryDoesNotInstallSkill(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	// Chat must work even when the optional builtin catalog is not materialized.
	t.Chdir(t.TempDir())
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/chat/stream" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "tool_groups": []any{}, "data": map[string]any{"items": []any{}}})
			return
		}
		called = true
		var payload LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"text": "answer"}})
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": completedRunEvent(payload.Conversation.RunID, true)}})
	}))
	defer server.Close()
	for _, name := range []string{"LAZYMIND_CHAT_SERVICE_URL", "LAZYMIND_AUTH_SERVICE_URL", "LAZYMIND_SCAN_CONTROL_PLANE_URL"} {
		t.Setenv(name, server.URL)
	}
	r := sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", `{"conversation_id":"optional-skill-chat","query":"飞书是什么","stream":false}`, nil)
	w := httptest.NewRecorder()
	ChatConversations(w, r)
	if w.Code != http.StatusOK || !called || !strings.Contains(w.Body.String(), "answer") {
		t.Fatalf("Chat failed without builtin Skill: status=%d called=%v body=%s", w.Code, called, w.Body.String())
	}
	var count int64
	if err := db.Model(&orm.SkillV2Skill{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("Chat must not install Skills: count=%d err=%v", count, err)
	}
}
