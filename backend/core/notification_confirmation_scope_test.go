package main

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func confirmedNotificationTaskIDs(t *testing.T, a *notificationAPI, revision any) []string {
	t.Helper()
	rec := a.request("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": revision, "enabled": false})
	notificationError(t, rec, 409, "NOTIFICATION_CONFIRMATION_REQUIRED")
	var body struct {
		Data struct {
			Detail struct {
				RunningTaskIDs []string `json:"running_task_ids"`
			} `json:"detail"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data.Detail.RunningTaskIDs
}

func TestNotificationMasterCloseUsesCurrentVisibleTaskStatus(t *testing.T) {
	a := newNotificationAPI(t)
	now := time.Now().UTC()
	for _, session := range []orm.WorkflowSession{
		{ID: "completed-session", ConversationID: "completed-conversation", Status: "completed"},
		{ID: "active-session", ConversationID: "active-conversation", Status: "active"},
		{ID: "dismissed-session", ConversationID: "dismissed-conversation", Status: "active", Dismissed: true},
	} {
		if err := a.db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []orm.TaskCenterTask{
		{ID: "completed", UserID: "owner", ConversationID: "completed-conversation", TaskType: "workflow_run", WorkflowSessionID: ptrNotification("completed-session"), Status: "running", CreatedAt: now},
		{ID: "active", UserID: "owner", ConversationID: "active-conversation", TaskType: "workflow_run", WorkflowSessionID: ptrNotification("active-session"), Status: "succeeded", CreatedAt: now},
		{ID: "dismissed", UserID: "owner", ConversationID: "dismissed-conversation", TaskType: "workflow_run", WorkflowSessionID: ptrNotification("dismissed-session"), Status: "running", CreatedAt: now},
		{ID: "timed-out", UserID: "owner", ConversationID: "timed-out-conversation", TaskType: "background_chat", Status: "running", CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "waiting", UserID: "owner", ConversationID: "waiting-conversation", TaskType: "background_chat", Status: "waiting_inputs", CreatedAt: now},
		{ID: "archived", UserID: "owner", ConversationID: "archived-conversation", TaskType: "background_chat", Status: "running", CreatedAt: now, ArchivedAt: &now},
		{ID: "other-owner", UserID: "other", TaskType: "background_chat", Status: "running", CreatedAt: now},
	} {
		if err := a.db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	want := []string{"active", "waiting"}
	if got := confirmedNotificationTaskIDs(t, a, prefs["revision"]); !reflect.DeepEqual(got, want) {
		t.Fatalf("confirmation tasks = %v, want only current visible active tasks %v", got, want)
	}
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{
		"revision": prefs["revision"], "enabled": false, "confirm_running_task_ids": want,
	})
}

func TestNotificationMasterCloseExcludesAlreadyDisabledChannels(t *testing.T) {
	a := newNotificationAPI(t)
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	defaults := notificationConfig(false)
	defaultChannels := notificationObject(t, defaults["channels"])
	defaultChannels["feishu"] = map[string]any{"enabled": true}
	prefs = a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{
		"revision": prefs["revision"], "defaults": defaults,
	})
	for _, channel := range []string{"desktop", "feishu", "wecom"} {
		config := notificationConfig(false)
		notificationObject(t, config["channels"])[channel] = map[string]any{"enabled": true}
		raw, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.db.Create(&orm.TaskCenterTask{
			ID: channel, UserID: "owner", ConversationID: channel + "-conversation", TaskType: "scheduled",
			Status: "running", NotificationConfig: ptrNotification(string(raw)),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := a.db.Create(&orm.TaskCenterTask{ID: "ordinary", UserID: "owner", TaskType: "background_chat", Status: "running"}).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{"feishu"}
	if got := confirmedNotificationTaskIDs(t, a, prefs["revision"]); !reflect.DeepEqual(got, want) {
		t.Fatalf("confirmation tasks = %v, want only tasks with a currently enabled channel %v", got, want)
	}
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{
		"revision": prefs["revision"], "enabled": false, "confirm_running_task_ids": want,
	})
}

func TestNotificationMasterCloseAcceptsCompletedConfirmedRuns(t *testing.T) {
	a := newNotificationAPI(t)
	for _, task := range []orm.TaskCenterTask{
		{ID: "running", UserID: "owner", TaskType: "background_chat", Status: "running"},
		{ID: "completed", UserID: "owner", TaskType: "background_chat", Status: "succeeded"},
	} {
		if err := a.db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	closed := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{
		"revision": prefs["revision"], "enabled": false, "confirm_running_task_ids": []string{"completed", "running"},
	})
	if closed["enabled"] != false {
		t.Fatalf("completed confirmed run prevented closing: %#v", closed)
	}
}
