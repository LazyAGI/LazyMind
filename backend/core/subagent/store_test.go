package subagent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func newTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/subagent.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.SubAgentTask{}, &orm.SubAgentStep{}, &orm.SubAgentArtifact{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func TestCreateTaskAllocatesSequentialSeq(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	t1, err := CreateTask(ctx, db.DB, CreateTaskInput{
		TaskID: "task-1", ConversationID: "conv-1", AgentType: "image_generation",
		Title: "生图A", Mode: "auto",
	})
	if err != nil {
		t.Fatalf("create task 1: %v", err)
	}
	if t1.SeqInConversation != 1 {
		t.Fatalf("expected seq 1, got %d", t1.SeqInConversation)
	}

	t2, err := CreateTask(ctx, db.DB, CreateTaskInput{
		TaskID: "task-2", ConversationID: "conv-1", AgentType: "research",
		Title: "调研B", Mode: "manual",
	})
	if err != nil {
		t.Fatalf("create task 2: %v", err)
	}
	if t2.SeqInConversation != 2 {
		t.Fatalf("expected seq 2, got %d", t2.SeqInConversation)
	}

	// A different conversation restarts at 1.
	t3, err := CreateTask(ctx, db.DB, CreateTaskInput{
		TaskID: "task-3", ConversationID: "conv-2", AgentType: "research",
		Title: "C", Mode: "auto",
	})
	if err != nil {
		t.Fatalf("create task 3: %v", err)
	}
	if t3.SeqInConversation != 1 {
		t.Fatalf("expected seq 1 for new conversation, got %d", t3.SeqInConversation)
	}
}

func TestStatusAndArtifactLifecycle(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateTask(ctx, db.DB, CreateTaskInput{
		TaskID: "task-x", ConversationID: "conv-x", AgentType: "image_generation",
		Title: "生图", Mode: "auto",
		OutputSlots: json.RawMessage(`["images"]`),
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := UpdateStatus(ctx, db.DB, "task-x", StatusRunning); err != nil {
		t.Fatalf("update status: %v", err)
	}
	if err := UpdateProgress(ctx, db.DB, "task-x", 40, "生成第1张", 30); err != nil {
		t.Fatalf("update progress: %v", err)
	}

	for i := 1; i <= 3; i++ {
		if err := SaveArtifact(ctx, db.DB, "task-x", "images", "image",
			json.RawMessage(`{"path":"images/img.png"}`), i); err != nil {
			t.Fatalf("save artifact %d: %v", i, err)
		}
	}
	arts, err := LoadArtifacts(ctx, db.DB, "task-x")
	if err != nil {
		t.Fatalf("load artifacts: %v", err)
	}
	if len(arts) != 3 {
		t.Fatalf("expected 3 artifacts, got %d", len(arts))
	}

	if err := UpdateFinalStatus(ctx, db.DB, "task-x", StatusSucceeded, "已生成3张图片"); err != nil {
		t.Fatalf("final status: %v", err)
	}
	got, err := GetTask(ctx, db.DB, "task-x")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != StatusSucceeded || got.ProgressPct != 100 {
		t.Fatalf("expected succeeded/100, got %s/%d", got.Status, got.ProgressPct)
	}

	cnt, err := CountByConversation(ctx, db.DB, "conv-x")
	if err != nil || cnt != 1 {
		t.Fatalf("expected count 1, got %d (err=%v)", cnt, err)
	}
}

func TestListTasksByConversationForUserEnforcesOwnership(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	for _, input := range []CreateTaskInput{
		{TaskID: "mine", ConversationID: "conv", AgentType: "research", Title: "mine", Mode: "auto", CreateUserID: "user-1"},
		{TaskID: "other", ConversationID: "conv", AgentType: "research", Title: "other", Mode: "auto", CreateUserID: "user-2"},
	} {
		if _, err := CreateTask(ctx, db.DB, input); err != nil {
			t.Fatalf("create task %s: %v", input.TaskID, err)
		}
	}

	tasks, err := ListTasksByConversationForUser(ctx, db.DB, "conv", "user-1")
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "mine" {
		t.Fatalf("expected only user-1 task, got %#v", tasks)
	}
	for _, taskID := range []string{"mine", "other"} {
		if err := SaveArtifact(
			ctx, db.DB, taskID, "result", "text", json.RawMessage(`{"text":"ok"}`), 1,
		); err != nil {
			t.Fatalf("save artifact for %s: %v", taskID, err)
		}
	}
	artifacts, err := ListArtifactsByConversationForUser(ctx, db.DB, "conv", "user-1")
	if err != nil {
		t.Fatalf("list conversation artifacts: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].TaskID != "mine" {
		t.Fatalf("expected only user-1 artifact, got %#v", artifacts)
	}
}

func TestMarkInterrupted(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateTask(ctx, db.DB, CreateTaskInput{
		TaskID: "stale", ConversationID: "c", AgentType: "x", Title: "t", Mode: "auto",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = UpdateStatus(ctx, db.DB, "stale", StatusRunning)
	// Force an old heartbeat.
	old := time.Now().UTC().Add(-10 * time.Minute)
	if err := db.DB.Model(&orm.SubAgentTask{}).Where("id = ?", "stale").
		Update("last_heartbeat", old).Error; err != nil {
		t.Fatalf("backdate heartbeat: %v", err)
	}

	n, err := MarkInterrupted(ctx, db.DB, 5*time.Minute)
	if err != nil {
		t.Fatalf("mark interrupted: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 interrupted, got %d", n)
	}
	got, _ := GetTask(ctx, db.DB, "stale")
	if got.Status != StatusInterrupted {
		t.Fatalf("expected interrupted, got %s", got.Status)
	}
}

func TestLateRunnerEventsDoNotReviveInterruptedTask(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := CreateTask(ctx, db.DB, CreateTaskInput{
		TaskID: "stopped", ConversationID: "conv", AgentType: "plugin_step", Title: "stopped",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := UpdateFinalStatus(ctx, db.DB, "stopped", StatusInterrupted, "stopped by user"); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	if accepted, err := AcceptTaskStart(ctx, db.DB, "stopped"); err != nil || accepted {
		t.Fatalf("late task_start accepted=%v err=%v", accepted, err)
	}
	if accepted, err := AcceptFinalStatus(ctx, db.DB, "stopped", StatusSucceeded, "late success"); err != nil || accepted {
		t.Fatalf("late success accepted=%v err=%v", accepted, err)
	}

	got, err := GetTask(ctx, db.DB, "stopped")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != StatusInterrupted || got.Summary != "stopped by user" {
		t.Fatalf("task revived after stop: status=%q summary=%q", got.Status, got.Summary)
	}
}
