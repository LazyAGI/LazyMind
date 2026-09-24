package taskcenter

import (
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestGlobalChannelGateAndTaskContentOverrideAreIndependent(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	global := DefaultNotificationConfig()
	global.Channels["wecom"] = NotificationChannelRule{Enabled: false}
	global.Events["succeeded"] = NotificationEventRule{Enabled: true, Content: "full"}
	globalJSON, err := json.Marshal(global)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserNotificationPreferences{
		UserID: "owner", Enabled: true, Revision: 2, Defaults: globalJSON, UpdatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	scheduleID := "schedule"
	taskConfig := `{"events":{"succeeded":{"enabled":true,"content":"summary"},"failed":{"enabled":true,"content":"summary"},"waiting":{"enabled":false,"content":"summary"}},"channels":{"wecom":{"enabled":true,"account_id":"account","recipient_id":"recipient"}}}`
	task := orm.TaskCenterTask{
		ID: "run", UserID: "owner", ConversationID: "conversation", TaskType: "scheduled", Status: "running",
		ScheduleID: &scheduleID, NotificationConfig: &taskConfig, NotificationRevision: 3,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.TaskRunOutput{
		ID: "output", TaskID: task.ID, ConversationID: task.ConversationID, FinalAnswerText: "完整结果",
		SummaryText: "结果摘要", OutputStatus: "ready", ContentHash: "hash", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := UpdateTaskStatus(t.Context(), db, task.ID, "succeeded"); err != nil {
		t.Fatal(err)
	}
	var notice orm.TaskNotification
	if err := db.First(&notice, "task_id = ? AND channel = ?", task.ID, "wecom").Error; err != nil {
		t.Fatal(err)
	}
	if notice.Status != "skipped" || notice.Reason != "NOTIFICATION_CHANNEL_DISABLED" {
		t.Fatalf("global channel gate did not block task channel: status=%q reason=%q", notice.Status, notice.Reason)
	}
	if notice.Content != "summary" || notice.Body != "结果摘要" {
		t.Fatalf("task content choice was not preserved: content=%q body=%q", notice.Content, notice.Body)
	}
}

func TestNotificationMasterSwitchStillOverridesTaskSnapshot(t *testing.T) {
	global := DefaultNotificationConfig()
	raw, err := json.Marshal(global)
	if err != nil {
		t.Fatal(err)
	}
	if got := notificationBlockReason(orm.UserNotificationPreferences{Enabled: false, Defaults: raw}, "wecom"); got != "NOTIFICATIONS_DISABLED" {
		t.Fatalf("master switch reason = %q", got)
	}
	if got := notificationBlockReason(orm.UserNotificationPreferences{Enabled: true, Defaults: raw}, "desktop"); got != "" {
		t.Fatalf("enabled master switch blocked task with %q", got)
	}
}

func TestNotificationRequiresBothMasterAndTaskEventEnabled(t *testing.T) {
	for _, test := range []struct {
		name          string
		masterEnabled bool
		taskEnabled   bool
		wantCount     int64
		wantStatus    string
		wantReason    string
	}{
		{name: "master on task off", masterEnabled: true, taskEnabled: false, wantCount: 0},
		{name: "master off task on", masterEnabled: false, taskEnabled: true, wantCount: 1, wantStatus: "skipped", wantReason: "NOTIFICATIONS_DISABLED"},
		{name: "both on", masterEnabled: true, taskEnabled: true, wantCount: 1, wantStatus: "pending"},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			if !test.masterEnabled {
				if err := db.Model(&orm.UserNotificationPreferences{}).Where("user_id = ?", "owner").Update("enabled", false).Error; err != nil {
					t.Fatal(err)
				}
			}
			scheduleID := "schedule"
			config := NotificationConfig{
				Events: map[string]NotificationEventRule{
					"succeeded": {Enabled: test.taskEnabled, Content: "summary"},
					"failed":    {Enabled: false, Content: "summary"},
					"waiting":   {Enabled: false, Content: "summary"},
				},
				Channels: map[string]NotificationChannelRule{"desktop": {Enabled: true}},
			}
			raw, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			rawConfig := string(raw)
			task := orm.TaskCenterTask{
				ID: "run", UserID: "owner", ConversationID: "conversation", TaskType: "scheduled", Status: "running",
				ScheduleID: &scheduleID, NotificationConfig: &rawConfig, NotificationRevision: 1,
			}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := db.Create(&orm.TaskRunOutput{
				ID: "output", TaskID: task.ID, ConversationID: task.ConversationID, FinalAnswerText: "完整结果",
				SummaryText: "结果摘要", OutputStatus: "ready", ContentHash: "hash", CreatedAt: now, UpdatedAt: now,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := UpdateTaskStatus(t.Context(), db, task.ID, "succeeded"); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.Model(&orm.TaskNotification{}).Where("task_id = ?", task.ID).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != test.wantCount {
				t.Fatalf("notification count = %d, want %d", count, test.wantCount)
			}
			if count == 0 {
				return
			}
			var notice orm.TaskNotification
			if err := db.First(&notice, "task_id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if notice.Status != test.wantStatus || notice.Reason != test.wantReason {
				t.Fatalf("notification status=%q reason=%q, want status=%q reason=%q", notice.Status, notice.Reason, test.wantStatus, test.wantReason)
			}
		})
	}
}

func TestTaskChannelOffDoesNotNotifyWhenMasterIsOn(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	defaults, _ := json.Marshal(DefaultNotificationConfig())
	if err := db.Create(&orm.UserNotificationPreferences{UserID: "owner", Enabled: true, Revision: 1, Defaults: defaults, UpdatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	scheduleID := "schedule"
	config := `{"events":{"succeeded":{"enabled":true,"content":"summary"}},"channels":{"desktop":{"enabled":false}}}`
	task := orm.TaskCenterTask{ID: "run", UserID: "owner", ConversationID: "conversation", TaskType: "scheduled", Status: "running", ScheduleID: &scheduleID, NotificationConfig: &config}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.TaskRunOutput{ID: "output", TaskID: task.ID, ConversationID: task.ConversationID, FinalAnswerText: "完整结果", SummaryText: "摘要", OutputStatus: "ready", ContentHash: "hash", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := UpdateTaskStatus(t.Context(), db, task.ID, "succeeded"); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&orm.TaskNotification{}).Where("task_id = ?", task.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("task channel was off but generated %d notifications", count)
	}
}
