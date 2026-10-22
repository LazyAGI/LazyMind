package taskcenter

import (
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"testing"
	"time"
)

func TestBackgroundExecutionRecoveryRejectsForeignRunAndUnboundScope(t *testing.T) {
	for _, scenario := range []string{"foreign-run", "foreign-owner", "unbound"} {
		t.Run(scenario, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			now := time.Now().UTC()
			owner := "owner"
			if scenario == "foreign-owner" {
				owner = "different"
			}
			if err := db.Create(&orm.Conversation{ID: "conversation", BaseModel: orm.BaseModel{CreateUserID: owner}}).Error; err != nil {
				t.Fatal(err)
			}
			scope := map[string]any{"version": 1, "parts": []any{map[string]any{"history_id": "history", "run_id": "expected"}}}
			if scenario == "unbound" {
				scope["parts"] = []any{}
			}
			progress, _ := json.Marshal(map[string]any{"_background_execution": scope})
			task := orm.TaskCenterTask{ID: "task", UserID: "owner", ConversationID: "conversation", TaskType: "background_chat", Status: "running", CreatedAt: now.Add(-time.Minute), ProgressJSON: progress}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			runID := "expected"
			if scenario == "foreign-run" {
				runID = "different"
			}
			if err := db.Create(&orm.ChatHistory{ID: "history", ConversationID: "conversation", RunID: runID, RunStatus: "completed", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
				t.Fatal(err)
			}
			if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if task.Status != "running" {
				t.Fatalf("unproven execution completed task: %s", task.Status)
			}
		})
	}
}

func TestBackgroundExecutionLegacyWindowAndAmbiguity(t *testing.T) {
	for _, scenario := range []string{"archived-boundary", "terminal-boundary", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			now := time.Now().UTC()
			task := orm.TaskCenterTask{ID: "old", UserID: "owner", ConversationID: "conv", TaskType: "background_chat", Status: "running", CreatedAt: now.Add(-3 * time.Minute)}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			if scenario != "ambiguous" {
				boundary := orm.TaskCenterTask{ID: "next", UserID: "owner", ConversationID: "conv", TaskType: "background_chat", Status: "succeeded", CreatedAt: now.Add(-2 * time.Minute)}
				if scenario == "archived-boundary" {
					boundary.ArchivedAt = &now
				}
				if err := db.Create(&boundary).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				if err := db.Create(&orm.ChatHistory{ID: "first", ConversationID: "conv", Seq: 1, RunStatus: "failed", TimeMixin: orm.TimeMixin{CreateTime: now.Add(-time.Minute), UpdateTime: now.Add(-time.Minute)}}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&orm.ChatHistory{ID: "second", ConversationID: "conv", Seq: 2, RunStatus: "completed", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}}).Error; err != nil {
				t.Fatal(err)
			}
			if err := FinalizeBackgroundTasks(t.Context(), db, "conv", "succeeded", now); err != nil {
				t.Fatal(err)
			}
			if _, err := ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
				t.Fatal(err)
			}
			if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if task.Status != "running" {
				t.Fatalf("legacy task completed without unique origin in its window: %s", task.Status)
			}
		})
	}
}

func TestBackgroundExecutionBindingPreservesProgressAndRejectsRebinding(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	if err := db.Create(&orm.Conversation{ID: "conv", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	task := orm.TaskCenterTask{ID: "task", UserID: "owner", ConversationID: "conv", TaskType: "background_chat", Status: "running", ProgressJSON: NewBackgroundExecutionProgress(orm.RawJSON(`{"keep":{"nested":true}}`))}
	if err := CreateTask(t.Context(), db, &task); err != nil {
		t.Fatal(err)
	}
	parts := []BackgroundExecutionPart{{HistoryID: "history", RunID: "run"}}
	for range 2 {
		if err := BindBackgroundExecution(t.Context(), db, task.ID, task.ConversationID, parts); err != nil {
			t.Fatal(err)
		}
	}
	if err := BindBackgroundExecution(t.Context(), db, task.ID, task.ConversationID, []BackgroundExecutionPart{{HistoryID: "other", RunID: "other"}}); err == nil {
		t.Fatal("conflicting rebinding accepted")
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(task.ProgressJSON, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["keep"].(map[string]any)["nested"] != true {
		t.Fatalf("progress fields lost: %s", task.ProgressJSON)
	}
	foreign := orm.TaskCenterTask{ID: "foreign", UserID: "other", ConversationID: "conv", TaskType: "background_chat", Status: "running", ProgressJSON: NewBackgroundExecutionProgress(nil)}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	if err := BindBackgroundExecution(t.Context(), db, foreign.ID, foreign.ConversationID, parts); err == nil {
		t.Fatal("foreign owner bound")
	}
}

func TestBackgroundExecutionProjectionRechecksWorkflowBoundary(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "conv", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	progress := withBackgroundExecution(nil, backgroundExecution{Version: 1, Parts: []BackgroundExecutionPart{{HistoryID: "history", RunID: "run", Status: "succeeded", CompletedAt: &now}}})
	task := orm.TaskCenterTask{ID: "task", UserID: "owner", ConversationID: "conv", TaskType: "background_chat", Status: "running", CreatedAt: now.Add(-time.Minute), ProgressJSON: progress}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	linked := false
	if err := db.Callback().Query().After("gorm:query").Register("background:link-before-projection", func(tx *gorm.DB) {
		if tx.Statement.Table != "task_center_tasks" || linked {
			return
		}
		linked = true
		if err := db.Session(&gorm.Session{NewDB: true}).Model(&orm.TaskCenterTask{}).Where("id = ?", task.ID).Update("plugin_session_id", "workflow").Error; err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove("background:link-before-projection")
	if err := FinalizeBackgroundTasks(t.Context(), db, task.ConversationID, "succeeded", now); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "running" || task.FinishedAt != nil {
		t.Fatalf("background projection crossed newly linked workflow boundary: %#v", task)
	}
	var notices int64
	if err := db.Model(&orm.TaskNotification{}).Where("task_id = ?", task.ID).Count(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if notices != 0 {
		t.Fatalf("linked workflow received %d premature notices", notices)
	}
}
