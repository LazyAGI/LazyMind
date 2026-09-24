package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/scheduler"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
)

// These tests exercise the registered API and real storage. They intentionally
// fail until notification behavior is implemented; there are no substitute
// handlers, notification implementations, or expected-failure decorators.
type notificationAPI struct {
	t      *testing.T
	db     *orm.DB
	router http.Handler
}

func newNotificationAPI(t *testing.T) *notificationAPI {
	t.Helper()
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	router := mux.NewRouter()
	registerAllRoutes(router)
	return &notificationAPI{t: t, db: db, router: router}
}

func (a *notificationAPI) request(method, path, owner string, body any) *httptest.ResponseRecorder {
	a.t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "notification-contract-request")
	if owner != "" {
		req.Header.Set("X-User-Id", owner)
	}
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	return rec
}

func (a *notificationAPI) data(method, path, owner string, body any) map[string]any {
	a.t.Helper()
	rec := a.request(method, path, owner, body)
	if rec.Code != http.StatusOK {
		a.t.Fatalf("%s %s: want 200, got %d: %s", method, path, rec.Code, rec.Body.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		a.t.Fatal(err)
	}
	if envelope["code"] != float64(0) {
		a.t.Fatalf("expected Core success envelope: %s", rec.Body.String())
	}
	return notificationObject(a.t, envelope["data"])
}

func notificationObject(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %#v", value)
	}
	return result
}

func notificationItems(t *testing.T, value any) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("expected array, got %#v", value)
	}
	return result
}

func notificationError(t *testing.T, rec *httptest.ResponseRecorder, status int, reason string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("want HTTP %d, got %d: %s", status, rec.Code, rec.Body.String())
	}
	var body struct {
		Code int
		Data map[string]any
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code == 0 {
		t.Fatalf("expected safe Core error: %s", rec.Body.String())
	}
	detail := notificationObject(t, body.Data["detail"])
	if detail["reason"] != reason || detail["request_id"] != "notification-contract-request" {
		t.Fatalf("missing stable reason/request_id: %s", rec.Body.String())
	}
}

func notificationConfig(desktop bool) map[string]any {
	return map[string]any{
		"events": map[string]any{
			"succeeded": map[string]any{"enabled": true, "content": "summary"},
			"failed":    map[string]any{"enabled": true, "content": "summary"},
			"waiting":   map[string]any{"enabled": false, "content": "summary"},
		},
		"channels": map[string]any{"desktop": map[string]any{"enabled": desktop}},
	}
}

func (a *notificationAPI) schedule(id string, legacy bool) string {
	a.t.Helper()
	row := orm.UserSchedule{
		ID: id, UserID: "owner", Name: "每日简报", CronExpr: "0 9 * * *",
		Timezone: "Asia/Shanghai", PromptTemplate: "整理当天项目进度",
		Enabled: true, NextRunAt: time.Now().UTC().Add(time.Hour), CreatedAt: time.Now().UTC(),
	}
	var err error
	if legacy {
		// A row written by the old version must remain unconfigured after upgrade.
		err = a.db.Create(&row).Error
	} else {
		err = scheduler.CreateSchedule(a.t.Context(), a.db.DB, &row)
	}
	if err != nil {
		a.t.Fatal(err)
	}
	return "/schedules/" + id + "/notifications"
}

func (a *notificationAPI) save(path string, config map[string]any) map[string]any {
	a.t.Helper()
	view := a.data("GET", path, "owner", nil)
	return a.data("PUT", path, "owner", map[string]any{"revision": view["revision"], "config": config})
}

func TestNotificationPreferencesDefaults(t *testing.T) {
	a := newNotificationAPI(t)
	view := a.data("GET", "/user/notification-preferences", "owner", nil)
	if view["enabled"] != true || view["revision"] != float64(1) {
		t.Fatalf("unexpected initial settings: %#v", view)
	}
	config := notificationObject(t, view["defaults"])
	events := notificationObject(t, config["events"])
	for _, name := range []string{"succeeded", "failed", "waiting"} {
		event := notificationObject(t, events[name])
		if event["enabled"] != (name != "waiting") || event["content"] != "summary" {
			t.Errorf("wrong %s default: %#v", name, event)
		}
	}
	channels := notificationObject(t, config["channels"])
	if notificationObject(t, channels["desktop"])["enabled"] != true {
		t.Fatal("new tasks must default to desktop notifications")
	}
	for _, provider := range []string{"feishu", "wechat", "wecom"} {
		if channel, exists := channels[provider]; exists && notificationObject(t, channel)["enabled"] != false {
			t.Errorf("%s must not automatically send externally", provider)
		}
	}
}

func TestNotificationDefaultsApplyOnlyToNewSchedules(t *testing.T) {
	a := newNotificationAPI(t)
	legacy := a.schedule("legacy-schedule", true)
	first := a.schedule("first-schedule", false)
	old := a.data("GET", legacy, "owner", nil)
	if old["configured"] != false {
		t.Fatalf("legacy schedule gained notifications: %#v", old)
	}
	firstView := a.data("GET", first, "owner", nil)
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	config := notificationConfig(false)
	a.data("PATCH", "/user/notification-preferences", "owner",
		map[string]any{"revision": prefs["revision"], "defaults": config})
	if got := a.data("GET", first, "owner", nil); !reflect.DeepEqual(got, firstView) {
		t.Fatal("updating defaults rewrote an existing schedule")
	}
	if got := a.data("GET", legacy, "owner", nil); got["configured"] != false {
		t.Fatal("updating defaults configured a legacy schedule")
	}
	next := a.data("GET", a.schedule("next-schedule", false), "owner", nil)
	channels := notificationObject(t, notificationObject(t, next["config"])["channels"])
	if notificationObject(t, channels["desktop"])["enabled"] != false {
		t.Fatal("new schedule failed to inherit changed defaults")
	}
	other := a.data("GET", "/user/notification-preferences", "another-owner", nil)
	if notificationObject(t, notificationObject(t, notificationObject(t, other["defaults"])["channels"])["desktop"])["enabled"] != true {
		t.Fatal("another user's defaults were modified")
	}
}

func TestNotificationPreferencesAllowChannelSwitchWithoutTaskRecipient(t *testing.T) {
	a := newNotificationAPI(t)
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	config := notificationConfig(false)
	config["channels"].(map[string]any)["wecom"] = map[string]any{"enabled": true}

	saved := a.data("PATCH", "/user/notification-preferences", "owner",
		map[string]any{"revision": prefs["revision"], "defaults": config})
	channel := notificationObject(t, notificationObject(t, notificationObject(t, saved["defaults"])["channels"])["wecom"])
	if channel["enabled"] != true || channel["account_id"] != nil || channel["recipient_id"] != nil {
		t.Fatalf("settings switch unexpectedly selected a task recipient: %#v", channel)
	}
}

func TestNotificationConfigValidationIsAtomic(t *testing.T) {
	cases := []struct {
		name, reason string
		change       func(map[string]any)
	}{
		{"no_event", "NOTIFICATION_EVENT_REQUIRED", func(c map[string]any) {
			for _, value := range c["events"].(map[string]any) {
				value.(map[string]any)["enabled"] = false
			}
		}},
		{"missing_target", "NOTIFICATION_TARGET_REQUIRED", func(c map[string]any) {
			c["channels"].(map[string]any)["wechat"] = map[string]any{"enabled": true}
		}},
		{"unknown_event", "INVALID_REQUEST", func(c map[string]any) {
			c["events"].(map[string]any)["finished_sometimes"] = map[string]any{"enabled": true, "content": "summary"}
		}},
		{"unknown_content", "INVALID_REQUEST", func(c map[string]any) {
			c["events"].(map[string]any)["failed"].(map[string]any)["content"] = "internal_trace"
		}},
		{"string_boolean", "INVALID_REQUEST", func(c map[string]any) {
			c["channels"].(map[string]any)["desktop"].(map[string]any)["enabled"] = "true"
		}},
		{"unknown_provider", "INVALID_REQUEST", func(c map[string]any) {
			c["channels"].(map[string]any)["email"] = map[string]any{"enabled": true}
		}},
		{"oversized_target", "INVALID_REQUEST", func(c map[string]any) {
			c["channels"].(map[string]any)["wechat"] = map[string]any{
				"enabled": true, "account_id": strings.Repeat("a", 257), "recipient_id": "recipient",
			}
		}},
	}
	for _, sample := range cases {
		t.Run(sample.name, func(t *testing.T) {
			a := newNotificationAPI(t)
			path := a.schedule("schedule-validation", false)
			before := a.data("GET", path, "owner", nil)
			config := notificationConfig(true)
			sample.change(config)
			rec := a.request("PUT", path, "owner", map[string]any{"revision": before["revision"], "config": config})
			notificationError(t, rec, http.StatusUnprocessableEntity, sample.reason)
			if after := a.data("GET", path, "owner", nil); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected update partially changed configuration")
			}
		})
	}
}

func TestNotificationDisableRetainsTargetAndContent(t *testing.T) {
	a := newNotificationAPI(t)
	path := a.schedule("disabled-notifications", false)
	config := notificationConfig(false)
	events := config["events"].(map[string]any)
	for _, value := range events {
		value.(map[string]any)["enabled"] = false
		value.(map[string]any)["content"] = "full"
	}
	config["channels"].(map[string]any)["wechat"] = map[string]any{
		"enabled": false, "account_id": "disconnected-account", "recipient_id": "saved-recipient",
	}
	saved := a.save(path, config)
	got := notificationObject(t, saved["config"])
	if !reflect.DeepEqual(got["events"], config["events"]) {
		t.Fatal("disabled event lost its content preference")
	}
	ch := notificationObject(t, notificationObject(t, got["channels"])["wechat"])
	if ch["account_id"] != "disconnected-account" || ch["recipient_id"] != "saved-recipient" {
		t.Fatal("disabled channel lost its unavailable target")
	}
	var schedule orm.UserSchedule
	if err := a.db.First(&schedule, "id = ?", "disabled-notifications").Error; err != nil || !schedule.Enabled {
		t.Fatalf("disabling notifications changed task scheduling: %v", err)
	}
}

func TestNotificationConcurrentUpdatesRejectStaleVersion(t *testing.T) {
	a := newNotificationAPI(t)
	path := a.schedule("concurrent-notifications", false)
	before := a.data("GET", path, "owner", nil)
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for _, enabled := range []bool{true, false} {
		wg.Add(1)
		go func(enabled bool) {
			defer wg.Done()
			<-start
			results <- a.request("PUT", path, "owner",
				map[string]any{"revision": before["revision"], "config": notificationConfig(enabled)})
		}(enabled)
	}
	close(start)
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for rec := range results {
		counts[rec.Code]++
		if rec.Code == http.StatusConflict {
			notificationError(t, rec, http.StatusConflict, "NOTIFICATION_CONFIG_CONFLICT")
		}
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("must accept one writer and reject the stale writer: %#v", counts)
	}
}

func TestNotificationResetAffectsOnlySelectedSchedule(t *testing.T) {
	a := newNotificationAPI(t)
	first := a.schedule("reset-first", false)
	second := a.schedule("reset-second", false)
	a.save(first, notificationConfig(false))
	secondBefore := a.data("GET", second, "owner", nil)
	firstBefore := a.data("GET", first, "owner", nil)
	got := a.data("POST", first+":reset", "owner", map[string]any{"revision": firstBefore["revision"]})
	channels := notificationObject(t, notificationObject(t, got["config"])["channels"])
	if notificationObject(t, channels["desktop"])["enabled"] != true {
		t.Fatal("reset did not restore default desktop channel")
	}
	if after := a.data("GET", second, "owner", nil); !reflect.DeepEqual(after, secondBefore) {
		t.Fatal("reset changed another schedule")
	}
}

func TestNotificationDefaultLastEventCannotBeDisabled(t *testing.T) {
	a := newNotificationAPI(t)
	before := a.data("GET", "/user/notification-preferences", "owner", nil)
	config := notificationConfig(false)
	for _, event := range config["events"].(map[string]any) {
		event.(map[string]any)["enabled"] = false
	}
	rec := a.request("PATCH", "/user/notification-preferences", "owner",
		map[string]any{"revision": before["revision"], "defaults": config})
	notificationError(t, rec, 422, "NOTIFICATION_EVENT_REQUIRED")
	if got := a.data("GET", "/user/notification-preferences", "owner", nil); !reflect.DeepEqual(before, got) {
		t.Fatal("rejected defaults changed persisted preferences")
	}
}

func TestNotificationObjectOwnership(t *testing.T) {
	for _, method := range []string{"GET", "PUT"} {
		t.Run(method, func(t *testing.T) {
			a := newNotificationAPI(t)
			path := a.schedule("owned-schedule", false)
			view := a.data("GET", path, "owner", nil)
			rec := a.request(method, path, "intruder",
				map[string]any{"revision": view["revision"], "config": notificationConfig(false)})
			if rec.Code != http.StatusNotFound {
				t.Fatalf("cross-user %s returned %d: %s", method, rec.Code, rec.Body.String())
			}
			if got := a.data("GET", path, "owner", nil); !reflect.DeepEqual(view, got) {
				t.Fatal("cross-user request changed the owner's config")
			}
		})
	}
}

func TestNotificationRegisteredRoutesRequireIdentity(t *testing.T) {
	for _, path := range []string{
		"/user/notification-preferences", "/schedules/example/notifications",
		"/task-center/tasks/example/notifications", "/task-center/desktop-notifications",
	} {
		t.Run(path, func(t *testing.T) {
			a := newNotificationAPI(t)
			rec := a.request("GET", path, "", nil)
			notificationError(t, rec, http.StatusUnauthorized, "UNAUTHORIZED")
		})
	}
}

func TestNotificationRejectsOversizedAndMalformedRequests(t *testing.T) {
	for _, body := range []string{
		"{", strings.Repeat(" ", 16*1024+1) + "{}",
		"{\"revision\":1,\"enabled\":true,\"enabled\":false}",
	} {
		t.Run(fmt.Sprintf("bytes_%d", len(body)), func(t *testing.T) {
			a := newNotificationAPI(t)
			req := httptest.NewRequest("PATCH", "/user/notification-preferences", strings.NewReader(body))
			req.Header.Set("X-User-Id", "owner")
			req.Header.Set("X-Request-Id", "notification-contract-request")
			rec := httptest.NewRecorder()
			a.router.ServeHTTP(rec, req)
			notificationError(t, rec, 422, "INVALID_REQUEST")
		})
	}
}

func TestNotificationRuntimeSnapshotAndDesktopResult(t *testing.T) {
	a := newNotificationAPI(t)
	path := a.schedule("daily-run", false)
	configured := a.save(path, notificationConfig(true))
	run := orm.TaskCenterTask{
		ID: "notification-run", UserID: "owner", ConversationID: "notification-conversation",
		TaskType: "scheduled", Status: "running", ScheduleID: ptrNotification("daily-run"),
		Title: ptrNotification("每日简报"),
	}
	if err := taskcenter.CreateTask(t.Context(), a.db.DB, &run); err != nil {
		t.Fatal(err)
	}
	a.save(path, notificationConfig(false))
	now := time.Now().UTC()
	output := orm.TaskRunOutput{
		ID: "notification-output", TaskID: run.ID, ConversationID: run.ConversationID,
		FinalAnswerText: "今天完成了通知接口设计，下一步开始联调。",
		SummaryText:     "今天完成了通知接口设计，下一步开始联调。",
		OutputStatus:    "ready", ContentHash: "fixture-output-hash", CreatedAt: now, UpdatedAt: now,
	}
	if err := a.db.Create(&output).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := taskcenter.UpdateTaskStatus(t.Context(), a.db.DB, run.ID, "succeeded"); err != nil {
			t.Fatal(err)
		}
	}
	view := a.data("GET", "/task-center/tasks/"+run.ID+"/notifications", "owner", nil)
	snapshot := notificationObject(t, view["snapshot"])
	if snapshot["revision"] != configured["revision"] {
		t.Fatal("running task's snapshot changed with the schedule")
	}
	items := notificationItems(t, view["items"])
	if len(items) != 1 {
		t.Fatalf("expected one desktop notification despite repeated status write, got %#v", items)
	}
	notice := notificationObject(t, items[0])
	if notice["title"] != "每日简报" || notice["body"] != output.SummaryText || notice["channel"] != "desktop" {
		t.Fatalf("desktop must show task name and actual result: %#v", notice)
	}
	id, _ := notice["notification_id"].(string)
	if notice["task_id"] != run.ID || notice["event"] != "succeeded" || id == "" {
		t.Fatalf("notification lacks stable identity/navigation: %#v", notice)
	}
}

func ptrNotification(value string) *string { return &value }

func TestNotificationGlobalDisableRequiresConfirmation(t *testing.T) {
	a := newNotificationAPI(t)
	a.schedule("running-schedule", false)
	run := orm.TaskCenterTask{ID: "running-notification", UserID: "owner", ConversationID: "conv",
		TaskType: "scheduled", Status: "running", ScheduleID: ptrNotification("running-schedule")}
	if err := taskcenter.CreateTask(t.Context(), a.db.DB, &run); err != nil {
		t.Fatal(err)
	}
	before := a.data("GET", "/user/notification-preferences", "owner", nil)
	rec := a.request("PATCH", "/user/notification-preferences", "owner",
		map[string]any{"revision": before["revision"], "enabled": false})
	notificationError(t, rec, 409, "NOTIFICATION_CONFIRMATION_REQUIRED")
	if !strings.Contains(rec.Body.String(), run.ID) {
		t.Fatal("confirmation must identify the affected run")
	}
	if got := a.data("GET", "/user/notification-preferences", "owner", nil); got["enabled"] != true {
		t.Fatal("first disable request took effect before confirmation")
	}
	var persisted orm.TaskCenterTask
	if err := a.db.First(&persisted, "id = ?", run.ID).Error; err != nil || persisted.Status != "running" {
		t.Fatalf("notification switch stopped the task: %v", err)
	}
}

func TestNotificationGlobalSwitchDoesNotAffectChatBackgroundTasks(t *testing.T) {
	a := newNotificationAPI(t)
	run := orm.TaskCenterTask{ID: "chat-background-notification", UserID: "owner",
		ConversationID: "chat-conversation", TaskType: "background_chat", Status: "running"}
	if err := taskcenter.CreateTask(t.Context(), a.db.DB, &run); err != nil {
		t.Fatal(err)
	}
	before := a.data("GET", "/user/notification-preferences", "owner", nil)
	a.data("PATCH", "/user/notification-preferences", "owner",
		map[string]any{"revision": before["revision"], "enabled": false})
	var persisted orm.TaskCenterTask
	if err := a.db.First(&persisted, "id = ?", run.ID).Error; err != nil || persisted.Status != "running" {
		t.Fatalf("scheduled notification preference changed chat background work: %v", err)
	}
}

func TestNotificationGlobalChannelDisableSkipsPendingDelivery(t *testing.T) {
	a := newNotificationAPI(t)
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	config := notificationObject(t, prefs["defaults"])
	channels := notificationObject(t, config["channels"])
	channels["wecom"] = map[string]any{"enabled": true}
	enabled := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{
		"revision": prefs["revision"], "defaults": config,
	})
	now := time.Now().UTC()
	notice := orm.TaskNotification{
		ID: "pending-wecom", UserID: "owner", TaskID: "run", ScheduleID: "schedule", EventID: "event",
		Event: "succeeded", Channel: "wecom", AccountID: "account", RecipientID: "recipient",
		Title: "任务", Body: "结果", Content: "summary", Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err := a.db.Create(&notice).Error; err != nil {
		t.Fatal(err)
	}
	channels["wecom"] = map[string]any{"enabled": false}
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{
		"revision": enabled["revision"], "defaults": config,
	})
	if err := a.db.First(&notice, "id = ?", notice.ID).Error; err != nil {
		t.Fatal(err)
	}
	if notice.Status != "skipped" || notice.Reason != "NOTIFICATION_CHANNEL_DISABLED" {
		t.Fatalf("pending notification survived global channel disable: status=%q reason=%q", notice.Status, notice.Reason)
	}
}
