package taskcenter

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestResolveNotificationTargetReturnsProviderSpecificGuidance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"provider":"wechat","status":"connected","default_recipient":null}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", server.URL)

	_, err := resolveNotificationTarget(t.Context(), "owner", "wechat", NotificationChannelRule{
		Enabled: true, AccountID: "account",
	})
	var problem *notificationError
	if !errors.As(err, &problem) || problem.reason != "WECHAT_NOTIFICATION_CONTEXT_REQUIRED" {
		t.Fatalf("error = %v, want WECHAT_NOTIFICATION_CONTEXT_REQUIRED", err)
	}
}

func TestInitializeScheduleNotificationsDisablesUntargetedExternalDefaults(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	defaults := DefaultNotificationConfig()
	defaults.Channels["wechat"] = NotificationChannelRule{Enabled: true}
	raw, err := json.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserNotificationPreferences{
		UserID: "owner", Enabled: true, Revision: 1, Defaults: raw, UpdatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	schedule := orm.UserSchedule{ID: "schedule", UserID: "owner"}
	if err := InitializeScheduleNotifications(t.Context(), db, &schedule); err != nil {
		t.Fatal(err)
	}
	if schedule.NotificationConfig == nil {
		t.Fatal("notification config was not initialized")
	}
	var got NotificationConfig
	if err := json.Unmarshal([]byte(*schedule.NotificationConfig), &got); err != nil {
		t.Fatal(err)
	}
	if got.Channels["wechat"].Enabled {
		t.Fatalf("accountless wechat default remained enabled: %#v", got.Channels["wechat"])
	}
	if !got.Channels["desktop"].Enabled {
		t.Fatal("valid desktop default was unexpectedly disabled")
	}
}

func TestSaveScheduleNotificationUpdateUsesAccountDefaultRecipient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channel-gateway/v1/channel-accounts/account":
			_, _ = w.Write([]byte(`{"provider":"wechat","status":"connected","default_recipient":{"recipient_id":"default-user","available":true}}`))
		case "/api/channel-gateway/v1/channel-accounts/account/notification-targets":
			_, _ = w.Write([]byte(`{"provider":"wechat","items":[{"recipient_id":"default-user","available":true}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", server.URL)

	db := orm.MigrateAllModelsForTest(t).DB
	schedule := orm.UserSchedule{ID: "schedule", UserID: "owner", NotificationRevision: 1}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatal(err)
	}
	config := DefaultNotificationConfig()
	config.Channels["wechat"] = NotificationChannelRule{Enabled: true, AccountID: "account"}
	if err := SaveScheduleNotificationUpdate(t.Context(), db, "owner", schedule.ID, ScheduleNotificationUpdate{
		Revision: 1, Config: &config,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&schedule, "id = ?", schedule.ID).Error; err != nil {
		t.Fatal(err)
	}
	var saved NotificationConfig
	if schedule.NotificationConfig == nil || json.Unmarshal([]byte(*schedule.NotificationConfig), &saved) != nil {
		t.Fatal("saved notification config is missing or invalid")
	}
	if got := saved.Channels["wechat"].RecipientID; got != "default-user" {
		t.Fatalf("recipient_id = %q, want account default", got)
	}
}

func TestDispatchNotificationsSkipsExternalRowsWithoutTargets(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	defaults, err := json.Marshal(DefaultNotificationConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserNotificationPreferences{
		UserID: "owner", Enabled: true, Revision: 1, Defaults: defaults, UpdatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	notice := orm.TaskNotification{
		ID: "notice", UserID: "owner", TaskID: "task", ScheduleID: "schedule", EventID: "event",
		Event: "succeeded", Channel: "wechat", ConfigRevision: 1, Title: "title", Body: "body",
		Content: "summary", Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&notice).Error; err != nil {
		t.Fatal(err)
	}

	if err := DispatchNotifications(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&notice, "id = ?", notice.ID).Error; err != nil {
		t.Fatal(err)
	}
	if notice.Status != "skipped" || notice.Reason != "NOTIFICATION_TARGET_UNAVAILABLE" {
		t.Fatalf("invalid target remained dispatchable: status=%q reason=%q", notice.Status, notice.Reason)
	}
}

func TestInitializeScheduleNotificationsPropagatesGatewayFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("include_references") != "false" {
			t.Error("resolution may call back into Core")
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", server.URL)
	db := orm.MigrateAllModelsForTest(t).DB
	config := DefaultNotificationConfig()
	config.Channels["wechat"] = NotificationChannelRule{Enabled: true, AccountID: "account"}
	raw, _ := json.Marshal(config)
	if err := db.Create(&orm.UserNotificationPreferences{UserID: "owner", Enabled: true, Revision: 1, Defaults: raw}).Error; err != nil {
		t.Fatal(err)
	}
	schedule := orm.UserSchedule{ID: "schedule", UserID: "owner"}
	if err := InitializeScheduleNotifications(t.Context(), db, &schedule); err == nil {
		t.Fatal("gateway failure silently disabled notifications")
	}
	if schedule.NotificationConfig != nil {
		t.Fatal("failed resolution mutated config")
	}
}

func TestReadNotificationPreferencesDoesNotCreateRows(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	prefs, err := ReadNotificationPreferences(t.Context(), db, "missing")
	if err != nil || !prefs.Enabled {
		t.Fatalf("preferences=%+v error=%v", prefs, err)
	}
	var count int64
	if err := db.Model(&orm.UserNotificationPreferences{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("read-only callback inserted preferences")
	}
}
