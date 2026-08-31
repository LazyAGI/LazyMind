package taskcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func newTestTaskDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t, &orm.TaskCenterTask{})
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
		TaskType:       "workflow_run",
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
		{UserID: "user-2", ConversationID: "conv-2", TaskType: "workflow_run", Status: "running"},
		{UserID: "user-2", ConversationID: "conv-2", TaskType: "workflow_run", Status: "succeeded"},
		{UserID: "user-2", ConversationID: "conv-2", TaskType: "workflow_run", Status: "failed"},
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

func TestArchiveTaskRunHidesRunAndPreservesConversation(t *testing.T) {
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
	var preservedConversation orm.Conversation
	if err := db.First(&preservedConversation, "id = ?", conversation.ID).Error; err != nil || preservedConversation.DeletedAt != nil {
		t.Fatalf("conversation lifecycle was mutated: conversation=%#v err=%v", preservedConversation, err)
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

func TestListTasksReportsConversationLifecycleState(t *testing.T) {
	db := orm.MigrateTestDB(t,
		&orm.TaskCenterTask{}, &orm.Conversation{}, &orm.UserSchedule{},
		&orm.WorkflowSession{}, &orm.SubAgentTask{},
	)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	archivedAt := now.Add(-time.Hour)
	deletedAt := now.Add(-30 * time.Minute)
	conversations := []orm.Conversation{
		{ID: "conv-active", DisplayName: "Active", ChannelID: "default", BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "user-1", CreatedAt: now, UpdatedAt: now}},
		{ID: "conv-archived", DisplayName: "Archived", ChannelID: "default", ArchivedAt: &archivedAt, BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "user-1", CreatedAt: now, UpdatedAt: now}},
		{ID: "conv-trash", DisplayName: "Trash", ChannelID: "default", BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "user-1", CreatedAt: now, UpdatedAt: now, DeletedAt: &deletedAt}},
	}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	for _, conversationID := range []string{"conv-active", "conv-archived", "conv-trash", "conv-missing"} {
		if err := db.Create(&orm.TaskCenterTask{
			ID: "task-" + conversationID, UserID: "user-1", ConversationID: conversationID,
			TaskType: "background_chat", Status: "succeeded", CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/task-center/tasks?page_size=20", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list tasks status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Items []taskResponse `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, item := range response.Items {
		states[item.ConversationID] = item.ConversationState
	}
	want := map[string]string{
		"conv-active": "active", "conv-archived": "archived", "conv-trash": "trash", "conv-missing": "missing",
	}
	for conversationID, state := range want {
		if states[conversationID] != state {
			t.Fatalf("conversation %s state=%q want=%q; all=%#v", conversationID, states[conversationID], state, states)
		}
	}
}

func TestArchiveTaskRunPreservesNonTaskConversationAndStopsLateUpdates(t *testing.T) {
	db := newTestTaskDB(t)
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.TaskRunInput{}); err != nil {
		t.Fatalf("auto migrate related models: %v", err)
	}
	now := time.Now().UTC()
	conversation := orm.Conversation{ID: "conv-keep", DisplayName: "用户会话", ChannelID: "default", IsTaskConv: false, BaseModel: orm.BaseModel{CreateUserID: "user-1", CreateUserName: "user-1", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	sessionID := "session-remove"
	task := orm.TaskCenterTask{ID: "workflow-remove", UserID: "user-1", ConversationID: conversation.ID, WorkflowSessionID: &sessionID, TaskType: "workflow_run", Status: "running", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := archiveTaskRun(context.Background(), db.DB, "user-1", task.ID); err != nil {
		t.Fatalf("archive task run: %v", err)
	}

	var archived orm.TaskCenterTask
	if err := db.First(&archived, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if archived.ArchivedAt == nil || archived.Status != "canceled" || archived.FinishedAt == nil {
		t.Fatalf("expected canceled archived task, got %#v", archived)
	}
	var kept orm.Conversation
	if err := db.First(&kept, "id = ?", conversation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if kept.DeletedAt != nil {
		t.Fatal("ordinary conversation must not be soft-deleted with its task-center record")
	}

	if err := UpdateTaskStatus(context.Background(), db.DB, task.ID, "succeeded"); err != nil {
		t.Fatalf("late status update: %v", err)
	}
	if err := UpdateTaskStatusBySession(context.Background(), db.DB, sessionID, "succeeded"); err != nil {
		t.Fatalf("late session status update: %v", err)
	}
	if err := db.First(&archived, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if archived.Status != "canceled" {
		t.Fatalf("late completion must not revive archived task, got status=%q", archived.Status)
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

func TestResolveTaskForResponseAddsTimeoutFailureReason(t *testing.T) {
	db := newTestTaskDB(t)
	createdAt := time.Now().UTC().Add(-3 * time.Hour)
	task := orm.TaskCenterTask{
		ID:             "stale-task",
		UserID:         "user-1",
		ConversationID: "stale-conv",
		TaskType:       "scheduled",
		Status:         "running",
		ProgressJSON:   orm.RawJSON(`{"processed":1}`),
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	}

	resolved := resolveTaskForResponse(context.Background(), db.DB, task)
	if resolved.Status != "failed" {
		t.Fatalf("expected stale task to resolve as failed, got %q", resolved.Status)
	}
	var progress map[string]any
	if err := json.Unmarshal(resolved.ProgressJSON, &progress); err != nil {
		t.Fatalf("decode progress: %v", err)
	}
	if progress["failure_reason"] != taskExecutionTimeoutReason {
		t.Fatalf("unexpected failure reason: %#v", progress["failure_reason"])
	}
	if progress["processed"] != float64(1) {
		t.Fatalf("existing progress was not preserved: %#v", progress)
	}
}

func TestUpdateTaskFailurePersistsReasonAndTerminalState(t *testing.T) {
	db := newTestTaskDB(t)
	ctx := context.Background()
	task := orm.TaskCenterTask{
		ID:             "failed-task",
		UserID:         "user-1",
		ConversationID: "failed-conv",
		TaskType:       "scheduled",
		Status:         "running",
		ProgressJSON:   orm.RawJSON(`{"processed":1}`),
	}
	if err := CreateTask(ctx, db.DB, &task); err != nil {
		t.Fatal(err)
	}
	if err := UpdateTaskFailure(ctx, db.DB, task.ID, "插件调用超时"); err != nil {
		t.Fatal(err)
	}

	var got orm.TaskCenterTask
	if err := db.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.FinishedAt == nil {
		t.Fatalf("expected persisted terminal failure, got %#v", got)
	}
	var progress map[string]any
	if err := json.Unmarshal(got.ProgressJSON, &progress); err != nil {
		t.Fatalf("decode progress: %v", err)
	}
	if progress["failure_reason"] != "插件调用超时" || progress["processed"] != float64(1) {
		t.Fatalf("unexpected progress: %#v", progress)
	}
}

func TestLoadStepsIncludesNaturalStatusContext(t *testing.T) {
	db := newTestTaskDB(t)
	if err := db.AutoMigrate(&orm.SubAgentTask{}, &orm.WorkflowSessionStep{}, &orm.SubAgentArtifact{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	subTask := orm.SubAgentTask{
		ID:                "sub-task-1",
		ConversationID:    "conv-1",
		SeqInConversation: 1,
		AgentType:         "workflow_step",
		Title:             "生成图片",
		Params:            json.RawMessage(`{}`),
		Mode:              "auto",
		Status:            "failed",
		CurrentPhase:      "调用图片插件",
		Summary:           "插件调用超时",
		LastHeartbeat:     now,
		InputSlots:        json.RawMessage(`[]`),
		OutputSlots:       json.RawMessage(`[]`),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := db.Create(&subTask).Error; err != nil {
		t.Fatal(err)
	}
	sessionStep := orm.WorkflowSessionStep{
		ID:        "session-step-1",
		SessionID: "session-1",
		StepID:    "generate_image",
		Attempt:   1,
		TaskID:    subTask.ID,
		Status:    "failed",
		Validity:  "effective",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.Create(&sessionStep).Error; err != nil {
		t.Fatal(err)
	}

	workflowSteps := loadStepsForWorkflowSession(context.Background(), db.DB, sessionStep.SessionID)
	if len(workflowSteps) != 1 {
		t.Fatalf("expected one workflow step, got %d", len(workflowSteps))
	}
	got := workflowSteps[0]
	if got.Title != subTask.Title || got.CurrentPhase != subTask.CurrentPhase || got.Summary != subTask.Summary {
		t.Fatalf("workflow step lost natural status context: %#v", got)
	}

	conversationSteps := loadStepsForConversation(context.Background(), db.DB, subTask.ConversationID)
	if len(conversationSteps) != 0 {
		t.Fatalf("workflow attempt leaked into background task steps: %#v", conversationSteps)
	}
}

func TestLoadStepsForConversationExcludesWorkflowAttempts(t *testing.T) {
	db := newTestTaskDB(t)
	if err := db.AutoMigrate(&orm.SubAgentTask{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tasks := []orm.SubAgentTask{
		{
			ID: "ordinary-step", ConversationID: "conv-steps", SeqInConversation: 1,
			AgentType: "research", Title: "检索资料", Params: json.RawMessage(`{}`),
			Mode: "auto", Status: "succeeded", LastHeartbeat: now,
			InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`),
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "workflow-step", ConversationID: "conv-steps", SeqInConversation: 2,
			AgentType: "workflow_step", Title: "生成大纲", Params: json.RawMessage(`{}`),
			Mode: "auto", Status: "failed", LastHeartbeat: now,
			InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`),
			CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}

	steps := loadStepsForConversation(context.Background(), db.DB, "conv-steps")
	if len(steps) != 1 || steps[0].StepID != "检索资料" {
		t.Fatalf("background task must exclude workflow attempts, got %#v", steps)
	}
}
