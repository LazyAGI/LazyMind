package taskcenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

// Notifications traverse task completion, workflow/history recovery and scheduled
// output collection. Keep those tables together; unrelated application tables
// are covered by the migration suite rather than every notification scenario.
func newNotificationTestDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t,
		&orm.TaskCenterTask{}, &orm.TaskNotification{}, &orm.UserNotificationPreferences{},
		&orm.DesktopNotificationReceipt{}, &orm.UserSchedule{}, &orm.TaskRunOutput{},
		&orm.Conversation{}, &orm.ChatHistory{}, &orm.MultiAnswersChatHistory{},
		&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{},
		&orm.ConversationArtifact{}, &orm.SubAgentTask{}, &orm.SubAgentArtifact{},
	)
}

func TestOrdinaryTaskCompletionUsesOnlyDesktopAndCurrentGate(t *testing.T) {
	for _, taskType := range []string{"workflow_run", "background_chat"} {
		for _, event := range []string{"succeeded", "failed"} {
			for _, gate := range []string{"on", "master off", "desktop off"} {
				t.Run(fmt.Sprintf("%s/%s/%s", taskType, event, gate), func(t *testing.T) {
					db := newNotificationTestDB(t).DB
					defaults := DefaultNotificationConfig()
					defaults.Events["succeeded"] = NotificationEventRule{Enabled: false, Content: "full"}
					defaults.Events["failed"] = NotificationEventRule{Enabled: false, Content: "full"}
					defaults.Channels["desktop"] = NotificationChannelRule{Enabled: gate != "desktop off"}
					for _, provider := range []string{"feishu", "wecom", "wechat"} {
						defaults.Channels[provider] = NotificationChannelRule{Enabled: true, AccountID: "account", RecipientID: "recipient"}
					}
					raw, _ := json.Marshal(defaults)
					prefs := orm.UserNotificationPreferences{UserID: "owner", Enabled: true, Revision: 4, Defaults: raw}
					if err := db.Create(&prefs).Error; err != nil {
						t.Fatal(err)
					}
					if gate == "master off" {
						if err := db.Model(&prefs).Update("enabled", false).Error; err != nil {
							t.Fatal(err)
						}
					}
					sessionID := "session"
					title := strings.Repeat("工作流", 90)
					config := string(raw)
					task := orm.TaskCenterTask{ID: "ordinary", UserID: "owner", ConversationID: "conversation", TaskType: taskType,
						WorkflowSessionID: &sessionID, Title: &title, Status: "running", NotificationConfig: &config,
						ProgressJSON: []byte(`{"failure_reason":"private provider diagnostics"}`)}
					if err := CreateTask(t.Context(), db, &task); err != nil {
						t.Fatal(err)
					}
					for range 2 {
						if err := UpdateTaskStatusBySession(t.Context(), db, sessionID, event); err != nil {
							t.Fatal(err)
						}
					}
					var notices []orm.TaskNotification
					if err := db.Where("task_id = ?", task.ID).Find(&notices).Error; err != nil {
						t.Fatal(err)
					}
					if len(notices) != 1 {
						t.Fatalf("completion notices = %#v, want one desktop notice", notices)
					}
					notice := notices[0]
					if notice.Channel != "desktop" || notice.ScheduleID != "" || notice.Event != event || notice.AccountID != "" || notice.RecipientID != "" {
						t.Fatalf("ordinary task used scheduled or external configuration: %#v", notice)
					}
					wantStatus, wantReason := "pending", ""
					if gate == "master off" {
						wantStatus, wantReason = "skipped", "NOTIFICATIONS_DISABLED"
					}
					if gate == "desktop off" {
						wantStatus, wantReason = "skipped", "NOTIFICATION_CHANNEL_DISABLED"
					}
					if notice.Status != wantStatus || notice.Reason != wantReason {
						t.Fatalf("status=%s reason=%s", notice.Status, notice.Reason)
					}
					if strings.TrimSpace(notice.Body) == "" || strings.Contains(notice.Body, "private") || len([]rune(notice.Body)) > 200 || len([]rune(notice.Title)) > 200 {
						t.Fatalf("unsafe or unbounded desktop notice: %#v", notice)
					}
				})
			}
		}
	}
}

func TestOrdinaryTaskCompletionPreservesTerminalAndArchiveBoundaries(t *testing.T) {
	for _, state := range []string{"canceled", "skipped", "succeeded", "failed", "archived", "waiting"} {
		t.Run(state, func(t *testing.T) {
			db := newNotificationTestDB(t).DB
			sessionID := "session"
			task := orm.TaskCenterTask{ID: "ordinary", UserID: "owner", TaskType: "workflow_run", WorkflowSessionID: &sessionID, Status: state}
			if state == "archived" {
				now := time.Now().UTC()
				task.Status, task.ArchivedAt = "running", &now
			}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			event := "failed"
			if state == "waiting" {
				event = "waiting"
			}
			if err := UpdateTaskStatusBySession(t.Context(), db, sessionID, event); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.Model(&orm.TaskNotification{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("created %d notices for %s", count, state)
			}
		})
	}
}

func TestUnresolvedScheduleDefaultCreatesSkippedNoticeWithoutDispatch(t *testing.T) {
	db := newNotificationTestDB(t).DB
	defaults := DefaultNotificationConfig()
	defaults.Channels["wechat"] = NotificationChannelRule{Enabled: true}
	raw, _ := json.Marshal(defaults)
	if err := db.Create(&orm.UserNotificationPreferences{UserID: "owner", Enabled: true, Revision: 1, Defaults: raw}).Error; err != nil {
		t.Fatal(err)
	}
	schedule := orm.UserSchedule{ID: "schedule", UserID: "owner"}
	if err := InitializeScheduleNotifications(t.Context(), db, &schedule); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.TaskCenterTask{ID: "run", UserID: "owner", TaskType: "scheduled", ScheduleID: &schedule.ID, Status: "running"}
	if err := CreateTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	if err := UpdateTaskFailure(t.Context(), db, task.ID, "任务执行被中断"); err != nil {
		t.Fatal(err)
	}
	var notice orm.TaskNotification
	if err := db.First(&notice, "task_id = ? AND channel = ?", task.ID, "wechat").Error; err != nil {
		t.Fatalf("unresolved selected channel has no audit record: %v", err)
	}
	if notice.Status != "skipped" || notice.Reason != "NOTIFICATION_TARGET_REQUIRED" {
		t.Fatalf("unresolved default remained dispatchable: %#v", notice)
	}
	if err := DispatchNotifications(t.Context(), db); err != nil {
		t.Fatal(err)
	}
}

func TestUnconfiguredScheduleCompletionUsesDesktopWithoutOutputOrExternalPolicy(t *testing.T) {
	for _, event := range []string{"succeeded", "failed"} {
		t.Run(event, func(t *testing.T) {
			db := newNotificationTestDB(t).DB
			defaults := DefaultNotificationConfig()
			defaults.Channels["feishu"] = NotificationChannelRule{Enabled: true, AccountID: "account", RecipientID: "recipient"}
			raw, _ := json.Marshal(defaults)
			if err := db.Create(&orm.UserNotificationPreferences{UserID: "owner", Enabled: true, Revision: 1, Defaults: raw}).Error; err != nil {
				t.Fatal(err)
			}
			scheduleID := "legacy-schedule"
			task := orm.TaskCenterTask{ID: "legacy-run", UserID: "owner", TaskType: "scheduled", ScheduleID: &scheduleID, Status: "running"}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			if err := UpdateTaskStatus(t.Context(), db, task.ID, event); err != nil {
				t.Fatal(err)
			}
			var notices []orm.TaskNotification
			if err := db.Where("task_id = ?", task.ID).Find(&notices).Error; err != nil {
				t.Fatal(err)
			}
			if len(notices) != 1 || notices[0].Channel != "desktop" || notices[0].ScheduleID != scheduleID || notices[0].Event != event || notices[0].Status != "pending" {
				t.Fatalf("unconfigured completion did not receive only desktop notice: %#v", notices)
			}
			if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if task.NotificationConfig != nil || task.NotificationRevision != 0 {
				t.Fatalf("fallback rewrote saved task policy: %#v", task)
			}
		})
	}
}

func TestOrdinaryCompletionIdentityChangesAfterRetryButNotCallbackReplay(t *testing.T) {
	db := newNotificationTestDB(t).DB
	task := orm.TaskCenterTask{ID: "workflow", UserID: "owner", TaskType: "workflow_run", Status: "running"}
	if err := CreateTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	for execution := range 2 {
		if execution > 0 {
			if err := db.Model(&task).Updates(map[string]any{"status": "running", "finished_at": nil}).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := UpdateTaskStatus(t.Context(), db, task.ID, "failed"); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
			t.Fatal(err)
		}
		finished := *task.FinishedAt
		if err := UpdateTaskStatus(t.Context(), db, task.ID, "failed"); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
			t.Fatal(err)
		}
		if task.FinishedAt == nil || !task.FinishedAt.Equal(finished) {
			t.Fatalf("replayed terminal callback changed persisted completion time: before=%v after=%v", finished, task.FinishedAt)
		}
		var notices []orm.TaskNotification
		if err := db.Where("task_id = ?", task.ID).Find(&notices).Error; err != nil {
			t.Fatal(err)
		}
		if len(notices) != execution+1 {
			t.Fatalf("execution %d notices=%#v, want %d independent completions", execution+1, notices, execution+1)
		}
	}
}

func TestOrdinaryFailureReplayPreservesCompletionUntilExplicitRetry(t *testing.T) {
	db := newNotificationTestDB(t).DB
	task := orm.TaskCenterTask{ID: "workflow", UserID: "owner", TaskType: "workflow_run", Status: "running"}
	if err := CreateTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	for execution := range 2 {
		if execution > 0 {
			if err := db.Model(&task).Updates(map[string]any{"status": "running", "finished_at": nil}).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := UpdateTaskFailure(t.Context(), db, task.ID, "failure"); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
			t.Fatal(err)
		}
		finished := *task.FinishedAt
		if err := UpdateTaskFailure(t.Context(), db, task.ID, "duplicate failure"); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Model(&orm.TaskNotification{}).Where("task_id = ?", task.ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if task.FinishedAt == nil || !task.FinishedAt.Equal(finished) || count != int64(execution+1) {
			t.Fatalf("failure replay changed occurrence: finished=%v want=%v notices=%d", task.FinishedAt, finished, count)
		}
	}
}

func TestScheduledRetryHasNewOccurrenceButCallbackReplayDoesNot(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, event := range []string{"succeeded", "failed"} {
			t.Run(fmt.Sprintf("configured=%v/%s", configured, event), func(t *testing.T) {
				db := newNotificationTestDB(t).DB
				scheduleID := "schedule"
				task := orm.TaskCenterTask{ID: "scheduled", UserID: "owner", TaskType: "scheduled", ScheduleID: &scheduleID, Status: "running"}
				if configured {
					config := `{"events":{"succeeded":{"enabled":true,"content":"summary"},"failed":{"enabled":true,"content":"summary"}},"channels":{"desktop":{"enabled":true}}}`
					task.NotificationConfig = &config
				}
				if err := db.Create(&task).Error; err != nil {
					t.Fatal(err)
				}
				if event == "succeeded" {
					if err := db.Create(&orm.TaskRunOutput{ID: "output", TaskID: task.ID, OutputStatus: "ready", FinalAnswerText: "result"}).Error; err != nil {
						t.Fatal(err)
					}
				}
				for execution := range 2 {
					if execution > 0 {
						if err := db.Model(&task).Updates(map[string]any{"status": "running", "finished_at": nil}).Error; err != nil {
							t.Fatal(err)
						}
					}
					complete := func() error {
						if event == "failed" {
							return UpdateTaskFailure(t.Context(), db, task.ID, "任务执行被中断")
						}
						return UpdateTaskStatus(t.Context(), db, task.ID, event)
					}
					if err := complete(); err != nil {
						t.Fatal(err)
					}
					if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
						t.Fatal(err)
					}
					finished := *task.FinishedAt
					if err := complete(); err != nil {
						t.Fatal(err)
					}
					if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
						t.Fatal(err)
					}
					var count int64
					if err := db.Model(&orm.TaskNotification{}).Where("task_id = ?", task.ID).Count(&count).Error; err != nil {
						t.Fatal(err)
					}
					if task.FinishedAt == nil || !task.FinishedAt.Equal(finished) || count != int64(execution+1) {
						t.Fatalf("scheduled execution %d replay/occurrence mismatch: finished=%v want=%v notices=%d", execution, task.FinishedAt, finished, count)
					}
				}
			})
		}
	}
}
