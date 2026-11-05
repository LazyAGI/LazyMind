package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func updateActivityDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "update-activity.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, model := range []any{
		&orm.TaskCenterTask{}, &orm.AsyncJob{}, &orm.SubAgentTask{},
		&orm.ExternalChatRun{}, &orm.ChatHistory{}, &orm.MultiAnswersChatHistory{},
		&orm.Task{}, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.ResourceUpdateTask{},
	} {
		if err := db.AutoMigrate(model); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestDesktopUpdateResourceWorkIsNotLostAfterHTTPReturns(t *testing.T) {
	for _, kind := range []string{
		orm.ResourceUpdateTaskTypeOrganizePreference,
		orm.ResourceUpdateTaskTypeGenerateReview,
	} {
		t.Run(kind, func(t *testing.T) {
			db := updateActivityDB(t)
			now := time.Now().UTC()
			until := now.Add(time.Minute)
			resourceType := orm.ResourceUpdateResourceTypeMemory
			if kind == orm.ResourceUpdateTaskTypeOrganizePreference {
				resourceType = orm.ResourceUpdateResourceTypeUserPreference
			}
			task := orm.ResourceUpdateTask{
				ID: "claimed-resource-task", UserID: "fixture-owner", TaskType: kind,
				ResourceType: resourceType,
				Status:       orm.ResourceUpdateTaskStatusRunning, RunID: "fixture-run",
				LockedBy: "fixture-worker", LockedUntil: &until, StartedAt: &now, CreatedAt: now, UpdatedAt: now,
				RequestJSON: json.RawMessage("{}"),
			}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			active, err := desktopUpdateTaskCount(context.Background(), db, nil, false)
			if err == nil && active == 0 {
				t.Fatal("claimed resource work was reported idle before final task persistence")
			}
			if err := db.Model(&task).Updates(map[string]any{"status": "done", "finished_at": now}).Error; err != nil {
				t.Fatal(err)
			}
			active, err = desktopUpdateTaskCount(context.Background(), db, nil, false)
			if err != nil || active != 0 {
				t.Fatalf("completed resource task still blocks: active=%d err=%v", active, err)
			}
		})
	}
}

func TestDesktopUpdatePendingResourceQueueCanWaitAcrossInstall(t *testing.T) {
	db := updateActivityDB(t)
	now := time.Now().UTC()
	task := orm.ResourceUpdateTask{
		ID: "queued-resource-task", UserID: "fixture-owner",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeMemory,
		Status:       orm.ResourceUpdateTaskStatusPending, NextRunAt: now,
		CreatedAt: now, UpdatedAt: now, RequestJSON: json.RawMessage("{}"),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	n, err := desktopUpdateTaskCount(context.Background(), db, nil, false)
	if err != nil || n != 0 {
		t.Fatalf("unclaimed durable queue is not an executing task: active=%d err=%v", n, err)
	}
	var after orm.ResourceUpdateTask
	if err := db.First(&after, "id = ?", task.ID).Error; err != nil || after.Status != "pending" || after.AttemptCount != 0 {
		t.Fatalf("observation changed pending work: %#v %v", after, err)
	}
}

func TestDesktopUpdateWorkflowUsesExecutionFactsAndPreservesRetryState(t *testing.T) {
	for _, sample := range []struct {
		name, session, attempt, validity string
		active                           bool
	}{
		{"stopped", "waiting", "interrupted", "effective", false},
		{"stopped-with-stale-running-attempt", "stopped", "running", "stale", false},
		{"pausing", "pausing", "interrupted", "effective", true},
		{"live", "active", "running", "effective", true},
		{"claimed", "active", "claimed", "effective", true},
		{"preparing-without-attempt", "active", "", "", true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := updateActivityDB(t)
			now := time.Now().UTC().Add(-time.Minute)
			session := orm.WorkflowSession{
				ID: "fixture-workflow", WorkflowID: "fixture-writer", ConversationID: "fixture-conversation",
				CreateUserID: "fixture-owner", Status: sample.session, CreatedAt: now, UpdatedAt: now,
			}
			task := orm.TaskCenterTask{
				ID: "fixture-center-task", UserID: "fixture-owner", TaskType: "workflow_run",
				WorkflowSessionID: &session.ID, ConversationID: session.ConversationID,
				Status: "running", CreatedAt: now, UpdatedAt: now,
			}
			for _, value := range []any{&session, &task} {
				if err := db.Create(value).Error; err != nil {
					t.Fatal(err)
				}
			}
			if sample.attempt != "" {
				step := orm.WorkflowSessionStep{
					ID: "fixture-attempt", SessionID: session.ID, StepID: "write", TaskID: "fixture-executor-task",
					Status: sample.attempt, Attempt: 1, Validity: sample.validity,
					CreatedAt: now, UpdatedAt: now, TerminalCode: "WORKFLOW_STOPPED",
				}
				if sample.active && sample.session != "pausing" {
					step.TerminalCode = ""
				}
				if err := db.Create(&step).Error; err != nil {
					t.Fatal(err)
				}
			}
			n, err := desktopUpdateTaskCount(context.Background(), db, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if (n > 0) != sample.active {
				t.Fatalf("workflow %s: active=%d, want executing=%v", sample.name, n, sample.active)
			}
			var persisted orm.TaskCenterTask
			if err := db.First(&persisted, "id = ?", task.ID).Error; err != nil || persisted.Status != "running" {
				t.Fatalf("update inspection rewrote recoverable history: %#v %v", persisted, err)
			}
		})
	}
}

func TestDesktopUpdateUnknownWorkflowCannotBeTreatedAsStopped(t *testing.T) {
	db := updateActivityDB(t)
	missing := "missing-session"
	task := orm.TaskCenterTask{
		ID: "fixture-unknown-task", UserID: "fixture-owner", TaskType: "workflow_run",
		Status: "running", WorkflowSessionID: &missing,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	n, err := desktopUpdateTaskCount(context.Background(), db, nil, false)
	if err == nil && n == 0 {
		t.Fatal("missing workflow facts must not authorize an update")
	}
}
