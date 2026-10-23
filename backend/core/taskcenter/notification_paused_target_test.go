package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestPausedNotificationPreparationIsBoundToSchedule(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserSchedule{}, &orm.UserNotificationPreferences{}).DB
	config := DefaultNotificationConfig()
	config.Channels["feishu"] = NotificationChannelRule{Enabled: true, AccountID: "account", RecipientID: "group"}
	raw, _ := json.Marshal(config)
	value := string(raw)
	for _, id := range []string{"first", "second"} {
		if err := db.Create(&orm.UserSchedule{ID: id, UserID: "owner", NotificationConfig: &value, NotificationRevision: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := PrepareScheduleNotificationUpdateForSchedule(t.Context(), db, "owner", "first", ScheduleNotificationUpdate{Revision: 1, Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		return SaveScheduleNotificationUpdate(t.Context(), tx, "owner", "second", prepared)
	})
	var problem *notificationError
	if !errors.As(err, &problem) || problem.status != http.StatusConflict {
		t.Fatalf("prepared paused channel was reusable for a different schedule: %v", err)
	}
}

func TestPausedNotificationSaveRechecksOriginalTarget(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserSchedule{}, &orm.UserNotificationPreferences{}).DB
	config := DefaultNotificationConfig()
	config.Channels["feishu"] = NotificationChannelRule{Enabled: true, AccountID: "account", RecipientID: "group"}
	raw, _ := json.Marshal(config)
	value := string(raw)
	if err := db.Create(&orm.UserSchedule{ID: "schedule", UserID: "owner", NotificationConfig: &value, NotificationRevision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareScheduleNotificationUpdateForSchedule(t.Context(), db, "owner", "schedule", ScheduleNotificationUpdate{Revision: 1, Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	config.Channels["feishu"] = NotificationChannelRule{Enabled: true, AccountID: "new-account", RecipientID: "new-group"}
	raw, _ = json.Marshal(config)
	if err := db.Model(&orm.UserSchedule{}).Where("id = ?", "schedule").Update("notification_config", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		return SaveScheduleNotificationUpdate(t.Context(), tx, "owner", "schedule", prepared)
	})
	var problem *notificationError
	if !errors.As(err, &problem) || problem.status != http.StatusConflict {
		t.Fatalf("changed original target escaped transaction recheck: %v", err)
	}
}

func TestPausedNotificationPreparationCannotUseAnotherOwnersOriginal(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserSchedule{}, &orm.UserNotificationPreferences{}).DB
	config := DefaultNotificationConfig()
	config.Channels["feishu"] = NotificationChannelRule{Enabled: true, AccountID: "foreign-account", RecipientID: "foreign-group"}
	raw, _ := json.Marshal(config)
	value := string(raw)
	if err := db.Create(&orm.UserSchedule{ID: "guessed-schedule", UserID: "other-owner", NotificationConfig: &value, NotificationRevision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unavailable", http.StatusNotFound)
	}))
	defer gateway.Close()
	t.Setenv("LAZYMIND_CHANNEL_GATEWAY_BASE_URL", gateway.URL)
	_, err := PrepareScheduleNotificationUpdateForSchedule(t.Context(), db, "owner", "guessed-schedule", ScheduleNotificationUpdate{Revision: 1, Config: &config})
	if !errors.Is(err, gorm.ErrRecordNotFound) || requests.Load() != 0 {
		t.Fatalf("guessed foreign schedule disclosed or reused a target: requests=%d error=%v", requests.Load(), err)
	}
}

func TestPausedNotificationSaveSerializesWithConcurrentGateEnable(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserSchedule{}, &orm.UserNotificationPreferences{}).DB
	if db.Dialector.Name() != "postgres" {
		t.Skip("requires PostgreSQL row locks")
	}
	config := DefaultNotificationConfig()
	config.Channels["feishu"] = NotificationChannelRule{Enabled: true, AccountID: "account", RecipientID: "group"}
	raw, _ := json.Marshal(config)
	value := string(raw)
	if err := db.Create(&orm.UserSchedule{ID: "schedule", UserID: "owner", NotificationConfig: &value, NotificationRevision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LoadNotificationPreferences(t.Context(), db, "owner"); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareScheduleNotificationUpdateForSchedule(t.Context(), db, "owner", "schedule", ScheduleNotificationUpdate{Revision: 1, Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	gateTx := db.WithContext(ctx).Begin()
	if gateTx.Error != nil {
		t.Fatal(gateTx.Error)
	}
	defer gateTx.Rollback()
	if _, err := LoadNotificationPreferences(ctx, gateTx, "owner"); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(config)
	if err := gateTx.Model(&orm.UserNotificationPreferences{}).Where("user_id = ?", "owner").Update("defaults", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SELECT set_config('application_name', ?, true)", t.Name()).Error; err != nil {
				return err
			}
			return SaveScheduleNotificationUpdate(ctx, tx, "owner", "schedule", prepared)
		})
	}()
	locked := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		if err := db.WithContext(ctx).Raw("SELECT count(*) FROM pg_stat_activity WHERE application_name = ? AND wait_event_type = 'Lock'", t.Name()).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			locked = true
			break
		}
		select {
		case err := <-done:
			t.Fatalf("save bypassed the global preference lock: %v", err)
		case <-time.After(5 * time.Millisecond):
		}
	}
	if !locked {
		t.Fatal("save did not wait for the concurrent preference change")
	}
	if err := gateTx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var problem *notificationError
		if !errors.As(err, &problem) || problem.status != http.StatusConflict {
			t.Fatalf("save enabled an unvalidated target after waiting for the gate: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("save did not finish after the gate transaction committed")
	}
	var schedule orm.UserSchedule
	if err := db.First(&schedule, "id = ?", "schedule").Error; err != nil {
		t.Fatal(err)
	}
	if schedule.NotificationRevision != 1 {
		t.Fatal("rejected concurrent save changed the schedule")
	}
}
