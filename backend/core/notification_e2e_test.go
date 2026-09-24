package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

func TestNotificationSchedulerGatewayEndToEnd(t *testing.T) {
	python := os.Getenv("LAZYMIND_NOTIFICATION_TEST_PYTHON")
	if python == "" {
		t.Skip("Set LAZYMIND_NOTIFICATION_TEST_PYTHON to the gateway test environment for cross-service integration")
	}
	a := newNotificationAPI(t)
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "synthetic-internal-notification-token")
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations:chat" {
			a.router.ServeHTTP(w, r)
			return
		}
		var body struct {
			ConversationID string `json:"conversation_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if err := a.db.Create(&orm.ChatHistory{ID: common.GenerateID(), ConversationID: body.ConversationID, Seq: 1,
			Result: "<think>private reasoning</think>今天完成三个接口的联调。", RunStatus: "completed"}).Error; err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer core.Close()
	t.Setenv("LAZYMIND_CORE_SELF_URL", core.URL)
	script, err := filepath.Abs("../../tests/backend/channel-gateway/notification_e2e_server.py")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), python, script, t.TempDir(), core.URL)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(output)
	read := func() map[string]any {
		t.Helper()
		if !scanner.Scan() {
			t.Fatalf("gateway fixture stopped: %s", diagnostics.String())
		}
		var value map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	control := func(action string) map[string]any {
		t.Helper()
		if err := json.NewEncoder(input).Encode(map[string]string{"command": action}); err != nil {
			t.Fatal(err)
		}
		return read()
	}
	defer func() {
		_ = json.NewEncoder(input).Encode(map[string]string{"command": "stop"})
		_ = input.Close()
		if err := command.Wait(); err != nil {
			t.Errorf("gateway fixture shutdown: %v: %s", err, diagnostics.String())
		}
	}()
	setup := read()
	gatewayURL := setup["url"].(string)
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gatewayURL)
	accounts := notificationObject(t, setup["accounts"])
	config := notificationConfig(true)
	channels := notificationObject(t, config["channels"])
	for provider, id := range accounts {
		channels[provider] = map[string]any{"enabled": true, "account_id": id, "recipient_id": "recipient"}
	}
	preferences := a.data("GET", "/user/notification-preferences", "owner", nil)
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": preferences["revision"], "defaults": config})
	a.schedule("e2e-daily", false)
	response := a.request("POST", "/schedules/e2e-daily:run-now", "owner", nil)
	if response.Code != 200 {
		t.Fatalf("run-now failed: %s", response.Body.String())
	}
	var run struct {
		TaskID string `json:"task_id"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.TaskID == "" {
		t.Fatal("run-now omitted task identity")
	}
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int64
		if err = a.db.Model(&orm.TaskNotification{}).Where("task_id = ?", run.TaskID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 4 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("scheduler did not persist four notifications")
		case <-ticker.C:
		}
	}
	for i := 0; i < 2; i++ {
		if err = taskcenter.DispatchNotifications(t.Context(), a.db.DB); err != nil {
			t.Fatal(err)
		}
	}
	// Gateway restart must keep the queued outbox and encrypted target context.
	control("restart")
	delivered := control("deliver")
	sent := notificationItems(t, delivered["sent"])
	if len(sent) != 3 {
		t.Fatalf("expected three independent channel deliveries, got %#v; %s", sent, diagnostics.String())
	}
	seen := map[string]bool{}
	for _, raw := range sent {
		item := notificationObject(t, raw)
		seen[item["provider"].(string)] = true
		text := item["text"].(string)
		if !strings.Contains(text, "今天完成三个接口") || strings.Contains(text, "private") {
			t.Fatalf("unsafe or incomplete result: %q", text)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("missing channel: %v", seen)
	}
	if err = taskcenter.DispatchNotifications(t.Context(), a.db.DB); err != nil {
		t.Fatal(err)
	}
	view := a.data("GET", "/task-center/tasks/"+run.TaskID+"/notifications", "owner", nil)
	for _, raw := range notificationItems(t, view["items"]) {
		item := notificationObject(t, raw)
		if item["channel"] != "desktop" && item["status"] != "sent" {
			t.Fatalf("delivery status not reconciled: %#v", item)
		}
	}
	feed := a.data("GET", "/task-center/desktop-notifications?device_id=local", "owner", nil)
	notices := notificationItems(t, feed["items"])
	if len(notices) != 1 {
		t.Fatalf("desktop result missing: %#v", feed)
	}
	notice := notificationObject(t, notices[0])
	a.data("POST", "/task-center/desktop-notifications/"+notice["notification_id"].(string)+":ack", "owner", map[string]any{"device_id": "local", "status": "delivered"})
	if again := notificationItems(t, control("deliver")["sent"]); len(again) != 3 {
		t.Fatalf("replayed already sent event: %#v", again)
	}
}
