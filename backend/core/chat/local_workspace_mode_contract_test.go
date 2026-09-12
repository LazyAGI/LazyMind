package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common"
	"lazymind/core/store"
)

func TestWorkspaceChatAndCloudRequestsAreRejectedBeforeExecution(t *testing.T) {
	for _, tc := range []struct {
		name, runtime string
		work          bool
	}{
		{"Chat", "local", false}, {"CloudWork", "cloud", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LAZYMIND_RUNTIME_MODE", tc.runtime)
			t.Setenv("LAZYMIND_LOCAL_WORKSPACE_RUNTIME", "local")
			db := newToolsTestDB(t)
			store.Init(db.DB, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			var calls int
			server := startChatToolsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				http.Error(w, "unexpected downstream request", http.StatusBadGateway)
			}))
			for _, key := range []string{"LAZYMIND_CHAT_SERVICE_URL", "LAZYMIND_AUTH_SERVICE_URL", "LAZYMIND_SCAN_CONTROL_PLANE_URL"} {
				t.Setenv(key, server)
			}
			body, err := json.Marshal(map[string]any{"input": []map[string]string{{"input_type": "text", "text": "hello"}}, "stream": false, "run_in_background": tc.work, "workspace_id": "grant"})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/conversations:chat", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", "owner")
			response := httptest.NewRecorder()
			ChatConversations(response, req)
			var envelope struct {
				Code int
				Data struct{ Detail any }
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			detail, ok := envelope.Data.Detail.(map[string]any)
			if response.Code != 403 || envelope.Code != common.ResolveAppError("forbidden", 403).Code || !ok || detail["reason"] != "mode_forbidden" {
				t.Errorf("want mode_forbidden: got %d %s", response.Code, response.Body.String())
			}
			if calls != 0 {
				t.Errorf("rejected workspace request dispatched %d downstream requests", calls)
			}
			var count int64
			if err := db.Table("conversations").Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Errorf("rejected request persisted %d conversations", count)
			}
		})
	}
}
