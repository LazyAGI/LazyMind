package scheduler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func newTestSchedulerDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t, &orm.UserSchedule{}, &orm.TaskCenterTask{}, &orm.UserUIPreferences{})
}

func newDependencyRuntimeTestDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t,
		&orm.UserSchedule{}, &orm.ScheduleDependency{}, &orm.TaskCenterTask{}, &orm.UserUIPreferences{},
		&orm.TaskRunInput{}, &orm.Conversation{},
	)
}

func TestCreateTaskConversationPersistsHighThinkingDepth(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{})
	conversationID := createTaskConversation(t.Context(), db.DB, "user-1", "daily report")
	if conversationID == "" {
		t.Fatal("expected conversation to be created")
	}

	var conversation orm.Conversation
	if err := db.First(&conversation, "id = ?", conversationID).Error; err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	if conversation.ThinkingDepth != "high" {
		t.Fatalf("thinking depth = %q, want high", conversation.ThinkingDepth)
	}
}

func TestFireSchedulesSkipsPausedSchedulesAndMovesNextRunForward(t *testing.T) {
	db := newTestSchedulerDB(t)
	now := time.Now().UTC()
	schedule := orm.UserSchedule{
		ID: "sched-paused", UserID: "user-1", CronExpr: "*/10 * * * *", Timezone: "UTC", PromptTemplate: "daily",
		Enabled: true, NextRunAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour),
	}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{
		"user_id": "user-1", "task_center_enabled": true, "schedules_enabled": false, "skills_enabled": true, "mcp_enabled": true,
		"created_at": now, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("seed controls: %v", err)
	}

	fireSchedules(context.Background(), db.DB, "")

	var saved orm.UserSchedule
	if err := db.Where("id = ?", schedule.ID).Take(&saved).Error; err != nil {
		t.Fatalf("load schedule: %v", err)
	}
	if !saved.NextRunAt.After(now) {
		t.Fatalf("paused schedule must move forward, got %s", saved.NextRunAt)
	}
	if saved.RunCount != 0 || saved.LastRunAt != nil {
		t.Fatalf("paused schedule must not produce a new run: %#v", saved)
	}
}

func TestResumeWaitingTasksKeepsTaskWaitingWhileSchedulesArePaused(t *testing.T) {
	db := newTestSchedulerDB(t)
	now := time.Now().UTC()
	scheduleID := "sched-waiting"
	windowStart := now.Add(-time.Hour)
	windowEnd := now
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{
		"user_id": "user-1", "task_center_enabled": true, "schedules_enabled": false,
		"skills_enabled": true, "workflows_enabled": true, "mcp_enabled": true,
		"created_at": now, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("seed controls: %v", err)
	}
	task := orm.TaskCenterTask{
		ID: "waiting-task", UserID: "user-1", ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", ScheduleID: &scheduleID, WindowStart: &windowStart, WindowEnd: &windowEnd,
		DependencyStatus: "waiting", TriggerType: "scheduled", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed waiting task: %v", err)
	}

	resumeWaitingTasks(t.Context(), db.DB)

	var saved orm.TaskCenterTask
	if err := db.Where("id = ?", task.ID).Take(&saved).Error; err != nil {
		t.Fatalf("load waiting task: %v", err)
	}
	if saved.Status != "waiting_inputs" || saved.DependencyStatus != "waiting" || saved.ConversationID != "" {
		t.Fatalf("paused schedules must not launch a waiting task: %#v", saved)
	}
	if saved.Attempt != 2 {
		t.Fatalf("successful claim must advance attempt to 2, got %d", saved.Attempt)
	}
}

func TestResumeWaitingTasksReclaimsStaleCheckingTask(t *testing.T) {
	db := newDependencyRuntimeTestDB(t)
	now := time.Now().UTC()
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{
		"user_id": "user-stale", "task_center_enabled": true, "schedules_enabled": false,
		"skills_enabled": true, "workflows_enabled": true, "mcp_enabled": true,
		"created_at": now, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("seed controls: %v", err)
	}
	scheduleID := "schedule-stale"
	windowStart := now.Add(-time.Hour)
	windowEnd := now
	task := orm.TaskCenterTask{
		ID: "stale-checking", UserID: "user-stale", ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", ScheduleID: &scheduleID, WindowStart: &windowStart, WindowEnd: &windowEnd,
		DependencyStatus: "checking", Attempt: 4, TriggerType: "scheduled",
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-dependencyClaimTimeout - time.Second),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	resumeWaitingTasks(t.Context(), db.DB)

	var saved orm.TaskCenterTask
	if err := db.First(&saved, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	if saved.Status != "waiting_inputs" || saved.DependencyStatus != "waiting" {
		t.Fatalf("stale claim must be reclaimed and released while schedules are paused: %#v", saved)
	}
	if saved.Attempt != 5 {
		t.Fatalf("reclaimed task attempt = %d, want 5", saved.Attempt)
	}
}

func TestResumeWaitingTasksDoesNotStealFreshCheckingTask(t *testing.T) {
	db := newDependencyRuntimeTestDB(t)
	now := time.Now().UTC()
	scheduleID := "schedule-fresh"
	windowStart := now.Add(-time.Hour)
	windowEnd := now
	task := orm.TaskCenterTask{
		ID: "fresh-checking", UserID: "user-fresh", ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", ScheduleID: &scheduleID, WindowStart: &windowStart, WindowEnd: &windowEnd,
		DependencyStatus: "checking", Attempt: 4, TriggerType: "scheduled", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	resumeWaitingTasks(t.Context(), db.DB)

	var saved orm.TaskCenterTask
	if err := db.First(&saved, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	if saved.DependencyStatus != "checking" || saved.Attempt != 4 {
		t.Fatalf("fresh claim was unexpectedly stolen: %#v", saved)
	}
}

func TestResumeWaitingTasksPrioritizesStaleClaimsAheadOfWaitingPage(t *testing.T) {
	db := newDependencyRuntimeTestDB(t)
	now := time.Now().UTC()
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{
		"user_id": "user-page", "task_center_enabled": true, "schedules_enabled": false,
		"skills_enabled": true, "workflows_enabled": true, "mcp_enabled": true,
		"created_at": now, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("seed controls: %v", err)
	}
	scheduleID := "schedule-page"
	windowStart := now.Add(-time.Hour)
	windowEnd := now
	tasks := make([]orm.TaskCenterTask, 0, 102)
	for i := 0; i < 101; i++ {
		tasks = append(tasks, orm.TaskCenterTask{
			ID: fmt.Sprintf("waiting-page-%03d", i), UserID: "user-page", ConversationID: "", TaskType: "scheduled",
			Status: "waiting_inputs", ScheduleID: &scheduleID, WindowStart: &windowStart, WindowEnd: &windowEnd,
			DependencyStatus: "waiting", Attempt: 1, TriggerType: "scheduled",
			CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now,
		})
	}
	stale := orm.TaskCenterTask{
		ID: "stale-after-page", UserID: "user-page", ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", ScheduleID: &scheduleID, WindowStart: &windowStart, WindowEnd: &windowEnd,
		DependencyStatus: "checking", Attempt: 4, TriggerType: "scheduled",
		CreatedAt: now, UpdatedAt: now.Add(-dependencyClaimTimeout - time.Second),
	}
	tasks = append(tasks, stale)
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatalf("seed tasks: %v", err)
	}

	resumeWaitingTasks(t.Context(), db.DB)

	var saved orm.TaskCenterTask
	if err := db.First(&saved, "id = ?", stale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.DependencyStatus != "waiting" || saved.Attempt != 5 {
		t.Fatalf("stale claim was starved behind waiting page: %#v", saved)
	}
}

func TestDependencyClaimAttemptFencesStaleOwner(t *testing.T) {
	db := newDependencyRuntimeTestDB(t)
	now := time.Now().UTC()
	task := orm.TaskCenterTask{
		ID: "fenced-checking", UserID: "user-fenced", ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", DependencyStatus: "checking", Attempt: 7,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-dependencyClaimTimeout - time.Second),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	claimAttempt, claimed, err := claimDependencyTask(t.Context(), db.DB, task, now)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed || claimAttempt != 8 {
		t.Fatalf("claim result = (%d, %v), want (8, true)", claimAttempt, claimed)
	}
	if _, claimedAgain, err := claimDependencyTask(t.Context(), db.DB, task, now); err != nil || claimedAgain {
		t.Fatalf("same snapshot claimed twice: claimed=%v err=%v", claimedAgain, err)
	}
	if released, err := releaseDependencyClaim(t.Context(), db.DB, task.ID, 7); err != nil || released {
		t.Fatalf("stale attempt released current claim: released=%v err=%v", released, err)
	}
	if released, err := releaseDependencyClaim(t.Context(), db.DB, task.ID, claimAttempt); err != nil || !released {
		t.Fatalf("current attempt failed to release claim: released=%v err=%v", released, err)
	}

	var saved orm.TaskCenterTask
	if err := db.First(&saved, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.DependencyStatus != "waiting" || saved.Attempt != claimAttempt {
		t.Fatalf("unexpected released task: %#v", saved)
	}
}

func TestRenewDependencyClaimRequiresCurrentAttempt(t *testing.T) {
	db := newDependencyRuntimeTestDB(t)
	now := time.Now().UTC()
	task := orm.TaskCenterTask{
		ID: "renew-checking", UserID: "user-renew", ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", DependencyStatus: "checking", Attempt: 3,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	if renewed, err := renewDependencyClaim(t.Context(), db.DB, task.ID, 2); err != nil || renewed {
		t.Fatalf("stale attempt renewed claim: renewed=%v err=%v", renewed, err)
	}
	if renewed, err := renewDependencyClaim(t.Context(), db.DB, task.ID, 3); err != nil || !renewed {
		t.Fatalf("current attempt failed to renew claim: renewed=%v err=%v", renewed, err)
	}

	var saved orm.TaskCenterTask
	if err := db.First(&saved, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !saved.UpdatedAt.After(task.UpdatedAt) || saved.Attempt != 3 {
		t.Fatalf("unexpected renewed task: %#v", saved)
	}
}

func TestDependencyClaimHeartbeatKeepsActiveClaimFresh(t *testing.T) {
	db := newDependencyRuntimeTestDB(t)
	now := time.Now().UTC()
	task := orm.TaskCenterTask{
		ID: "heartbeat-checking", UserID: "user-heartbeat", ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", DependencyStatus: "checking", Attempt: 3,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-dependencyClaimTimeout - time.Second),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	stop := startDependencyClaimHeartbeat(t.Context(), db.DB, task.ID, task.Attempt, time.Millisecond)
	deadline := time.Now().Add(time.Second)
	var saved orm.TaskCenterTask
	for {
		if err := db.First(&saved, "id = ?", task.ID).Error; err != nil {
			stop()
			t.Fatal(err)
		}
		if saved.UpdatedAt.After(task.UpdatedAt) {
			break
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatal("dependency claim heartbeat did not renew the claim")
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	stop() // Stopping is deliberately idempotent for terminal write paths plus defer.

	if _, claimed, err := claimDependencyTask(t.Context(), db.DB, saved, time.Now().UTC()); err != nil || claimed {
		t.Fatalf("fresh heartbeat claim was reclaimed: claimed=%v err=%v", claimed, err)
	}
}

func TestResumeWaitingTasksFailsMalformedClaimInsteadOfStrandingIt(t *testing.T) {
	db := newDependencyRuntimeTestDB(t)
	now := time.Now().UTC()
	task := orm.TaskCenterTask{
		ID: "malformed-waiting", UserID: "user-malformed", ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", DependencyStatus: "waiting", Attempt: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	resumeWaitingTasks(t.Context(), db.DB)

	var saved orm.TaskCenterTask
	if err := db.First(&saved, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != "failed" || saved.DependencyStatus != "failed" || saved.FinishedAt == nil {
		t.Fatalf("malformed task was not failed cleanly: %#v", saved)
	}
	if saved.Attempt != 2 {
		t.Fatalf("malformed task attempt = %d, want 2", saved.Attempt)
	}
}

func TestPrepareDependentTaskLaunchCommitsOnlyForCurrentAttempt(t *testing.T) {
	db := newDependencyRuntimeTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	windowStart := now.Add(-time.Hour)
	windowEnd := now
	schedule := orm.UserSchedule{
		ID: "launch-schedule", UserID: "user-launch", Name: "aggregate", CronExpr: "0 9 * * *",
		Timezone: "UTC", PromptTemplate: "build report", KbIDs: "[]", FileIDs: "[]",
		Enabled: true, NextRunAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour),
	}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	task := orm.TaskCenterTask{
		ID: "launch-task", UserID: schedule.UserID, ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", ScheduleID: &schedule.ID, WindowStart: &windowStart, WindowEnd: &windowEnd,
		DependencyStatus: "checking", Attempt: 3, TriggerType: "scheduled", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}
	input := orm.TaskRunInput{
		ID: "launch-input", DownstreamTaskID: task.ID, UpstreamTaskID: "upstream-task", DependencyID: "dependency",
		OutputID: "output", OutputContentHash: "hash", Position: 0, SnapshotJSON: []byte(`{"source_name":"source"}`), CreatedAt: now,
	}
	collection := dependencyInputCollection{contextText: "collected context", inputs: []orm.TaskRunInput{input}}

	if _, _, err := prepareDependentTaskLaunch(t.Context(), db.DB, schedule, task, 2, collection); !errors.Is(err, errDependencyClaimLost) {
		t.Fatalf("stale launch error = %v, want claim lost", err)
	}
	var count int64
	if err := db.Model(&orm.Conversation{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("stale launch persisted conversations: count=%d err=%v", count, err)
	}
	if err := db.Model(&orm.TaskRunInput{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("stale launch persisted inputs: count=%d err=%v", count, err)
	}

	convID, reqBody, err := prepareDependentTaskLaunch(t.Context(), db.DB, schedule, task, 3, collection)
	if err != nil {
		t.Fatalf("prepare launch: %v", err)
	}
	if convID == "" || !strings.Contains(reqBody["query"].(string), collection.contextText) {
		t.Fatalf("unexpected prepared request: conversation=%q body=%#v", convID, reqBody)
	}
	var saved orm.TaskCenterTask
	if err := db.First(&saved, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != "running" || saved.DependencyStatus != "ready" || saved.Attempt != 3 || saved.ConversationID != convID {
		t.Fatalf("unexpected launched task: %#v", saved)
	}
	if err := db.Model(&orm.TaskRunInput{}).Where("downstream_task_id = ?", task.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("launch input count=%d err=%v", count, err)
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", convID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("launch conversation count=%d err=%v", count, err)
	}
	var savedSchedule orm.UserSchedule
	if err := db.First(&savedSchedule, "id = ?", schedule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if savedSchedule.RunCount != 1 || savedSchedule.LastRunAt == nil || !savedSchedule.LastRunAt.Equal(windowEnd) {
		t.Fatalf("unexpected schedule counters: %#v", savedSchedule)
	}
}

func TestPrepareDependentTaskLaunchBatchesLargeInputSet(t *testing.T) {
	db := newDependencyRuntimeTestDB(t)
	now := time.Now().UTC()
	windowStart := now.Add(-time.Hour)
	windowEnd := now
	schedule := orm.UserSchedule{
		ID: "large-input-schedule", UserID: "user-large-input", Name: "aggregate", CronExpr: "0 9 * * *",
		Timezone: "UTC", PromptTemplate: "build report", KbIDs: "[]", FileIDs: "[]",
		Enabled: true, NextRunAt: now.Add(time.Hour), CreatedAt: now.Add(-time.Hour),
	}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	task := orm.TaskCenterTask{
		ID: "large-input-task", UserID: schedule.UserID, ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", ScheduleID: &schedule.ID, WindowStart: &windowStart, WindowEnd: &windowEnd,
		DependencyStatus: "checking", Attempt: 3, TriggerType: "scheduled", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	const inputCount = 3277
	inputs := make([]orm.TaskRunInput, inputCount)
	for i := range inputs {
		inputs[i] = orm.TaskRunInput{
			ID:                   fmt.Sprintf("large-input-%04d", i),
			DownstreamTaskID:     task.ID,
			UpstreamTaskID:       fmt.Sprintf("upstream-%04d", i),
			DependencyID:         fmt.Sprintf("dependency-%04d", i),
			OutputID:             fmt.Sprintf("output-%04d", i),
			OutputContentHash:    fmt.Sprintf("hash-%04d", i),
			Position:             i,
			SnapshotJSON:         []byte(`{}`),
			SourceLogicalSlotKey: fmt.Sprintf("slot-%04d", i),
			CreatedAt:            now,
		}
	}
	collection := dependencyInputCollection{contextText: "large collected context", inputs: inputs}

	convID, _, err := prepareDependentTaskLaunch(t.Context(), db.DB, schedule, task, task.Attempt, collection)
	if err != nil {
		t.Fatalf("large input launch failed: %v", err)
	}
	if convID == "" {
		t.Fatal("expected a conversation for the launched task")
	}

	var saved orm.TaskCenterTask
	if err := db.First(&saved, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != "running" || saved.DependencyStatus != "ready" || saved.ConversationID != convID {
		t.Fatalf("unexpected large input task: %#v", saved)
	}
	var persisted int64
	if err := db.Model(&orm.TaskRunInput{}).Where("downstream_task_id = ?", task.ID).Count(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted != inputCount {
		t.Fatalf("persisted input count = %d, want %d", persisted, inputCount)
	}
}

func TestFailDependentTaskClaimRejectsStaleAttempt(t *testing.T) {
	db := newDependencyRuntimeTestDB(t)
	now := time.Now().UTC()
	task := orm.TaskCenterTask{
		ID: "fail-fenced", UserID: "user-fail", ConversationID: "", TaskType: "scheduled",
		Status: "waiting_inputs", DependencyStatus: "checking", Attempt: 9, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	input := orm.TaskRunInput{ID: "existing-input", DownstreamTaskID: task.ID, UpstreamTaskID: "upstream", DependencyID: "dep", OutputID: "output", OutputContentHash: "hash", CreatedAt: now}
	if err := db.Create(&input).Error; err != nil {
		t.Fatal(err)
	}

	if failed, err := failDependentTaskClaim(t.Context(), db.DB, task, 8, "no_inputs", "no inputs"); err != nil || failed {
		t.Fatalf("stale attempt failed task: failed=%v err=%v", failed, err)
	}
	if failed, err := failDependentTaskClaim(t.Context(), db.DB, task, 9, "no_inputs", "no inputs"); err != nil || !failed {
		t.Fatalf("current attempt failed to fail task: failed=%v err=%v", failed, err)
	}

	var saved orm.TaskCenterTask
	if err := db.First(&saved, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Status != "failed" || saved.DependencyStatus != "no_inputs" {
		t.Fatalf("unexpected failed task: %#v", saved)
	}
	var count int64
	if err := db.Model(&orm.TaskRunInput{}).Where("downstream_task_id = ?", task.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed task retained inputs: count=%d err=%v", count, err)
	}
}

func TestEnableScheduleUsesSchedulesControlIndependentlyFromSubtasks(t *testing.T) {
	db := newTestSchedulerDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	schedule := orm.UserSchedule{
		ID: "sched-independent", UserID: "user-1", Name: "daily", CronExpr: "0 9 * * *",
		Timezone: "UTC", PromptTemplate: "daily", Enabled: false, NextRunAt: now.Add(time.Hour), CreatedAt: now,
	}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{
		"user_id": "user-1", "task_center_enabled": false, "schedules_enabled": true,
		"skills_enabled": true, "workflows_enabled": true, "mcp_enabled": true,
		"created_at": now, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("seed controls: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/schedules/"+schedule.ID+":enable", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	EnableScheduleHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("subtasks being off must not block schedules: status=%d body=%s", rec.Code, rec.Body.String())
	}

	if err := db.Model(&orm.UserUIPreferences{}).Where("user_id = ?", "user-1").Update("schedules_enabled", false).Error; err != nil {
		t.Fatalf("pause schedules: %v", err)
	}
	req = httptest.NewRequest(http.MethodPost, "/schedules/"+schedule.ID+":enable", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec = httptest.NewRecorder()
	EnableScheduleHandler(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("paused schedules must block enable: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// ──────────────────────────────────────────────
// CreateSchedule + next_run_at calculation
// ──────────────────────────────────────────────

func TestCreateAndCancelSchedule(t *testing.T) {
	db := newTestSchedulerDB(t)
	ctx := context.Background()

	s := &orm.UserSchedule{
		UserID:         "user-1",
		Name:           "weekly report",
		CronExpr:       "0 9 * * 1",
		Timezone:       "Asia/Shanghai",
		PromptTemplate: "weekly report",
		Enabled:        true,
	}
	if err := CreateSchedule(ctx, db.DB, s); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if s.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if s.NextRunAt.IsZero() {
		t.Fatal("expected non-zero next_run_at")
	}

	// Cancel the schedule.
	if err := CancelSchedule(ctx, db.DB, "user-1", s.ID); err != nil {
		t.Fatalf("CancelSchedule: %v", err)
	}
	var got orm.UserSchedule
	if err := db.First(&got, "id = ?", s.ID).Error; err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Enabled {
		t.Fatal("expected schedule to be disabled after cancel")
	}
}

func TestCreateScheduleRejectsBlankName(t *testing.T) {
	db := newTestSchedulerDB(t)

	for _, name := range []string{"", "   "} {
		s := &orm.UserSchedule{
			UserID:         "user-1",
			Name:           name,
			CronExpr:       "0 9 * * 1",
			Timezone:       "Asia/Shanghai",
			PromptTemplate: "weekly report",
			Enabled:        true,
		}
		if err := CreateSchedule(context.Background(), db.DB, s); err == nil || err.Error() != "name required" {
			t.Fatalf("CreateSchedule(%q) error = %v, want name required", name, err)
		}
	}

	var count int64
	if err := db.Model(&orm.UserSchedule{}).Count(&count).Error; err != nil {
		t.Fatalf("count schedules: %v", err)
	}
	if count != 0 {
		t.Fatalf("blank schedule names must not be persisted, got %d rows", count)
	}
}

func TestNextCronTimeAfterUsesScheduleTimezone(t *testing.T) {
	now := time.Date(2026, time.July, 30, 5, 30, 0, 0, time.UTC)

	next, err := nextCronTimeAfter("0 14 * * *", "Asia/Shanghai", now)
	if err != nil {
		t.Fatalf("nextCronTimeAfter: %v", err)
	}
	want := time.Date(2026, time.July, 30, 6, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}
}

func TestNextCronTimeRejectsUnknownTimezone(t *testing.T) {
	if _, err := nextCronTimeAfter("0 14 * * *", "Invalid/Timezone", time.Now()); err == nil {
		t.Fatal("expected an invalid timezone error")
	}
	if _, err := previousCronTime("0 14 * * *", "Invalid/Timezone", time.Now()); err == nil {
		t.Fatal("expected an invalid timezone error for previous cron time")
	}
}

func TestRepairFutureScheduleNextRunsCorrectsTimezoneFallback(t *testing.T) {
	db := newTestSchedulerDB(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 5, 30, 0, 0, time.UTC)
	wrongFuture := time.Date(2026, time.July, 30, 14, 0, 0, 0, time.UTC)
	overdue := time.Date(2026, time.July, 29, 6, 0, 0, 0, time.UTC)

	for _, schedule := range []*orm.UserSchedule{
		{
			ID:             "sched-wrong-timezone",
			UserID:         "user-1",
			CronExpr:       "0 14 * * *",
			Timezone:       "Asia/Shanghai",
			PromptTemplate: "future",
			Enabled:        true,
			NextRunAt:      wrongFuture,
			CreatedAt:      now.Add(-time.Hour),
		},
		{
			ID:             "sched-overdue",
			UserID:         "user-1",
			CronExpr:       "0 14 * * *",
			Timezone:       "Asia/Shanghai",
			PromptTemplate: "overdue",
			Enabled:        true,
			NextRunAt:      overdue,
			CreatedAt:      now.Add(-48 * time.Hour),
		},
	} {
		if err := db.Create(schedule).Error; err != nil {
			t.Fatalf("seed schedule %s: %v", schedule.ID, err)
		}
	}

	repairFutureScheduleNextRunsAt(ctx, db.DB, now)

	var repaired orm.UserSchedule
	if err := db.First(&repaired, "id = ?", "sched-wrong-timezone").Error; err != nil {
		t.Fatalf("fetch repaired schedule: %v", err)
	}
	want := time.Date(2026, time.July, 30, 6, 0, 0, 0, time.UTC)
	if !repaired.NextRunAt.Equal(want) {
		t.Fatalf("repaired next run = %s, want %s", repaired.NextRunAt, want)
	}

	var preserved orm.UserSchedule
	if err := db.First(&preserved, "id = ?", "sched-overdue").Error; err != nil {
		t.Fatalf("fetch overdue schedule: %v", err)
	}
	if !preserved.NextRunAt.Equal(overdue) {
		t.Fatalf("overdue next run changed to %s, want %s", preserved.NextRunAt, overdue)
	}
}

func TestDeleteScheduleRemovesRuleAndDependencyEdgesKeepsHistory(t *testing.T) {
	db := newTestSchedulerDB(t)
	if err := db.AutoMigrate(&orm.ScheduleDependency{}); err != nil {
		t.Fatalf("auto migrate dependencies: %v", err)
	}
	now := time.Now().UTC()
	schedules := []orm.UserSchedule{
		{ID: "source", UserID: "user-1", CronExpr: "0 9 * * 1", Timezone: "UTC", PromptTemplate: "source", Enabled: true, NextRunAt: now, CreatedAt: now},
		{ID: "target", UserID: "user-1", CronExpr: "0 10 * * 1", Timezone: "UTC", PromptTemplate: "target", Enabled: true, NextRunAt: now, CreatedAt: now},
		{ID: "other", UserID: "user-1", CronExpr: "0 11 * * 1", Timezone: "UTC", PromptTemplate: "other", Enabled: true, NextRunAt: now, CreatedAt: now},
	}
	if err := db.Create(&schedules).Error; err != nil {
		t.Fatalf("seed schedules: %v", err)
	}
	deps := []orm.ScheduleDependency{
		{ID: "dep-in", UserID: "user-1", SourceScheduleID: "source", TargetScheduleID: "target", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "dep-out", UserID: "user-1", SourceScheduleID: "target", TargetScheduleID: "other", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "dep-keep", UserID: "user-1", SourceScheduleID: "source", TargetScheduleID: "other", Enabled: true, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&deps).Error; err != nil {
		t.Fatalf("seed dependencies: %v", err)
	}
	scheduleID := "target"
	history := orm.TaskCenterTask{ID: "target-history", UserID: "user-1", TaskType: "scheduled", Status: "succeeded", ScheduleID: &scheduleID, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&history).Error; err != nil {
		t.Fatalf("seed history: %v", err)
	}

	if err := DeleteSchedule(context.Background(), db.DB, "user-1", "target"); err != nil {
		t.Fatalf("delete schedule: %v", err)
	}
	var deleted orm.UserSchedule
	if err := db.First(&deleted, "id = ?", "target").Error; err == nil {
		t.Fatal("expected deleted schedule to be absent")
	}
	var dependencyCount int64
	if err := db.Model(&orm.ScheduleDependency{}).
		Where("source_schedule_id = ? OR target_schedule_id = ?", "target", "target").Count(&dependencyCount).Error; err != nil {
		t.Fatal(err)
	}
	if dependencyCount != 0 {
		t.Fatalf("expected deleted schedule dependencies to be removed, got %d", dependencyCount)
	}
	var keptDependency orm.ScheduleDependency
	if err := db.First(&keptDependency, "id = ?", "dep-keep").Error; err != nil {
		t.Fatalf("unrelated dependency was removed: %v", err)
	}
	var keptHistory orm.TaskCenterTask
	if err := db.First(&keptHistory, "id = ?", history.ID).Error; err != nil {
		t.Fatalf("task history was removed with schedule: %v", err)
	}
}

// ──────────────────────────────────────────────
// Optimistic lock — only one attempt fires per tick
// ──────────────────────────────────────────────

func TestFireOne_OptimisticLock(t *testing.T) {
	db := newTestSchedulerDB(t)

	// Create a schedule whose next_run_at is in the past so it fires immediately.
	oldNext := time.Now().UTC().Add(-time.Minute)
	s := &orm.UserSchedule{
		ID:             "sched-lock-1",
		UserID:         "user-lock",
		CronExpr:       "* * * * *",
		Timezone:       "UTC",
		PromptTemplate: "lock test",
		Enabled:        true,
		NextRunAt:      oldNext,
		CreatedAt:      time.Now().UTC(),
	}
	if err := db.Create(s).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	newNext := time.Now().UTC().Add(time.Minute)
	// First attempt: should succeed (WHERE next_run_at = oldNext matches).
	r1 := db.Model(&orm.UserSchedule{}).
		Where("id = ? AND next_run_at = ?", "sched-lock-1", oldNext).
		Updates(map[string]any{"last_run_at": time.Now().UTC(), "next_run_at": newNext})
	if r1.RowsAffected != 1 {
		t.Fatalf("first attempt: expected 1 row affected, got %d", r1.RowsAffected)
	}

	// Second attempt with same old next_run_at: should fail (row already updated).
	r2 := db.Model(&orm.UserSchedule{}).
		Where("id = ? AND next_run_at = ?", "sched-lock-1", oldNext).
		Updates(map[string]any{"last_run_at": time.Now().UTC(), "next_run_at": newNext})
	if r2.RowsAffected != 0 {
		t.Fatalf("second attempt should be skipped (optimistic lock), got %d rows affected", r2.RowsAffected)
	}
}

func TestMatchDayOfMonthSupportsMonthEndOffsets(t *testing.T) {
	loc := time.UTC
	if !matchDayOfMonth("-1", time.Date(2026, time.February, 28, 9, 0, 0, 0, loc)) {
		t.Fatal("expected -1 to match the last day of February")
	}
	if !matchDayOfMonth("-2", time.Date(2026, time.April, 29, 9, 0, 0, 0, loc)) {
		t.Fatal("expected -2 to match the second-to-last day of April")
	}
	if matchDayOfMonth("-1", time.Date(2026, time.April, 29, 9, 0, 0, 0, loc)) {
		t.Fatal("expected -1 not to match before the last day")
	}
}

func TestCadenceExpression(t *testing.T) {
	interval, unit, cronExpr, err := parseCadenceExpr("@every:2:week;0 9 * * 1")
	if err != nil || interval != 2 || unit != "week" || cronExpr != "0 9 * * 1" {
		t.Fatalf("unexpected cadence parse: %d %q %q %v", interval, unit, cronExpr, err)
	}
	matching := time.Date(2026, time.January, 5, 9, 0, 0, 0, time.UTC)
	if matchCadence(matching, 2, "week") == matchCadence(matching.AddDate(0, 0, 7), 2, "week") {
		t.Fatal("adjacent ISO weeks must not both match a two-week cadence")
	}
}

func TestPreviousCronTimeUsesPriorScheduledCycle(t *testing.T) {
	next := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	previous, err := previousCronTime("0 12 * * 4", "UTC", next)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	if !previous.Equal(want) {
		t.Fatalf("previous cycle = %s, want %s", previous, want)
	}
}
