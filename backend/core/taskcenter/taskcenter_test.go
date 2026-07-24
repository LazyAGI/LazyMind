package taskcenter

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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

func TestArchiveTaskRunHidesRunAndSoftDeletesConversation(t *testing.T) {
	db := newTestTaskDB(t)
	if err := db.AutoMigrate(&orm.UserSchedule{}, &orm.Conversation{}, &orm.TaskRunInput{}); err != nil {
		t.Fatalf("auto migrate related models: %v", err)
	}
	now := time.Now().UTC()
	schedule := orm.UserSchedule{ID: "schedule-1", UserID: "user-1", Name: "周报", CronExpr: "0 9 * * 1", Timezone: "UTC", PromptTemplate: "report", KbIDs: "[]", FileIDs: "[]", Enabled: true, RunCount: 2, NextRunAt: now.Add(7 * 24 * time.Hour), CreatedAt: now}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatal(err)
	}
	conversation := orm.Conversation{ID: "conv-delete", DisplayName: "待删除任务对话", ChannelID: "default", IsTaskConv: true, BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "user-1", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	tasks := []orm.TaskCenterTask{
		{ID: "run-delete", UserID: "user-1", ConversationID: conversation.ID, TaskType: "scheduled", Status: "succeeded", ScheduleID: &schedule.ID, CreatedAt: now, UpdatedAt: now},
		{ID: "run-keep", UserID: "user-1", ConversationID: "conv-keep", TaskType: "scheduled", Status: "succeeded", ScheduleID: &schedule.ID, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := archiveTaskRun(context.Background(), db.DB, "user-1", "run-delete"); err != nil {
		t.Fatalf("archive task run: %v", err)
	}
	var archived orm.TaskCenterTask
	if err := db.First(&archived, "id = ?", "run-delete").Error; err != nil || archived.ArchivedAt == nil {
		t.Fatalf("run was not archived: task=%#v err=%v", archived, err)
	}
	var deletedConversation orm.Conversation
	if err := db.First(&deletedConversation, "id = ?", conversation.ID).Error; err != nil || deletedConversation.DeletedAt == nil {
		t.Fatalf("conversation was not soft-deleted: conversation=%#v err=%v", deletedConversation, err)
	}
	var visibleRuns int64
	if err := db.Model(&orm.TaskCenterTask{}).Where("schedule_id = ? AND archived_at IS NULL", schedule.ID).Count(&visibleRuns).Error; err != nil || visibleRuns != 1 {
		t.Fatalf("expected one visible run, count=%d err=%v", visibleRuns, err)
	}
	var updatedSchedule orm.UserSchedule
	if err := db.First(&updatedSchedule, "id = ?", schedule.ID).Error; err != nil || updatedSchedule.RunCount != 1 {
		t.Fatalf("expected visible run_count=1, schedule=%#v err=%v", updatedSchedule, err)
	}
}

func TestResolveTaskStatusDoesNotTreatStreamingHistoryAsComplete(t *testing.T) {
	db := newTestTaskDB(t)
	if err := db.AutoMigrate(&orm.ChatHistory{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task := orm.TaskCenterTask{ID: "streaming-task", UserID: "user-1", ConversationID: "streaming-conv", TaskType: "background_chat", Status: "running", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	history := orm.ChatHistory{ID: "progress-history", Seq: 1, ConversationID: task.ConversationID, Result: "<think>still working</think>", TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	if got := resolveTaskStatus(context.Background(), db.DB, task); got != "running" {
		t.Fatalf("streaming progress must remain running, got %q", got)
	}
}
