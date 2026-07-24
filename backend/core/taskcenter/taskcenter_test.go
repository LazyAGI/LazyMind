package taskcenter

import (
	"context"
	"path/filepath"
	"testing"

	"lazymind/core/common/orm"
)

func newTestTaskDB(t *testing.T) *orm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.TaskCenterTask{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// ──────────────────────────────────────────────
// CreateTask + CancelTask
// ──────────────────────────────────────────────

func TestCreateTask_And_CancelTask(t *testing.T) {
	db := newTestTaskDB(t)
	ctx := context.Background()

	task := &orm.TaskCenterTask{
		UserID:         "user-1",
		ConversationID: "conv-1",
		TaskType:       "plugin_run",
		Status:         "running",
	}
	if err := CreateTask(ctx, db.DB, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.ID == "" {
		t.Fatal("expected non-empty task ID")
	}

	if err := CancelTask(ctx, db.DB, "user-1", task.ID); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	var got orm.TaskCenterTask
	if err := db.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Status != "canceled" {
		t.Fatalf("expected status=canceled, got %s", got.Status)
	}
	if got.FinishedAt == nil {
		t.Fatal("expected finished_at to be set after cancel")
	}
}

// ──────────────────────────────────────────────
// ListTasks status filter (via DB helper)
// ──────────────────────────────────────────────

func TestListTasks_FilterByStatus(t *testing.T) {
	db := newTestTaskDB(t)
	ctx := context.Background()

	rows := []orm.TaskCenterTask{
		{UserID: "user-2", ConversationID: "conv-2", TaskType: "plugin_run", Status: "running"},
		{UserID: "user-2", ConversationID: "conv-2", TaskType: "plugin_run", Status: "succeeded"},
		{UserID: "user-2", ConversationID: "conv-2", TaskType: "plugin_run", Status: "failed"},
	}
	for i := range rows {
		if err := CreateTask(ctx, db.DB, &rows[i]); err != nil {
			t.Fatalf("seed task %d: %v", i, err)
		}
	}

	// Query only running tasks.
	var running []orm.TaskCenterTask
	if err := db.Where("user_id = ? AND status = ?", "user-2", "running").Find(&running).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(running) != 1 {
		t.Fatalf("expected 1 running task, got %d", len(running))
	}
}
