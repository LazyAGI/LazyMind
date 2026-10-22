package taskcenter

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestWorkflowNotificationCompletionUsesCurrentDurableTransition(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	session := orm.WorkflowSession{ID: "session", CreateUserID: "owner", Status: "failed", CreatedAt: start, UpdatedAt: time.Now().UTC()}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: "replacement", SessionID: session.ID, StepID: "write", Attempt: 2, Status: "failed", Validity: "effective", CreatedAt: start.Add(2 * time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	completedAt := start.Add(3 * time.Minute)
	for _, event := range []orm.WorkflowEvent{
		{SessionID: session.ID, EventType: "workflow.patch", PayloadJSON: []byte(`{"status":"failed"}`), CreatedAt: start.Add(time.Minute)},
		{SessionID: session.ID, EventType: "workflow.patch", PayloadJSON: []byte(`{"status":"failed"}`), CreatedAt: completedAt},
		{SessionID: session.ID, EventType: "workflow.snapshot", PayloadJSON: []byte(`{"status":"failed"}`), CreatedAt: time.Now().UTC()},
	} {
		if err := db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	at, err := WorkflowNotificationCompletedAt(t.Context(), db, session)
	if err != nil {
		t.Fatal(err)
	}
	if !at.Equal(completedAt) {
		t.Fatalf("metadata/replayed snapshots moved completion: %v want %v", at, completedAt)
	}
}

func TestWorkflowCallbackDoesNotEndActiveReplacement(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	sessionID := "session"
	if err := db.Create(&orm.WorkflowSession{ID: sessionID, CreateUserID: "owner", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.TaskCenterTask{ID: "task", UserID: "owner", TaskType: "workflow_run", WorkflowSessionID: &sessionID, Status: "running"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"failed", "succeeded"} {
		if err := UpdateTaskStatusBySession(t.Context(), db, sessionID, status); err != nil {
			t.Fatal(err)
		}
	}
	var task orm.TaskCenterTask
	if err := db.First(&task, "id = ?", "task").Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&orm.TaskNotification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "running" || task.FinishedAt != nil || count != 0 {
		t.Fatalf("old callback ended active replacement: %#v notices=%d", task, count)
	}
}
