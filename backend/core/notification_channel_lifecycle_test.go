package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
)

func TestNotificationChannelClosePreservesTaskTargetsAndOnlyResumesFutureRuns(t *testing.T) {
	a := newNotificationAPI(t)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("lifecycle validation attempted external send: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"provider": "feishu", "items": []map[string]any{{"recipient_id": "daily-group", "available": true}}})
	}))
	defer gateway.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gateway.URL)
	path := a.schedule("channel-lifecycle", false)
	config := notificationConfig(false)
	config["channels"].(map[string]any)["feishu"] = map[string]any{"enabled": true, "account_id": "daily-bot", "recipient_id": "daily-group"}
	a.save(path, config)
	var schedule orm.UserSchedule
	if err := a.db.First(&schedule, "id = ?", "channel-lifecycle").Error; err != nil {
		t.Fatal(err)
	}
	original := *schedule.NotificationConfig
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	defaults := notificationObject(t, prefs["defaults"])
	globalChannels := notificationObject(t, defaults["channels"])
	globalChannels["feishu"] = map[string]any{"enabled": true}
	enabled := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "defaults": defaults})
	globalChannels["feishu"] = map[string]any{"enabled": false}
	closed := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": enabled["revision"], "defaults": defaults})
	a.completeRun("while-closed", "channel-lifecycle", "Paused run result")
	var skipped orm.TaskNotification
	if err := a.db.First(&skipped, "task_id = ? AND channel = ?", "while-closed", "feishu").Error; err != nil {
		t.Fatal(err)
	}
	if skipped.Status != "skipped" || skipped.Reason != "NOTIFICATION_CHANNEL_DISABLED" || skipped.AccountID != "daily-bot" || skipped.RecipientID != "daily-group" {
		t.Fatalf("closed global channel did not preserve and suppress selected task target: %#v", skipped)
	}
	globalChannels["feishu"] = map[string]any{"enabled": true}
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": closed["revision"], "defaults": defaults})
	a.completeRun("after-reopen", "channel-lifecycle", "Future run result")
	var pending orm.TaskNotification
	if err := a.db.First(&pending, "task_id = ? AND channel = ?", "after-reopen", "feishu").Error; err != nil {
		t.Fatal(err)
	}
	if pending.Status != "pending" || pending.AccountID != "daily-bot" || pending.RecipientID != "daily-group" {
		t.Fatalf("future run did not resume using saved target: %#v", pending)
	}
	if err := a.db.First(&skipped, "id = ?", skipped.ID).Error; err != nil {
		t.Fatal(err)
	}
	if skipped.Status != "skipped" || skipped.Reason != "NOTIFICATION_CHANNEL_DISABLED" {
		t.Fatal("reopening resurrected the suppressed run")
	}
	if err := a.db.First(&schedule, "id = ?", schedule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if schedule.NotificationConfig == nil || *schedule.NotificationConfig != original {
		t.Fatal("global channel lifecycle rewrote the saved task configuration")
	}
}
