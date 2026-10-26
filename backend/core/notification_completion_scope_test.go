package main

import (
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

func TestNotificationDesktopFeedIncludesOrdinaryTaskCompletions(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	a := newNotificationAPI(t)
	for _, taskType := range []string{"workflow_run", "background_chat"} {
		task := orm.TaskCenterTask{ID: taskType, UserID: "owner", ConversationID: "conversation", TaskType: taskType, Status: "running"}
		if err := taskcenter.CreateTask(t.Context(), a.db.DB, &task); err != nil {
			t.Fatal(err)
		}
		if err := taskcenter.UpdateTaskStatus(t.Context(), a.db.DB, task.ID, "succeeded"); err != nil {
			t.Fatal(err)
		}
	}
	feed := a.data("GET", "/task-center/desktop-notifications?device_id=local", "owner", nil)
	items := notificationItems(t, feed["items"])
	if len(items) != 2 {
		t.Fatalf("ordinary task feed=%#v, want two notices", items)
	}
	for _, raw := range items {
		item := notificationObject(t, raw)
		if item["user_id"] != "owner" || item["channel"] != "desktop" || item["schedule_id"] != "" || item["body"] == "" {
			t.Fatalf("ordinary task broke desktop feed contract: %#v", item)
		}
	}
}

func TestNotificationMasterCloseConfirmsOrdinaryAndUnconfiguredScheduledTasks(t *testing.T) {
	a := newNotificationAPI(t)
	for _, row := range []orm.TaskCenterTask{
		{ID: "ordinary", UserID: "owner", ConversationID: "ordinary-conversation", TaskType: "background_chat", Status: "running"},
		{ID: "legacy", UserID: "owner", ConversationID: "legacy-conversation", TaskType: "scheduled", Status: "running"},
	} {
		if err := a.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	patch := map[string]any{"revision": prefs["revision"], "enabled": false}
	rejected := a.request("PATCH", "/user/notification-preferences", "owner", patch)
	notificationError(t, rejected, 409, "NOTIFICATION_CONFIRMATION_REQUIRED")
	if !strings.Contains(rejected.Body.String(), "ordinary") || !strings.Contains(rejected.Body.String(), "legacy") {
		t.Fatalf("confirmation omitted affected desktop tasks: %s", rejected.Body.String())
	}
	patch["confirm_running_task_ids"] = []string{"ordinary", "legacy"}
	if closed := a.data("PATCH", "/user/notification-preferences", "owner", patch); closed["enabled"] != false {
		t.Fatalf("exact affected-task confirmation did not close master gate: %#v", closed)
	}
}
