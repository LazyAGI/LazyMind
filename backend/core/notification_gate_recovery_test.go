package main

import (
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow"
)

func TestNotificationGateReopenDoesNotReplayRecoveredCompletions(t *testing.T) {
	for _, gate := range []string{"master", "desktop"} {
		for _, kind := range []string{"background_chat", "workflow_run", "workflow_callback", "scheduled"} {
			t.Run(gate+"/"+kind, func(t *testing.T) {
				a := newNotificationAPI(t)
				prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
				defaults := notificationObject(t, prefs["defaults"])
				channels := notificationObject(t, defaults["channels"])
				patch := map[string]any{"revision": prefs["revision"]}
				if gate == "master" {
					patch["enabled"] = false
				} else {
					channels["desktop"] = map[string]any{"enabled": false}
					patch["defaults"] = defaults
				}
				closed := a.data("PATCH", "/user/notification-preferences", "owner", patch)
				now := time.Now().UTC()
				taskKind := kind
				if kind == "workflow_callback" {
					taskKind = "workflow_run"
				}
				task := orm.TaskCenterTask{ID: "recovered", UserID: "owner", ConversationID: "conversation", TaskType: taskKind, Status: "running", CreatedAt: now.Add(-time.Second)}
				if taskKind == "workflow_run" {
					sessionID := "session"
					task.WorkflowSessionID = &sessionID
					if err := a.db.Create(&orm.WorkflowSession{ID: sessionID, CreateUserID: "owner", Status: "completed", CreatedAt: task.CreatedAt, UpdatedAt: now}).Error; err != nil {
						t.Fatal(err)
					}
				}
				if kind == "scheduled" {
					a.schedule("schedule", false)
					scheduleID := "schedule"
					task.ScheduleID = &scheduleID
					// Use a selected desktop policy even while its global gate is closed.
					raw := `{"events":{"succeeded":{"enabled":true,"content":"summary"},"failed":{"enabled":true,"content":"summary"},"waiting":{"enabled":false,"content":"summary"}},"channels":{"desktop":{"enabled":true}}}`
					task.NotificationConfig = &raw
				}
				if err := a.db.Create(&task).Error; err != nil {
					t.Fatal(err)
				}
				if err := a.db.Create(&orm.ChatHistory{ID: "history", ConversationID: task.ConversationID, Seq: 1, RunStatus: "completed", Result: "result", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}}).Error; err != nil {
					t.Fatal(err)
				}
				// The authoritative completion survived an interrupted callback. Reopening
				// must not turn that old completion into a newly deliverable event.
				patch = map[string]any{"revision": closed["revision"]}
				if gate == "master" {
					patch["enabled"] = true
				} else {
					channels["desktop"] = map[string]any{"enabled": true}
					patch["defaults"] = defaults
				}
				a.data("PATCH", "/user/notification-preferences", "owner", patch)
				if kind == "scheduled" {
					taskcenter.FinalizeScheduledOutput(t.Context(), a.db.DB, task.ID, task.ConversationID)
				} else if kind == "workflow_callback" {
					if err := taskcenter.UpdateTaskStatusBySession(t.Context(), a.db.DB, *task.WorkflowSessionID, "succeeded"); err != nil {
						t.Fatal(err)
					}
				} else if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), a.db.DB, ""); err != nil {
					t.Fatal(err)
				}
				var notice orm.TaskNotification
				if err := a.db.First(&notice, "task_id = ?", task.ID).Error; err != nil {
					t.Fatal(err)
				}
				if notice.Status != "skipped" {
					t.Fatalf("replayed %s completion after %s reopened: %#v", kind, gate, notice)
				}
				future := orm.TaskCenterTask{ID: "future", UserID: "owner", ConversationID: "future-conversation", TaskType: "background_chat", Status: "running", ProgressJSON: taskcenter.NewBackgroundExecutionProgress(nil)}
				if err := a.db.Create(&orm.Conversation{ID: future.ConversationID, BaseModel: orm.BaseModel{CreateUserID: future.UserID}}).Error; err != nil {
					t.Fatal(err)
				}
				if err := taskcenter.CreateTask(t.Context(), a.db.DB, &future); err != nil {
					t.Fatal(err)
				}
				if err := taskcenter.BindBackgroundExecution(t.Context(), a.db.DB, future.ID, future.ConversationID, []taskcenter.BackgroundExecutionPart{{HistoryID: "future-history", RunID: "future-run"}}); err != nil {
					t.Fatal(err)
				}
				futureAt := time.Now().UTC()
				if err := a.db.Create(&orm.ChatHistory{ID: "future-history", ConversationID: future.ConversationID, RunID: "future-run", RunStatus: "completed", Seq: 1, TimeMixin: orm.TimeMixin{CreateTime: futureAt, UpdateTime: futureAt}}).Error; err != nil {
					t.Fatal(err)
				}
				if err := taskcenter.FinalizeBackgroundExecution(t.Context(), a.db.DB, future.ConversationID, "future-history", "future-run"); err != nil {
					t.Fatal(err)
				}
				notice = orm.TaskNotification{}
				if err := a.db.First(&notice, "task_id = ?", future.ID).Error; err != nil {
					t.Fatal(err)
				}
				if notice.Status != "pending" {
					t.Fatalf("future completion stayed suppressed: %#v", notice)
				}
			})
		}
	}
}

func TestNotificationGateReopenDoesNotReplayRecoveredWaitingAction(t *testing.T) {
	a := newNotificationAPI(t)
	path := a.schedule("schedule", false)
	config := notificationConfig(true)
	notificationObject(t, notificationObject(t, config["events"])["waiting"])["enabled"] = true
	a.save(path, config)
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	defaults := notificationObject(t, prefs["defaults"])
	channels := notificationObject(t, defaults["channels"])
	channels["desktop"] = map[string]any{"enabled": false}
	closed := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "defaults": defaults})
	scheduleID := "schedule"
	task := orm.TaskCenterTask{ID: "waiting-task", UserID: "owner", ConversationID: "conversation", TaskType: "scheduled", Status: "running", ScheduleID: &scheduleID}
	if err := taskcenter.CreateTask(t.Context(), a.db.DB, &task); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := a.db.Create(&orm.ChatHistory{ID: "history", ConversationID: task.ConversationID, Seq: 1, RunStatus: "completed", Ext: []byte(`{"ask_pending":{"ask_id":"action","title":"Review result"}}`), TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}}).Error; err != nil {
		t.Fatal(err)
	}
	channels["desktop"] = map[string]any{"enabled": true}
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": closed["revision"], "defaults": defaults})
	taskcenter.FinalizeScheduledOutput(t.Context(), a.db.DB, task.ID, task.ConversationID)
	var notice orm.TaskNotification
	if err := a.db.First(&notice, "task_id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if notice.Event != "waiting" || notice.Status != "skipped" {
		t.Fatalf("old waiting action was replayed after reopen: %#v", notice)
	}
	if err := taskcenter.WaitScheduledTask(t.Context(), a.db.DB, task.ID, "new-action", "Review new result"); err != nil {
		t.Fatal(err)
	}
	var notices []orm.TaskNotification
	if err := a.db.Where("task_id = ? AND status = ?", task.ID, "pending").Find(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].Event != "waiting" {
		t.Fatalf("future action did not resume: %#v", notices)
	}
}

func TestNotificationDeliveryBarriersStayServerOwned(t *testing.T) {
	a := newNotificationAPI(t)
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	closed := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "enabled": false})
	reopened := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": closed["revision"], "enabled": true})
	view := a.request("GET", "/user/notification-preferences", "owner", nil)
	if strings.Contains(view.Body.String(), "_delivery_barriers") {
		t.Fatal("private delivery metadata leaked into preferences contract")
	}
	defaults := notificationObject(t, reopened["defaults"])
	defaults["_delivery_barriers"] = map[string]any{"desktop": map[string]any{"before": "2099-01-01T00:00:00Z", "reason": "NOTIFICATIONS_DISABLED"}}
	response := a.request("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": reopened["revision"], "defaults": defaults})
	notificationError(t, response, 422, "INVALID_REQUEST")
}

func TestNotificationGateReopenDoesNotReplayDismissedWorkflow(t *testing.T) {
	for _, callback := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovery", true: "callback"}[callback], func(t *testing.T) {
			a := newNotificationAPI(t)
			prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
			closed := a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "enabled": false})
			sessionID := "session"
			if err := a.db.Create(&orm.WorkflowSession{ID: sessionID, CreateUserID: "owner", Status: "active"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := a.db.Create(&orm.TaskCenterTask{ID: "task", UserID: "owner", TaskType: "workflow_run", Status: "running", WorkflowSessionID: &sessionID}).Error; err != nil {
				t.Fatal(err)
			}
			if err := workflow.UpdateSessionStatus(t.Context(), a.db.DB, sessionID, "completed"); err != nil {
				t.Fatal(err)
			}
			a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": closed["revision"], "enabled": true})
			if err := workflow.DismissSession(t.Context(), a.db.DB, sessionID); err != nil {
				t.Fatal(err)
			}
			if callback {
				if err := taskcenter.UpdateTaskStatusBySession(t.Context(), a.db.DB, sessionID, "succeeded"); err != nil {
					t.Fatal(err)
				}
			} else if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), a.db.DB, ""); err != nil {
				t.Fatal(err)
			}
			var notice orm.TaskNotification
			if err := a.db.First(&notice, "task_id = ?", "task").Error; err != nil {
				t.Fatal(err)
			}
			if notice.Status != "skipped" {
				t.Fatalf("dismissal moved old workflow completion across reopen: %#v", notice)
			}
		})
	}
}

func TestNotificationTimingEditDoesNotSuppressRecoveredCompletion(t *testing.T) {
	a := newNotificationAPI(t)
	now := time.Now().UTC()
	task := orm.TaskCenterTask{ID: "task", UserID: "owner", ConversationID: "conversation", TaskType: "background_chat", Status: "running", CreatedAt: now.Add(-time.Second)}
	if err := a.db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&orm.ChatHistory{ID: "history", ConversationID: task.ConversationID, Seq: 1, RunStatus: "completed", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}}).Error; err != nil {
		t.Fatal(err)
	}
	prefs := a.data("GET", "/user/notification-preferences", "owner", nil)
	defaults := notificationObject(t, prefs["defaults"])
	notificationObject(t, notificationObject(t, defaults["events"])["failed"])["content"] = "full"
	a.data("PATCH", "/user/notification-preferences", "owner", map[string]any{"revision": prefs["revision"], "defaults": defaults})
	if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), a.db.DB, ""); err != nil {
		t.Fatal(err)
	}
	var notice orm.TaskNotification
	if err := a.db.First(&notice, "task_id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if notice.Status != "pending" {
		t.Fatalf("unrelated setting edit suppressed an enabled completion: %#v", notice)
	}
}
