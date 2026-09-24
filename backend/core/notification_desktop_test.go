package main

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

func (a *notificationAPI) completeRun(id, scheduleID, body string) {
	a.t.Helper()
	run := orm.TaskCenterTask{ID: id, UserID: "owner", ConversationID: "conv-" + id,
		TaskType: "scheduled", Status: "running", ScheduleID: &scheduleID, Title: ptrNotification("每日简报")}
	if err := taskcenter.CreateTask(a.t.Context(), a.db.DB, &run); err != nil {
		a.t.Fatal(err)
	}
	now := time.Now().UTC()
	output := orm.TaskRunOutput{ID: "out-" + id, TaskID: id, ConversationID: run.ConversationID,
		FinalAnswerText: body, SummaryText: body, OutputStatus: "ready", ContentHash: id,
		ArtifactManifestJSON: []byte("[]"), CreatedAt: now, UpdatedAt: now}
	if err := a.db.Create(&output).Error; err != nil {
		a.t.Fatal(err)
	}
	if err := taskcenter.UpdateTaskStatus(a.t.Context(), a.db.DB, id, "succeeded"); err != nil {
		a.t.Fatal(err)
	}
}

func TestNotificationDesktopFeedMatchesResultAndStableIdentity(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	a := newNotificationAPI(t)
	a.schedule("desktop-schedule", false)
	a.completeRun("desktop-run", "desktop-schedule", "今天完成三项工作，下一步准备联调。")
	path := "/task-center/desktop-notifications?device_id=local&limit=20"
	first := a.data("GET", path, "owner", nil)
	items := notificationItems(t, first["items"])
	if len(items) != 1 {
		t.Fatalf("expected one desktop notice: %#v", items)
	}
	notice := notificationObject(t, items[0])
	id, ok := notice["notification_id"].(string)
	if !ok || id == "" || notice["task_id"] != "desktop-run" || notice["title"] != "每日简报" || notice["body"] != "今天完成三项工作，下一步准备联调。" {
		t.Fatalf("desktop payload does not meet screenshot requirements: %#v", notice)
	}
	if again := a.data("GET", path, "owner", nil); !reflect.DeepEqual(first["items"], again["items"]) {
		t.Fatal("repeated queries must return stable identities before acknowledgement")
	}
	if len(notificationItems(t, a.data("GET", path, "other", nil)["items"])) != 0 {
		t.Fatal("desktop feed leaked another user's result")
	}
	ackPath := "/task-center/desktop-notifications/" + id + ":ack"
	ack := map[string]any{"device_id": "local", "status": "delivered"}
	a.data("POST", ackPath, "owner", ack)
	a.data("POST", ackPath, "owner", ack)
	if len(notificationItems(t, a.data("GET", path, "owner", nil)["items"])) != 0 {
		t.Fatal("acknowledged notice was replayed")
	}
	notificationError(t, a.request("POST", ackPath, "other", ack), 404, "NOTIFICATION_NOT_FOUND")
}

func TestNotificationDesktopPermissionDeniedIsNotReportedAsDelivered(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	a := newNotificationAPI(t)
	a.schedule("permission-schedule", false)
	a.completeRun("permission-run", "permission-schedule", "实际结果")
	feed := a.data("GET", "/task-center/desktop-notifications?device_id=local", "owner", nil)
	items := notificationItems(t, feed["items"])
	if len(items) != 1 {
		t.Fatal("expected pending desktop notification")
	}
	id := notificationObject(t, items[0])["notification_id"].(string)
	receipt := a.data("POST", "/task-center/desktop-notifications/"+id+":ack", "owner",
		map[string]any{"device_id": "local", "status": "permission_denied"})
	if receipt["status"] == "sent" || receipt["status"] == "delivered" || receipt["reason"] != "DESKTOP_NOTIFICATION_PERMISSION_DENIED" {
		t.Fatalf("permission denial must remain distinguishable from delivery: %#v", receipt)
	}
}

func TestNotificationDesktopRejectsUnboundDeviceAndBadPagination(t *testing.T) {
	for _, tc := range []struct {
		query  string
		status int
		reason string
	}{
		{"device_id=some-other-computer", 403, "NOTIFICATION_DEVICE_UNAVAILABLE"},
		{"device_id=local&limit=0", 422, "INVALID_REQUEST"},
		{"device_id=local&limit=101", 422, "INVALID_REQUEST"},
		{"device_id=local&cursor=not-a-valid-cursor", 422, "INVALID_REQUEST"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
			a := newNotificationAPI(t)
			notificationError(t, a.request("GET", "/task-center/desktop-notifications?"+tc.query, "owner", nil), tc.status, tc.reason)
		})
	}
}

func TestNotificationDesktopPreviewBoundAndCursorNoDuplicates(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	a := newNotificationAPI(t)
	a.schedule("page-schedule", false)
	for i := range 3 {
		a.completeRun(fmt.Sprintf("page-%d", i), "page-schedule", strings.Repeat("完成了接口联调。", 40))
	}
	path := "/task-center/desktop-notifications?device_id=local&limit=2"
	first := a.data("GET", path, "owner", nil)
	items := notificationItems(t, first["items"])
	if len(items) != 2 {
		t.Fatalf("first page length=%d", len(items))
	}
	cursor, _ := first["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("missing next cursor")
	}
	second := a.data("GET", path+"&cursor="+url.QueryEscape(cursor), "owner", nil)
	if len(notificationItems(t, second["items"])) != 1 {
		t.Fatal("second page must contain the remaining notification")
	}
	seen := map[any]bool{}
	for _, raw := range append(items, notificationItems(t, second["items"])...) {
		notice := notificationObject(t, raw)
		if seen[notice["notification_id"]] {
			t.Fatal("duplicate notification across pages")
		}
		seen[notice["notification_id"]] = true
		body, _ := notice["body"].(string)
		if len([]rune(body)) > 200 || !strings.HasPrefix(body, "完成了接口联调。") {
			t.Fatalf("preview must retain actual result and be <=200 characters: %q", body)
		}
	}
}

func TestNotificationGlobalCloseSkipsPendingDesktopAndDoesNotBackfill(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	a := newNotificationAPI(t)
	a.schedule("close-schedule", false)
	a.completeRun("before-close", "close-schedule", "关闭前已完成但未领取的结果")
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	closed := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "enabled": false})
	a.completeRun("during-close", "close-schedule", "关闭期间的结果")
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": closed["revision"], "enabled": true})
	feed := a.data("GET", "/task-center/desktop-notifications?device_id=local", "owner", nil)
	if len(notificationItems(t, feed["items"])) != 0 {
		t.Fatal("reopening replayed notifications suppressed while the switch was off")
	}
	for _, runID := range []string{"before-close", "during-close"} {
		view := a.data("GET", "/task-center/tasks/"+runID+"/notifications", "owner", nil)
		items := notificationItems(t, view["items"])
		if len(items) != 1 || notificationObject(t, items[0])["status"] != "skipped" {
			t.Fatalf("suppressed notification history missing for %s: %#v", runID, items)
		}
	}
}

func TestNotificationGlobalConfirmationRechecksRunningSet(t *testing.T) {
	a := newNotificationAPI(t)
	a.schedule("confirm-schedule", false)
	for _, id := range []string{"first-run", "new-run"} {
		run := orm.TaskCenterTask{ID: id, UserID: "owner", ConversationID: "conv-" + id,
			TaskType: "scheduled", Status: "running", ScheduleID: ptrNotification("confirm-schedule")}
		if err := taskcenter.CreateTask(t.Context(), a.db.DB, &run); err != nil {
			t.Fatal(err)
		}
	}
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	payload := map[string]any{"revision": prefs["revision"], "enabled": false, "confirm_running_task_ids": []string{"first-run"}}
	notificationError(t, a.request("PATCH", "/user/notification-preferences", "owner", payload), 409, "NOTIFICATION_CONFIRMATION_REQUIRED")
	payload["confirm_running_task_ids"] = []string{"first-run", "new-run"}
	closed := a.data("PATCH", "/user/notification-preferences", "owner", payload)
	if closed["enabled"] != false {
		t.Fatal("confirmed switch did not close")
	}
}

// Inject the state change after ack's initial read, at the final UPDATE boundary.
// This deterministic SQLite test verifies the SQL predicate; a live PostgreSQL
// READ COMMITTED race remains a separate integration check.
func TestNotificationDesktopAckPreservesConcurrentDisable(t *testing.T) {
	for _, reason := range []string{"NOTIFICATIONS_DISABLED", "NOTIFICATION_CHANNEL_DISABLED"} {
		t.Run(reason, func(t *testing.T) {
			t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
			a := newNotificationAPI(t)
			a.schedule("race-schedule", false)
			a.completeRun("race-run", "race-schedule", "result")
			feed := a.data("GET", "/task-center/desktop-notifications?device_id=local", "owner", nil)
			id := notificationObject(t, notificationItems(t, feed["items"])[0])["notification_id"].(string)
			injected := false
			err := a.db.DB.Callback().Update().Before("gorm:update").Register("review:disable-before-ack", func(tx *gorm.DB) {
				if injected || tx.Statement.Table != "task_notifications" {
					return
				}
				injected = true
				tx.AddError(tx.Exec("UPDATE task_notifications SET status = ?, reason = ? WHERE id = ?", "skipped", reason, id).Error)
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { a.db.DB.Callback().Update().Remove("review:disable-before-ack") })
			a.data("POST", "/task-center/desktop-notifications/"+id+":ack", "owner", map[string]any{"device_id": "local", "status": "delivered"})
			var notice orm.TaskNotification
			if err := a.db.DB.First(&notice, "id = ?", id).Error; err != nil {
				t.Fatal(err)
			}
			if !injected || notice.Status != "skipped" || notice.Reason != reason {
				t.Fatalf("disable overwritten: %#v (injected=%v)", notice, injected)
			}
		})
	}
}
