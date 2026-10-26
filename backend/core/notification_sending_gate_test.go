package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

func TestNotificationChannelCloseReopenDoesNotResumeSending(t *testing.T) {
	a := newNotificationAPI(t)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "synthetic-review-claim-token")
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"provider": "feishu", "items": []map[string]any{{"recipient_id": "group", "available": true}}})
	}))
	defer gateway.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gateway.URL)
	defaults := taskcenter.DefaultNotificationConfig()
	defaults.Channels["feishu"] = taskcenter.NotificationChannelRule{Enabled: true}
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "defaults": defaults})
	path := a.schedule("sending-close-reopen", false)
	config := notificationConfig(false)
	config["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true, "account_id": "account", "recipient_id": "group"}
	a.save(path, config)
	a.completeRun("sending-run", "sending-close-reopen", "result")
	var notice orm.TaskNotification
	if err := a.db.First(&notice, "task_id = ? AND channel = ?", "sending-run", "feishu").Error; err != nil {
		t.Fatal(err)
	}
	claim := func() *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"outbox_id": strings.Repeat("a", 64)})
		request := httptest.NewRequest("POST", "/task-center/notification-events/"+notice.ID+":claim", bytes.NewReader(raw))
		request.Header.Set("X-User-Id", "owner")
		request.Header.Set("X-LazyMind-Internal-Token", "synthetic-review-claim-token")
		response := httptest.NewRecorder()
		a.router.ServeHTTP(response, request)
		return response
	}
	if response := claim(); response.Code != 200 {
		t.Fatalf("first claim: %d %s", response.Code, response.Body.String())
	}
	prefs = a.data("GET", "/user/notification-preferences", "owner", nil)
	closed := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "defaults": taskcenter.DefaultNotificationConfig()})
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": closed["revision"], "defaults": defaults})
	// There is intentionally no dispatcher pass or claim between these settings updates.
	response := claim()
	if response.Code != 409 {
		t.Fatalf("next segment of old sending event resumed after channel off/on: want 409, got %d: %s", response.Code, response.Body.String())
	}
}
