package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

func TestNotificationClaimRequiresServiceIdentityAndHonorsClose(t *testing.T) {
	a := newNotificationAPI(t)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "synthetic-claim-token")
	a.schedule("claim-schedule", false)
	a.completeRun("claim-run", "claim-schedule", "实际结果")
	var notice orm.TaskNotification
	if err := a.db.First(&notice, "task_id = ?", "claim-run").Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Model(&notice).Update("channel", "wechat").Error; err != nil {
		t.Fatal(err)
	}
	config := taskcenter.DefaultNotificationConfig()
	config.Channels["wechat"] = taskcenter.NotificationChannelRule{Enabled: true}
	defaults, _ := json.Marshal(config)
	if err := a.db.Model(&orm.UserNotificationPreferences{}).Where("user_id = ?", "owner").Update("defaults", string(defaults)).Error; err != nil {
		t.Fatal(err)
	}
	claim := func(owner, token string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"outbox_id": strings.Repeat("a", 64)})
		request := httptest.NewRequest("POST", "/task-center/notification-events/"+notice.ID+":claim", bytes.NewReader(raw))
		request.Header.Set("X-User-Id", owner)
		request.Header.Set("X-Request-Id", "notification-contract-request")
		request.Header.Set("X-LazyMind-Internal-Token", token)
		response := httptest.NewRecorder()
		a.router.ServeHTTP(response, request)
		return response
	}
	notificationError(t, claim("owner", ""), 401, "UNAUTHORIZED")
	notificationError(t, claim("other", "synthetic-claim-token"), 404, "NOTIFICATION_NOT_FOUND")
	if response := claim("owner", "synthetic-claim-token"); response.Code != 200 {
		t.Fatalf("claim failed: %s", response.Body.String())
	}
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	closed := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "enabled": false})
	notificationError(t, claim("owner", "synthetic-claim-token"), 409, "NOTIFICATIONS_DISABLED")
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"notification_id": strings.Repeat("a", 64), "status": "sending", "reason": ""})
	}))
	defer stale.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", stale.URL)
	if err := taskcenter.DispatchNotifications(t.Context(), a.db.DB); err != nil {
		t.Fatal(err)
	}
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": closed["revision"], "enabled": true})
	notificationError(t, claim("owner", "synthetic-claim-token"), 409, "NOTIFICATIONS_DISABLED")
}

func TestNotificationClaimHonorsChannelCloseAfterReopen(t *testing.T) {
	a := newNotificationAPI(t)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "synthetic-claim-token")
	a.schedule("claim-schedule", false)
	a.completeRun("claim-run", "claim-schedule", "实际结果")
	var notice orm.TaskNotification
	if err := a.db.First(&notice, "task_id = ?", "claim-run").Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Model(&notice).Updates(map[string]any{
		"channel": "wechat", "account_id": "account", "recipient_id": "recipient",
	}).Error; err != nil {
		t.Fatal(err)
	}
	config := taskcenter.DefaultNotificationConfig()
	config.Channels["wechat"] = taskcenter.NotificationChannelRule{Enabled: true, AccountID: "account", RecipientID: "recipient"}
	defaults, _ := json.Marshal(config)
	if err := a.db.Model(&orm.UserNotificationPreferences{}).Where("user_id = ?", "owner").Update("defaults", string(defaults)).Error; err != nil {
		t.Fatal(err)
	}
	claim := func(owner, token string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"outbox_id": strings.Repeat("a", 64)})
		request := httptest.NewRequest("POST", "/task-center/notification-events/"+notice.ID+":claim", bytes.NewReader(raw))
		request.Header.Set("X-User-Id", owner)
		request.Header.Set("X-Request-Id", "notification-contract-request")
		request.Header.Set("X-LazyMind-Internal-Token", token)
		response := httptest.NewRecorder()
		a.router.ServeHTTP(response, request)
		return response
	}
	notificationError(t, claim("owner", ""), 401, "UNAUTHORIZED")
	notificationError(t, claim("other", "synthetic-claim-token"), 404, "NOTIFICATION_NOT_FOUND")
	if response := claim("owner", "synthetic-claim-token"); response.Code != 200 {
		t.Fatalf("claim failed: %s", response.Body.String())
	}
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	closed := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "defaults": taskcenter.DefaultNotificationConfig()})
	notificationError(t, claim("owner", "synthetic-claim-token"), 409, "NOTIFICATION_CHANNEL_DISABLED")
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/notification-targets") {
			_ = json.NewEncoder(w).Encode(map[string]any{"provider": "wechat", "items": []map[string]any{{"recipient_id": "recipient", "available": true}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"notification_id": strings.Repeat("a", 64), "status": "sending", "reason": ""})
	}))
	defer stale.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", stale.URL)
	if err := taskcenter.DispatchNotifications(t.Context(), a.db.DB); err != nil {
		t.Fatal(err)
	}
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": closed["revision"], "defaults": config})
	notificationError(t, claim("owner", "synthetic-claim-token"), 409, "NOTIFICATION_CHANNEL_DISABLED")
}

func TestNotificationActionableWaitAndResumeUsePersistedHistory(t *testing.T) {
	a := newNotificationAPI(t)
	config := notificationConfig(true)
	notificationObject(t, notificationObject(t, config["events"])["waiting"])["enabled"] = true
	a.save(a.schedule("pending-schedule", false), config)
	id := "pending-schedule"
	task := orm.TaskCenterTask{ID: "pending-run", UserID: "owner", ConversationID: "pending-conversation", TaskType: "scheduled", ScheduleID: &id, Status: "running"}
	if err := taskcenter.CreateTask(t.Context(), a.db.DB, &task); err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&orm.ChatHistory{ID: "pending-history", Seq: 1, ConversationID: task.ConversationID, RunStatus: "completed",
		Result: "这只是中间结果", Ext: json.RawMessage(`{"ask_pending":{"ask_id":"ask-one","questions":[{"text":"是否继续执行？"}]}}`)}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := taskcenter.ReconcileScheduledNotifications(t.Context(), a.db.DB, "", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	items := notificationItems(t, a.data("GET", "/task-center/tasks/"+task.ID+"/notifications", "owner", nil)["items"])
	if len(items) != 1 || notificationObject(t, items[0])["event"] != "waiting" || notificationObject(t, items[0])["body"] != "是否继续执行？" {
		t.Fatalf("wrong waiting notification: %#v", items)
	}
	var count int64
	a.db.Model(&orm.TaskRunOutput{}).Where("task_id = ?", task.ID).Count(&count)
	if count != 0 {
		t.Fatal("intermediate output was finalized")
	}
	if err := a.db.Create(&orm.ChatHistory{ID: "final-history", Seq: 2, ConversationID: task.ConversationID, RunStatus: "completed", Result: "用户确认后完成的最终结果"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := taskcenter.FinalizeScheduledConversation(t.Context(), a.db.DB, task.ConversationID); err != nil {
		t.Fatal(err)
	}
	items = notificationItems(t, a.data("GET", "/task-center/tasks/"+task.ID+"/notifications", "owner", nil)["items"])
	if len(items) != 2 {
		t.Fatalf("resume did not produce one final notification: %#v", items)
	}
	var output orm.TaskRunOutput
	if err := a.db.First(&output, "task_id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if output.FinalAnswerText != "用户确认后完成的最终结果" {
		t.Fatalf("wrong final output: %q", output.FinalAnswerText)
	}
}

func TestNotificationProtocolCleanupAndFailedPartialOutput(t *testing.T) {
	for _, value := range []string{"<think>unfinished private reasoning", "<tool_call>private parameters"} {
		if taskcenter.TaskOutputBody(value) != "" {
			t.Fatalf("leaked incomplete protocol: %q", value)
		}
	}
	a := newNotificationAPI(t)
	a.schedule("failed-partial", false)
	id := "failed-partial"
	task := orm.TaskCenterTask{ID: "failed-partial-run", UserID: "owner", ConversationID: "failed-conv", TaskType: "scheduled", ScheduleID: &id, Status: "running"}
	if err := taskcenter.CreateTask(t.Context(), a.db.DB, &task); err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&orm.ChatHistory{ID: "failed-history", ConversationID: task.ConversationID, RunStatus: "failed", Result: "不能作为成功结果的部分输出"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := taskcenter.FinalizeScheduledConversation(t.Context(), a.db.DB, task.ConversationID); err != nil {
		t.Fatal(err)
	}
	view := a.data("GET", "/task-center/tasks/"+task.ID+"/notifications", "owner", nil)
	items := notificationItems(t, view["items"])
	if len(items) != 1 || notificationObject(t, items[0])["event"] != "failed" {
		t.Fatalf("partial output became success: %#v", items)
	}
}
