package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"lazymind/core/algo"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/store"
)

func TestCountSkillReviewHistoryStatsFiltersUserAndHalfOpenWindow(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	start := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	insertConversation(t, db, "conv-u1", "user-1", start)
	insertConversation(t, db, "conv-u2", "user-2", start)

	insertHistory(t, db, "h-start", "conv-u1", start, "at start", "", 2)
	insertHistory(t, db, "h-mid-content", "conv-u1", start.Add(10*time.Minute), "", "content fallback", 1)
	insertHistory(t, db, "h-empty", "conv-u1", start.Add(20*time.Minute), "", "", 3)
	insertHistory(t, db, "h-end", "conv-u1", end, "at end", "", 9)
	insertHistory(t, db, "h-before", "conv-u1", start.Add(-time.Nanosecond), "before", "", 9)
	insertHistory(t, db, "h-other-user", "conv-u2", start.Add(15*time.Minute), "other", "", 9)

	stats, err := CountSkillReviewHistoryStats(ctx, db, "user-1", start, end)
	if err != nil {
		t.Fatalf("count stats: %v", err)
	}
	if stats.UserTurnCount != 2 {
		t.Fatalf("expected 2 user turns, got %d", stats.UserTurnCount)
	}
	if stats.ToolCallCount != 6 {
		t.Fatalf("expected tool call sum 6, got %d", stats.ToolCallCount)
	}
}

func TestSkillPreflightFreezesRequestAndSkipsWhenBelowThreshold(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	insertConversation(t, db, "conv-u1", "user-1", now)
	insertHistory(t, db, "h-low", "conv-u1", start.Add(10*time.Minute), "one turn", "", 0)
	insertSchedulerState(t, db, orm.SkillReviewSchedulerState{
		UserID:        "user-1",
		LastWindowEnd: start,
		NextRunAt:     now,
		ActiveTaskID:  "task-skill-skip",
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	task := insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "task-skill-skip",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		ResourceID:   "skill",
		TriggerType:  orm.ResourceUpdateTriggerTypeScheduled,
		TriggerID:    "trigger-skill-skip",
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON: marshalJSON(t, skillGenerateRequestJSON{
			RequestID:     "request-skip",
			UserID:        "user-1",
			TriggerReason: "stale",
		}),
		NextRunAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	worker := NewWorker(db, Config{
		MinUserTurns:       2,
		MinToolTurns:       1,
		WorkerBatchSize:    1,
		WorkerLockTTL:      time.Minute,
		MaxAttempts:        2,
		RetryBackoffBase:   time.Minute,
		RetryBackoffMax:    time.Minute,
		SchedulerBatchSize: 1,
	}, "worker-skip")
	worker.clock = func() time.Time { return now }
	worker.loadLLMConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		t.Fatal("loadLLMConfig should not be called for skipped preflight")
		return nil, nil
	}
	worker.callers.Skill = func(context.Context, algo.SkillReviewRequest) (*algo.SkillReviewResponse, int, error) {
		t.Fatal("skill review should not be called for skipped preflight")
		return nil, 0, nil
	}

	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("worker run: %v", err)
	}
	if result.Claimed != 1 || result.Skipped != 1 {
		t.Fatalf("unexpected worker result: %#v", result)
	}
	var got orm.ResourceUpdateTask
	if err := db.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("read task: %v", err)
	}
	if got.Status != orm.ResourceUpdateTaskStatusSkipped {
		t.Fatalf("expected skipped task, got %s", got.Status)
	}
	var frozen skillGenerateRequestJSON
	if err := json.Unmarshal(got.RequestJSON, &frozen); err != nil {
		t.Fatalf("unmarshal frozen request: %v", err)
	}
	if !frozen.WindowFrozen || frozen.RequestID == "" || frozen.UserTurnCount != 1 || frozen.ToolCallCount != 0 {
		t.Fatalf("unexpected frozen request: %#v", frozen)
	}
	if frozen.StartTime != formatTaskTime(start) || frozen.EndTime != formatTaskTime(now) {
		t.Fatalf("expected worker to freeze state window, got %#v", frozen)
	}
	assertRequestJSONHasNoSensitiveFields(t, got.RequestJSON)
}

func TestSkillWorkerCallsReviewAndExpiresOnlyStillPendingResults(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	insertConversation(t, db, "conv-u1", "user-1", now)
	insertHistory(t, db, "h1", "conv-u1", start.Add(10*time.Minute), "turn one", "", 1)
	insertHistory(t, db, "h2", "conv-u1", start.Add(20*time.Minute), "turn two", "", 1)
	insertSchedulerState(t, db, orm.SkillReviewSchedulerState{
		UserID:        "user-1",
		LastWindowEnd: start,
		NextRunAt:     now,
		ActiveTaskID:  "task-skill-done",
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	insertSkillReviewResult(t, db, "pending-1", "user-1", "pending", now.Add(-3*time.Minute))
	insertSkillReviewResult(t, db, "pending-2", "user-1", "pending", now.Add(-2*time.Minute))
	insertSkillReviewResult(t, db, "accepted-1", "user-1", "accepted", now.Add(-time.Minute))
	insertSkillReviewResult(t, db, "other-user", "user-2", "pending", now.Add(-time.Minute))

	task := insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "task-skill-done",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		ResourceID:   "skill",
		TriggerType:  orm.ResourceUpdateTriggerTypeScheduled,
		TriggerID:    "trigger-skill-done",
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON: marshalJSON(t, skillGenerateRequestJSON{
			RequestID:     "request-done",
			UserID:        "user-1",
			TriggerReason: "quantity",
		}),
		NextRunAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	var captured algo.SkillReviewRequest
	worker := NewWorker(db, Config{
		MinUserTurns:     2,
		MinToolTurns:     2,
		WorkerBatchSize:  1,
		WorkerLockTTL:    time.Minute,
		MaxAttempts:      2,
		RetryBackoffBase: time.Minute,
		RetryBackoffMax:  time.Minute,
	}, "worker-skill")
	worker.clock = func() time.Time { return now }
	worker.loadLLMConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"chat": map[string]any{"api_key": "secret-key", "model": "m"}}, nil
	}
	worker.callers.Skill = func(_ context.Context, req algo.SkillReviewRequest) (*algo.SkillReviewResponse, int, error) {
		captured = req
		return &algo.SkillReviewResponse{Code: 0, Data: algo.SkillReviewData{Status: "running", RequestID: req.RequestID}}, 200, nil
	}

	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("worker run: %v", err)
	}
	if result.Done != 1 {
		t.Fatalf("expected one done task, got %#v", result)
	}
	if captured.UserID != "user-1" {
		t.Fatalf("unexpected skill review request: %#v", captured)
	}
	capturedBody, err := json.Marshal(captured)
	if err != nil {
		t.Fatalf("marshal captured request: %v", err)
	}
	if strings.Contains(string(capturedBody), "user_turn_count") || strings.Contains(string(capturedBody), "tool_call_count") {
		t.Fatalf("skill review request must not expose internal threshold counts: %s", string(capturedBody))
	}
	if strings.Join(captured.PendingSkillIDs, ",") != "pending-1,pending-2" {
		t.Fatalf("unexpected pending skill ids: %v", captured.PendingSkillIDs)
	}

	var got orm.ResourceUpdateTask
	if err := db.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("read task: %v", err)
	}
	if got.Status != orm.ResourceUpdateTaskStatusDone {
		t.Fatalf("expected done task, got %s", got.Status)
	}
	assertRequestJSONHasNoSensitiveFields(t, got.RequestJSON)
	if status := skillReviewResultStatus(t, db, "pending-1"); status != "expired" {
		t.Fatalf("expected pending-1 expired, got %s", status)
	}
	if status := skillReviewResultStatus(t, db, "pending-2"); status != "expired" {
		t.Fatalf("expected pending-2 expired, got %s", status)
	}
	if status := skillReviewResultStatus(t, db, "accepted-1"); status != "accepted" {
		t.Fatalf("expected accepted-1 unchanged, got %s", status)
	}
	if status := skillReviewResultStatus(t, db, "other-user"); status != "pending" {
		t.Fatalf("expected other-user unchanged, got %s", status)
	}
}

func TestSchedulerCreatesOneActiveTaskAndSettlesDoneTask(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	insertConversation(t, db, "conv-u1", "user-1", now)
	insertHistory(t, db, "h1", "conv-u1", start.Add(10*time.Minute), "turn one", "", 1)
	insertHistory(t, db, "h2", "conv-u1", start.Add(20*time.Minute), "turn two", "", 1)
	insertSchedulerState(t, db, orm.SkillReviewSchedulerState{
		UserID:        "user-1",
		LastWindowEnd: start,
		NextRunAt:     now,
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	scheduler := NewScheduler(db, Config{
		SchedulerBatchSize:    10,
		SchedulerLockTTL:      time.Minute,
		SchedulerRetryDelay:   time.Minute,
		MinUserTurns:          2,
		MinToolTurns:          2,
		QuantityCheckInterval: time.Second,
		MinInterval:           time.Second,
		MaxWindow:             24 * time.Hour,
		Stages:                []Stage{{Window: 4 * time.Hour, Interval: time.Hour, Successes: 1}, {Window: 8 * time.Hour, Interval: 2 * time.Hour, Successes: 0}},
	}, "scheduler-1")
	scheduler.clock = func() time.Time { return now }

	result, err := scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("scheduler run: %v", err)
	}
	if result.CreatedTasks != 1 {
		t.Fatalf("expected one created task, got %#v", result)
	}
	var state orm.SkillReviewSchedulerState
	if err := db.First(&state, "user_id = ?", "user-1").Error; err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.ActiveTaskID == "" {
		t.Fatal("expected active_task_id to be set")
	}
	if !state.LastWindowEnd.Equal(start) {
		t.Fatalf("scheduler must not advance last_window_end before worker done: %v", state.LastWindowEnd)
	}
	var taskCount int64
	db.Model(&orm.ResourceUpdateTask{}).Where("resource_type = ?", orm.ResourceUpdateResourceTypeSkill).Count(&taskCount)
	if taskCount != 1 {
		t.Fatalf("expected one skill task, got %d", taskCount)
	}

	result, err = scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("scheduler second run: %v", err)
	}
	if result.CreatedTasks != 0 {
		t.Fatalf("expected active task to prevent duplicate task, got %#v", result)
	}

	if err := db.Model(&orm.ResourceUpdateTask{}).Where("id = ?", state.ActiveTaskID).Updates(map[string]any{
		"status":      orm.ResourceUpdateTaskStatusDone,
		"finished_at": now,
		"updated_at":  now,
	}).Error; err != nil {
		t.Fatalf("mark task done: %v", err)
	}
	result, err = scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("scheduler settle run: %v", err)
	}
	if result.CreatedTasks != 0 {
		t.Fatalf("settle should not create a second task in same tick, got %#v", result)
	}
	if err := db.First(&state, "user_id = ?", "user-1").Error; err != nil {
		t.Fatalf("read settled state: %v", err)
	}
	if state.ActiveTaskID != "" || !state.LastWindowEnd.Equal(now) || state.StageIndex != 1 || state.StageSuccessCount != 0 || state.TotalSuccessCount != 1 {
		t.Fatalf("unexpected settled state: %#v", state)
	}
}

func TestSchedulerDoesNotAdvanceWindowWhenThresholdNotReached(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	insertConversation(t, db, "conv-u1", "user-1", now)
	insertHistory(t, db, "h1", "conv-u1", start.Add(10*time.Minute), "turn one", "", 0)
	insertSchedulerState(t, db, orm.SkillReviewSchedulerState{
		UserID:        "user-1",
		LastWindowEnd: start,
		NextRunAt:     now,
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	scheduler := NewScheduler(db, Config{
		SchedulerBatchSize:    10,
		SchedulerLockTTL:      time.Minute,
		SchedulerRetryDelay:   time.Minute,
		MinUserTurns:          2,
		MinToolTurns:          1,
		QuantityCheckInterval: time.Second,
		MinInterval:           time.Second,
		MaxWindow:             24 * time.Hour,
		Stages:                []Stage{{Window: 4 * time.Hour, Interval: time.Hour, Successes: 0}},
	}, "scheduler-threshold")
	scheduler.clock = func() time.Time { return now }

	result, err := scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("scheduler run: %v", err)
	}
	if result.CreatedTasks != 0 || result.SkippedStates != 1 {
		t.Fatalf("expected skipped state without task, got %#v", result)
	}
	var state orm.SkillReviewSchedulerState
	if err := db.First(&state, "user_id = ?", "user-1").Error; err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !state.LastWindowEnd.Equal(start) {
		t.Fatalf("last_window_end advanced without accepted task: got %v want %v", state.LastWindowEnd, start)
	}
}

func TestMemoryWorkerRetriesThenFailsAndDoesNotPersistLLMConfig(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	task := insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "task-memory-retry",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeMemory,
		UserID:       "user-1",
		ResourceID:   "memory",
		TriggerType:  orm.ResourceUpdateTriggerTypeConversationIdle,
		TriggerID:    "idle-1",
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON: marshalJSON(t, memoryGenerateRequestJSON{
			SessionID:      "session-1",
			Target:         orm.ResourceUpdateResourceTypeMemory,
			History:        json.RawMessage(`[{"role":"user","content":"hello"}]`),
			CurrentContent: "current memory",
		}),
		NextRunAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	clockNow := now
	worker := NewWorker(db, Config{
		WorkerBatchSize:  1,
		WorkerLockTTL:    time.Minute,
		MaxAttempts:      2,
		RetryBackoffBase: time.Minute,
		RetryBackoffMax:  time.Minute,
	}, "worker-retry")
	worker.clock = func() time.Time { return clockNow }
	worker.loadLLMConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"chat": map[string]any{"api_key": "secret-key"}}, nil
	}
	worker.callers.Memory = func(context.Context, algo.MemoryReviewRequest) (*algo.MemoryReviewResponse, int, error) {
		return nil, 200, errors.New("temporary upstream error")
	}

	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("worker first run: %v", err)
	}
	if result.Retried != 1 {
		t.Fatalf("expected one retry, got %#v", result)
	}
	var got orm.ResourceUpdateTask
	if err := db.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("read retried task: %v", err)
	}
	if got.Status != orm.ResourceUpdateTaskStatusPending || got.AttemptCount != 1 || got.NextRunAt.Before(now.Add(time.Minute)) {
		t.Fatalf("unexpected retry state: %#v", got)
	}
	assertRequestJSONHasNoSensitiveFields(t, got.RequestJSON)

	clockNow = now.Add(2 * time.Minute)
	result, err = worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("worker second run: %v", err)
	}
	if result.Failed != 1 {
		t.Fatalf("expected final failure, got %#v", result)
	}
	if err := db.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("read failed task: %v", err)
	}
	if got.Status != orm.ResourceUpdateTaskStatusFailed || got.AttemptCount != 2 {
		t.Fatalf("unexpected failed state: %#v", got)
	}
}

func TestMemoryWorkerMarksSuccessfulReviewDone(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	task := insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "task-memory-done",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeUserPreference,
		UserID:       "user-1",
		ResourceID:   "user_preference",
		TriggerType:  orm.ResourceUpdateTriggerTypeConversationIdle,
		TriggerID:    "idle-2",
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON: marshalJSON(t, memoryGenerateRequestJSON{
			SessionID:      "session-2",
			Target:         orm.ResourceUpdateResourceTypeUserPreference,
			History:        json.RawMessage(`[{"role":"assistant","content":"ok"}]`),
			CurrentContent: "current preference",
		}),
		NextRunAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	var captured algo.MemoryReviewRequest
	worker := NewWorker(db, Config{
		WorkerBatchSize:  1,
		WorkerLockTTL:    time.Minute,
		MaxAttempts:      2,
		RetryBackoffBase: time.Minute,
		RetryBackoffMax:  time.Minute,
	}, "worker-memory-done")
	worker.clock = func() time.Time { return now }
	worker.loadLLMConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"chat": map[string]any{"api_key": "secret-key"}}, nil
	}
	worker.callers.Memory = func(_ context.Context, req algo.MemoryReviewRequest) (*algo.MemoryReviewResponse, int, error) {
		captured = req
		return &algo.MemoryReviewResponse{Status: "success"}, 200, nil
	}

	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("worker run: %v", err)
	}
	if result.Done != 1 {
		t.Fatalf("expected done result, got %#v", result)
	}
	if captured.UserID != "user-1" || captured.User != "current preference" || captured.Memory != "" {
		t.Fatalf("unexpected memory review request: %#v", captured)
	}
	if captured.History == nil || captured.LLMConfig == nil {
		t.Fatalf("expected history and llm_config in memory review request: %#v", captured)
	}
	var got orm.ResourceUpdateTask
	if err := db.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("read done task: %v", err)
	}
	if got.Status != orm.ResourceUpdateTaskStatusDone || got.ResultID != "" {
		t.Fatalf("unexpected done task: %#v", got)
	}
	assertRequestJSONHasNoSensitiveFields(t, got.RequestJSON)
}

func TestMemoryWorkerSendsCombinedMemoryReviewRequest(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	task := insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "task-memory-combined",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeMemory,
		UserID:       "user-1",
		ResourceID:   "memory",
		TriggerType:  orm.ResourceUpdateTriggerTypeConversationIdle,
		TriggerID:    "idle-combined",
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON: marshalJSON(t, memoryGenerateRequestJSON{
			SessionID: "session-combined",
			History:   json.RawMessage(`[{"role":"user","content":"hello"}]`),
			Memory:    "current memory",
			User:      "current preference",
		}),
		NextRunAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	var captured algo.MemoryReviewRequest
	worker := NewWorker(db, Config{
		WorkerBatchSize:  1,
		WorkerLockTTL:    time.Minute,
		MaxAttempts:      2,
		RetryBackoffBase: time.Minute,
		RetryBackoffMax:  time.Minute,
	}, "worker-memory-combined")
	worker.clock = func() time.Time { return now }
	worker.loadLLMConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"chat": map[string]any{"model": "demo"}}, nil
	}
	worker.callers.Memory = func(_ context.Context, req algo.MemoryReviewRequest) (*algo.MemoryReviewResponse, int, error) {
		captured = req
		return &algo.MemoryReviewResponse{Status: "success"}, 200, nil
	}

	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("worker run: %v", err)
	}
	if result.Done != 1 {
		t.Fatalf("expected done result, got %#v", result)
	}
	if captured.UserID != "user-1" || captured.Memory != "current memory" || captured.User != "current preference" {
		t.Fatalf("unexpected memory review request: %#v", captured)
	}
	if captured.History == nil || captured.LLMConfig == nil {
		t.Fatalf("expected history and llm_config in memory review request: %#v", captured)
	}
	var got orm.ResourceUpdateTask
	if err := db.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("read done task: %v", err)
	}
	if got.Status != orm.ResourceUpdateTaskStatusDone {
		t.Fatalf("unexpected done task: %#v", got)
	}
}

func TestScannerCreatesAutoApplyTasksAndExpiresOlderSkillPatches(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	createMemoryReviewTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertSkillResource(t, db, orm.SkillResource{
		ID:           "skill-auto",
		OwnerUserID:  "user-1",
		Category:     "system",
		SkillName:    "git-workflow",
		NodeType:     evolution.SkillNodeTypeParent,
		Content:      skillContent("git-workflow", "old"),
		ContentHash:  evolution.HashContent(skillContent("git-workflow", "old")),
		Version:      1,
		AutoEvo:      true,
		IsEnabled:    true,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
		CreateUserID: "user-1",
	})
	insertSkillResource(t, db, orm.SkillResource{
		ID:           "skill-manual",
		OwnerUserID:  "user-1",
		Category:     "system",
		SkillName:    "manual-skill",
		NodeType:     evolution.SkillNodeTypeParent,
		Content:      skillContent("manual-skill", "old"),
		ContentHash:  evolution.HashContent(skillContent("manual-skill", "old")),
		Version:      1,
		AutoEvo:      false,
		IsEnabled:    true,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
		CreateUserID: "user-1",
	})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "patch-old", UserID: "user-1", SkillName: "git-workflow", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: skillContent("git-workflow", "old patch"), Time: now.Add(-10 * time.Minute)})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "patch-new", UserID: "user-1", SkillName: "git-workflow", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: skillContent("git-workflow", "new patch"), Time: now.Add(-5 * time.Minute)})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "new-skill", UserID: "user-1", SkillName: "brand-new", Type: skillReviewTypeNew, ReviewStatus: reviewStatusPending, SkillContent: skillContent("brand-new", "body"), Time: now.Add(-4 * time.Minute)})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "manual-patch", UserID: "user-1", SkillName: "manual-skill", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: skillContent("manual-skill", "manual"), Time: now.Add(-3 * time.Minute)})
	insertMemoryResource(t, db, orm.SystemMemory{ID: "memory-auto", UserID: "user-1", Content: "old memory", ContentHash: evolution.HashContent("old memory"), Version: 1, AutoEvo: true, CreatedAt: now, UpdatedAt: now})
	insertPreferenceResource(t, db, orm.SystemUserPreference{ID: "pref-manual", UserID: "user-1", Content: "old pref", ContentHash: evolution.HashContent("old pref"), Version: 1, AutoEvo: false, CreatedAt: now, UpdatedAt: now})
	insertMemoryReviewResult(t, db, MemoryReviewResult{ID: "memory-result", UserID: "user-1", Target: orm.ResourceUpdateResourceTypeMemory, Content: "new memory", State: memoryReviewStateSuccess, ReviewStatus: reviewStatusPending, Time: now})
	insertMemoryReviewResult(t, db, MemoryReviewResult{ID: "pref-result", UserID: "user-1", Target: orm.ResourceUpdateResourceTypeUserPreference, Content: "new pref", State: memoryReviewStateSuccess, ReviewStatus: reviewStatusPending, Time: now})

	scanner := NewScanner(db, Config{WorkerBatchSize: 10}, "scanner-test")
	scanner.clock = func() time.Time { return now }
	result, err := scanner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("scanner run: %v", err)
	}
	if result.SkillResultsExpired != 1 || result.SkillTasksCreated != 1 || result.MemoryTasksCreated != 1 || result.UserPreferenceTasksCreated != 0 {
		t.Fatalf("unexpected scanner result: %#v", result)
	}
	if status := skillReviewResultStatus(t, db, "patch-old"); status != reviewStatusExpired {
		t.Fatalf("expected old patch expired, got %s", status)
	}
	for _, id := range []string{"patch-new", "manual-patch"} {
		if status := skillReviewResultStatus(t, db, id); status != reviewStatusPending {
			t.Fatalf("expected %s pending, got %s", id, status)
		}
	}
	if status := skillReviewResultStatus(t, db, "new-skill"); status != reviewStatusAccepted {
		t.Fatalf("expected new-skill accepted, got %s", status)
	}
	var createdSkill orm.SkillResource
	if err := db.Take(&createdSkill, "owner_user_id = ? AND skill_name = ?", "user-1", "brand-new").Error; err != nil {
		t.Fatalf("read auto-created skill: %v", err)
	}
	if createdSkill.AutoEvo {
		t.Fatal("expected auto-created new skill auto_evo=false")
	}
	result, err = scanner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("scanner second run: %v", err)
	}
	if result.SkillTasksCreated != 0 || result.MemoryTasksCreated != 0 {
		t.Fatalf("scanner should not duplicate active tasks: %#v", result)
	}
	var taskCount int64
	if err := db.Model(&orm.ResourceUpdateTask{}).Where("task_type = ?", orm.ResourceUpdateTaskTypeAutoApplyReview).Count(&taskCount).Error; err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 2 {
		t.Fatalf("expected two auto apply tasks, got %d", taskCount)
	}
	var tasks []orm.ResourceUpdateTask
	if err := db.Order("resource_type ASC").Find(&tasks, "task_type = ?", orm.ResourceUpdateTaskTypeAutoApplyReview).Error; err != nil {
		t.Fatalf("list auto apply tasks: %v", err)
	}
	for _, task := range tasks {
		if task.TriggerType != orm.ResourceUpdateTriggerTypeReviewResult {
			t.Fatalf("expected review_result trigger, got %#v", task)
		}
		if task.ResourceType == orm.ResourceUpdateResourceTypeSkill && task.TriggerID != "skill_review_results:patch-new" {
			t.Fatalf("unexpected skill trigger id: %s", task.TriggerID)
		}
		if task.ResourceType == orm.ResourceUpdateResourceTypeMemory && task.TriggerID != "memory_review:memory-result" {
			t.Fatalf("unexpected memory trigger id: %s", task.TriggerID)
		}
	}
}

func TestScannerExpiresUnmappableSkillPatchAndFailedAutoApplyBlocksRecreate(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	createMemoryReviewTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "missing-skill", UserID: "user-1", SkillName: "missing", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: skillContent("missing", "body"), Time: now})
	insertSkillResource(t, db, orm.SkillResource{
		ID:           "skill-auto",
		OwnerUserID:  "user-1",
		Category:     "system",
		SkillName:    "blocked",
		NodeType:     evolution.SkillNodeTypeParent,
		Content:      skillContent("blocked", "old"),
		ContentHash:  evolution.HashContent(skillContent("blocked", "old")),
		Version:      1,
		AutoEvo:      true,
		IsEnabled:    true,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
		CreateUserID: "user-1",
	})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "blocked-result", UserID: "user-1", SkillName: "blocked", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: skillContent("blocked", "new"), Time: now})
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:             "failed-auto-apply",
		TaskType:       orm.ResourceUpdateTaskTypeAutoApplyReview,
		ResourceType:   orm.ResourceUpdateResourceTypeSkill,
		UserID:         "user-1",
		ResourceID:     "skill-auto",
		TriggerType:    orm.ResourceUpdateTriggerTypeReviewResult,
		TriggerID:      "skill_review_results:blocked-result",
		ReviewResultID: "blocked-result",
		Status:         orm.ResourceUpdateTaskStatusFailed,
		NextRunAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	})

	scanner := NewScanner(db, Config{WorkerBatchSize: 10}, "scanner-failed")
	scanner.clock = func() time.Time { return now }
	result, err := scanner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("scanner run: %v", err)
	}
	if result.SkillResultsExpired != 1 || result.SkillTasksCreated != 0 {
		t.Fatalf("unexpected scanner result: %#v", result)
	}
	if status := skillReviewResultStatus(t, db, "missing-skill"); status != reviewStatusExpired {
		t.Fatalf("expected missing skill patch expired, got %s", status)
	}
	if status := skillReviewResultStatus(t, db, "blocked-result"); status != reviewStatusPending {
		t.Fatalf("expected blocked result to remain pending, got %s", status)
	}
	var count int64
	if err := db.Model(&orm.ResourceUpdateTask{}).
		Where("task_type = ? AND resource_type = ? AND review_result_id = ?",
			orm.ResourceUpdateTaskTypeAutoApplyReview, orm.ResourceUpdateResourceTypeSkill, "blocked-result").
		Count(&count).Error; err != nil {
		t.Fatalf("count blocked auto apply tasks: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected failed auto apply task to block recreation, got %d tasks", count)
	}
}

func TestScanPendingResultsForResourceUsesAutoEvoEnabledTrigger(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertSkillResource(t, db, orm.SkillResource{
		ID:                "skill-compensate",
		OwnerUserID:       "user-1",
		Category:          "system",
		SkillName:         "compensate",
		NodeType:          evolution.SkillNodeTypeParent,
		Content:           skillContent("compensate", "old"),
		ContentHash:       evolution.HashContent(skillContent("compensate", "old")),
		Version:           1,
		AutoEvo:           true,
		AutoEvoGeneration: 3,
		IsEnabled:         true,
		CreatedAt:         now.Add(-time.Hour),
		UpdatedAt:         now.Add(-time.Hour),
		CreateUserID:      "user-1",
	})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "pending-compensate", UserID: "user-1", SkillName: "compensate", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: skillContent("compensate", "new"), Time: now})

	if err := ScanPendingResultsForResource(ctx, db, orm.ResourceUpdateResourceTypeSkill, "user-1", "skill-compensate"); err != nil {
		t.Fatalf("scan pending result for resource: %v", err)
	}
	var task orm.ResourceUpdateTask
	if err := db.Take(&task, "review_result_id = ?", "pending-compensate").Error; err != nil {
		t.Fatalf("read compensation task: %v", err)
	}
	if task.TriggerType != orm.ResourceUpdateTriggerTypeAutoEvoEnabled {
		t.Fatalf("expected auto_evo_enabled trigger, got %s", task.TriggerType)
	}
	if task.TriggerID != "skill:skill-compensate:pending-compensate:3" {
		t.Fatalf("unexpected compensation trigger id: %s", task.TriggerID)
	}
}

func TestAutoApplyReviewRechecksAutoEvoAndAppliesWhenStillValid(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	oldContent := skillContent("git-workflow", "old")
	newContent := skillContent("git-workflow", "new")
	insertSkillResource(t, db, orm.SkillResource{
		ID:           "skill-apply",
		OwnerUserID:  "user-1",
		Category:     "system",
		SkillName:    "git-workflow",
		NodeType:     evolution.SkillNodeTypeParent,
		Content:      oldContent,
		ContentHash:  evolution.HashContent(oldContent),
		Version:      2,
		AutoEvo:      true,
		Ext:          json.RawMessage(`{"draft_suggestion_ids":["legacy"],"keep":"yes"}`),
		IsEnabled:    true,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
		CreateUserID: "user-1",
	})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "patch-apply", UserID: "user-1", SkillName: "git-workflow", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: newContent, Time: now})
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:             "task-apply",
		TaskType:       orm.ResourceUpdateTaskTypeAutoApplyReview,
		ResourceType:   orm.ResourceUpdateResourceTypeSkill,
		UserID:         "user-1",
		ResourceID:     "skill-apply",
		TriggerType:    orm.ResourceUpdateTriggerTypeReviewResult,
		TriggerID:      "patch-apply",
		ReviewResultID: "patch-apply",
		Status:         orm.ResourceUpdateTaskStatusPending,
		NextRunAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	worker := NewWorker(db, Config{WorkerBatchSize: 1, WorkerLockTTL: time.Minute, MaxAttempts: 2, RetryBackoffBase: time.Second, RetryBackoffMax: time.Second}, "worker-apply")
	worker.clock = func() time.Time { return now }
	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("worker run: %v", err)
	}
	if result.Done != 1 {
		t.Fatalf("expected done auto apply, got %#v", result)
	}
	var updated orm.SkillResource
	if err := db.Take(&updated, "id = ?", "skill-apply").Error; err != nil {
		t.Fatalf("read skill: %v", err)
	}
	if updated.Content != newContent || updated.Version != 3 || updated.ContentHash != evolution.HashContent(newContent) {
		t.Fatalf("skill not applied correctly: %#v", updated)
	}
	if strings.Contains(string(updated.Ext), "draft_suggestion_ids") || !strings.Contains(string(updated.Ext), `"keep":"yes"`) {
		t.Fatalf("expected legacy draft suggestion refs cleared while preserving ext, got %s", string(updated.Ext))
	}
	if status := skillReviewResultStatus(t, db, "patch-apply"); status != reviewStatusAccepted {
		t.Fatalf("expected accepted result, got %s", status)
	}
}

func TestAutoApplyReviewSkipsWhenAutoEvoDisabledAtExecution(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	oldContent := skillContent("git-workflow", "old")
	insertSkillResource(t, db, orm.SkillResource{
		ID:           "skill-skip",
		OwnerUserID:  "user-1",
		Category:     "system",
		SkillName:    "git-workflow",
		NodeType:     evolution.SkillNodeTypeParent,
		Content:      oldContent,
		ContentHash:  evolution.HashContent(oldContent),
		Version:      1,
		AutoEvo:      false,
		IsEnabled:    true,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
		CreateUserID: "user-1",
	})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "patch-skip", UserID: "user-1", SkillName: "git-workflow", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: skillContent("git-workflow", "new"), Time: now})
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:             "task-skip",
		TaskType:       orm.ResourceUpdateTaskTypeAutoApplyReview,
		ResourceType:   orm.ResourceUpdateResourceTypeSkill,
		UserID:         "user-1",
		ResourceID:     "skill-skip",
		TriggerType:    orm.ResourceUpdateTriggerTypeReviewResult,
		TriggerID:      "patch-skip",
		ReviewResultID: "patch-skip",
		Status:         orm.ResourceUpdateTaskStatusPending,
		NextRunAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	worker := NewWorker(db, Config{WorkerBatchSize: 1, WorkerLockTTL: time.Minute, MaxAttempts: 2, RetryBackoffBase: time.Second, RetryBackoffMax: time.Second}, "worker-skip-auto")
	worker.clock = func() time.Time { return now }
	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("worker run: %v", err)
	}
	if result.Skipped != 1 {
		t.Fatalf("expected skipped auto apply, got %#v", result)
	}
	var task orm.ResourceUpdateTask
	if err := db.Take(&task, "id = ?", "task-skip").Error; err != nil {
		t.Fatalf("read task: %v", err)
	}
	if task.Status != orm.ResourceUpdateTaskStatusSkipped {
		t.Fatalf("expected skipped task, got %s", task.Status)
	}
	var updated orm.SkillResource
	if err := db.Take(&updated, "id = ?", "skill-skip").Error; err != nil {
		t.Fatalf("read skill: %v", err)
	}
	if updated.Content != oldContent || updated.Version != 1 {
		t.Fatalf("skill should remain unchanged, got content=%q version=%d", updated.Content, updated.Version)
	}
	if status := skillReviewResultStatus(t, db, "patch-skip"); status != reviewStatusPending {
		t.Fatalf("expected result pending, got %s", status)
	}
}

func TestSkillAcceptRejectAndUserFiltering(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	rawNewContent := "\n" + skillContentWithCategory("new-skill", "A new skill", "body", "") + "\n"
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "new-1", UserID: "user-1", SkillName: "new-skill", Type: skillReviewTypeNew, ReviewStatus: reviewStatusPending, SkillContent: rawNewContent, Time: now})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "other-1", UserID: "other-user", SkillName: "other-skill", Type: skillReviewTypeNew, ReviewStatus: reviewStatusPending, SkillContent: skillContent("other-skill", "body"), Time: now})

	otherReq := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/core/skill-review-results/other-1:accept", nil), map[string]string{"review_result_id": "other-1"})
	otherReq.Header.Set("X-User-Id", "user-1")
	otherRec := httptest.NewRecorder()
	AcceptSkillReviewResult(otherRec, otherReq)
	if otherRec.Code != http.StatusNotFound {
		t.Fatalf("expected cross-user accept hidden as 404, got %d body=%s", otherRec.Code, otherRec.Body.String())
	}

	req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/core/skill-review-results/new-1:accept", nil), map[string]string{"review_result_id": "new-1"})
	req.Header.Set("X-User-Id", "user-1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()
	AcceptSkillReviewResult(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept new skill failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var row orm.SkillResource
	if err := db.Take(&row, "owner_user_id = ? AND skill_name = ?", "user-1", "new-skill").Error; err != nil {
		t.Fatalf("read created skill: %v", err)
	}
	if row.Content != rawNewContent || row.Category != "system" || row.AutoEvo {
		t.Fatalf("created skill mismatch: category=%q auto_evo=%v content=%q", row.Category, row.AutoEvo, row.Content)
	}
	if status := skillReviewResultStatus(t, db, "new-1"); status != reviewStatusAccepted {
		t.Fatalf("expected new result accepted, got %s", status)
	}

	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "reject-1", UserID: "user-1", SkillName: "reject-skill", Type: skillReviewTypeNew, ReviewStatus: reviewStatusPending, SkillContent: skillContent("reject-skill", "body"), Time: now})
	rejectReq := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/core/skill-review-results/reject-1:reject", nil), map[string]string{"review_result_id": "reject-1"})
	rejectReq.Header.Set("X-User-Id", "user-1")
	rejectRec := httptest.NewRecorder()
	RejectSkillReviewResult(rejectRec, rejectReq)
	if rejectRec.Code != http.StatusOK {
		t.Fatalf("reject skill failed: code=%d body=%s", rejectRec.Code, rejectRec.Body.String())
	}
	if status := skillReviewResultStatus(t, db, "reject-1"); status != reviewStatusRejected {
		t.Fatalf("expected rejected status, got %s", status)
	}
}

func TestListSkillReviewResultsFiltersSkillName(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "target-1", UserID: "user-1", SkillName: "git-workflow", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: skillContent("git-workflow", "target"), Time: now})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "other-skill", UserID: "user-1", SkillName: "release-check", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: skillContent("release-check", "other"), Time: now})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "other-user", UserID: "user-2", SkillName: "git-workflow", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: skillContent("git-workflow", "other user"), Time: now})
	if err := db.Exec("UPDATE skill_review_results SET summary = NULL WHERE id = ?", "target-1").Error; err != nil {
		t.Fatalf("set summary null: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/core/skill-review-results?review_status=pending&type=patch&skill_name=git-workflow", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()

	ListSkillReviewResults(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items []skillReviewResultResponse `json:"items"`
			Total int                         `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("expected code 0, got %d", resp.Code)
	}
	if resp.Data.Total != 1 || len(resp.Data.Items) != 1 || resp.Data.Items[0].ID != "target-1" {
		t.Fatalf("expected only target result, got %#v", resp.Data)
	}
	if resp.Data.Items[0].Summary != "" {
		t.Fatalf("expected null summary to be returned as empty string, got %q", resp.Data.Items[0].Summary)
	}
}

func TestSkillReviewResultDetailIncludesCurrentContentForPatch(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	currentContent := skillContent("patch-skill", "current body")
	candidateContent := skillContent("patch-skill", "candidate body")
	insertSkillResource(t, db, orm.SkillResource{
		ID:           "skill-detail",
		OwnerUserID:  "user-1",
		Category:     "system",
		SkillName:    "patch-skill",
		NodeType:     evolution.SkillNodeTypeParent,
		Content:      currentContent,
		ContentHash:  evolution.HashContent(currentContent),
		Version:      1,
		AutoEvo:      false,
		IsEnabled:    true,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
		CreateUserID: "user-1",
	})
	insertFullSkillReviewResult(t, db, SkillReviewResult{ID: "patch-detail", UserID: "user-1", SkillName: "patch-skill", Type: skillReviewTypePatch, ReviewStatus: reviewStatusPending, SkillContent: candidateContent, Summary: "summary", Time: now})

	req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/core/skill-review-results/patch-detail", nil), map[string]string{"review_result_id": "patch-detail"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetSkillReviewResult(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int                       `json:"code"`
		Data skillReviewResultResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if resp.Data.SkillContent != candidateContent || resp.Data.CurrentContent != currentContent {
		t.Fatalf("unexpected detail response: %#v", resp.Data)
	}
}

func TestMemoryAcceptRejectTaskAPIAndNoAsyncJobID(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createMemoryReviewTable(t, db)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertMemoryResource(t, db, orm.SystemMemory{ID: "memory-1", UserID: "user-1", Content: "old memory", ContentHash: evolution.HashContent("old memory"), Version: 1, AutoEvo: false, CreatedAt: now, UpdatedAt: now})
	insertMemoryReviewResult(t, db, MemoryReviewResult{ID: "memory-accept", UserID: "user-1", Target: orm.ResourceUpdateResourceTypeMemory, Content: "new memory", State: memoryReviewStateSuccess, ReviewStatus: reviewStatusPending, Time: now})
	insertMemoryReviewResult(t, db, MemoryReviewResult{ID: "memory-reject", UserID: "user-1", Target: orm.ResourceUpdateResourceTypeMemory, Content: "reject memory", State: memoryReviewStateSuccess, ReviewStatus: reviewStatusPending, Time: now})
	insertMemoryReviewResult(t, db, MemoryReviewResult{ID: "memory-other", UserID: "other-user", Target: orm.ResourceUpdateResourceTypeMemory, Content: "other memory", State: memoryReviewStateSuccess, ReviewStatus: reviewStatusPending, Time: now})
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:             "task-user-1",
		TaskType:       orm.ResourceUpdateTaskTypeAutoApplyReview,
		ResourceType:   orm.ResourceUpdateResourceTypeMemory,
		UserID:         "user-1",
		ResourceID:     "memory-1",
		TriggerType:    orm.ResourceUpdateTriggerTypeReviewResult,
		TriggerID:      "memory-accept",
		ReviewResultID: "memory-accept",
		Status:         orm.ResourceUpdateTaskStatusSkipped,
		NextRunAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	})

	req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/core/memory-review-results/memory-accept:accept", nil), map[string]string{"review_result_id": "memory-accept"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	AcceptMemoryReviewResult(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept memory failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var memory orm.SystemMemory
	if err := db.Take(&memory, "id = ?", "memory-1").Error; err != nil {
		t.Fatalf("read memory: %v", err)
	}
	if memory.Content != "new memory" || memory.Version != 2 || memory.ContentHash != evolution.HashContent("new memory") {
		t.Fatalf("memory not updated: %#v", memory)
	}

	otherReq := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/core/memory-review-results/memory-other", nil), map[string]string{"review_result_id": "memory-other"})
	otherReq.Header.Set("X-User-Id", "user-1")
	otherRec := httptest.NewRecorder()
	GetMemoryReviewResult(otherRec, otherReq)
	if otherRec.Code != http.StatusNotFound {
		t.Fatalf("expected cross-user memory result hidden as 404, got %d", otherRec.Code)
	}

	rejectReq := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/core/memory-review-results/memory-reject:reject", nil), map[string]string{"review_result_id": "memory-reject"})
	rejectReq.Header.Set("X-User-Id", "user-1")
	rejectRec := httptest.NewRecorder()
	RejectMemoryReviewResult(rejectRec, rejectReq)
	if rejectRec.Code != http.StatusOK {
		t.Fatalf("reject memory failed: code=%d body=%s", rejectRec.Code, rejectRec.Body.String())
	}
	if status := memoryReviewStatus(t, db, "memory-reject"); status != reviewStatusRejected {
		t.Fatalf("expected rejected status, got %s", status)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/core/evolution/tasks", nil)
	listReq.Header.Set("X-User-Id", "user-1")
	listRec := httptest.NewRecorder()
	ListTasks(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list tasks failed: code=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), "async_job_id") {
		t.Fatalf("task response must not contain async_job_id: %s", listRec.Body.String())
	}

	var listResp struct {
		Code int `json:"code"`
		Data struct {
			Items []taskResponse `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list tasks: %v", err)
	}
	if len(listResp.Data.Items) != 1 || listResp.Data.Items[0].ID != "task-user-1" {
		t.Fatalf("unexpected task list: %#v", listResp.Data.Items)
	}
}

func TestListMemoryReviewResultsHidesUnmappedRows(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createMemoryReviewTable(t, db)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertMemoryResource(t, db, orm.SystemMemory{ID: "memory-1", UserID: "user-1", Content: "old memory", ContentHash: evolution.HashContent("old memory"), Version: 1, AutoEvo: false, CreatedAt: now, UpdatedAt: now})
	insertMemoryReviewResult(t, db, MemoryReviewResult{ID: "mapped", UserID: "user-1", Target: orm.ResourceUpdateResourceTypeMemory, Content: "new memory", State: memoryReviewStateSuccess, ReviewStatus: reviewStatusPending, Time: now})
	insertMemoryReviewResult(t, db, MemoryReviewResult{ID: "unmapped-preference", UserID: "user-1", Target: orm.ResourceUpdateResourceTypeUserPreference, Content: "---\nagent_persona: a\npreferred_name: b\nresponse_style: c\n---\n\nbody", State: memoryReviewStateSuccess, ReviewStatus: reviewStatusPending, Time: now.Add(time.Second)})

	req := httptest.NewRequest(http.MethodGet, "/api/core/memory-review-results", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()

	ListMemoryReviewResults(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list memory review results failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Items []memoryReviewResultResponse `json:"items"`
			Total int                          `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Total != 1 || len(resp.Data.Items) != 1 || resp.Data.Items[0].ID != "mapped" {
		t.Fatalf("expected only mapped memory review result, got total=%d items=%#v", resp.Data.Total, resp.Data.Items)
	}
}

func TestAcceptUserPreferenceReviewResultParsesFrontmatter(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createMemoryReviewTable(t, db)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	resource := orm.SystemUserPreference{
		ID:            "preference-1",
		UserID:        "user-1",
		Content:       "旧正文",
		AgentPersona:  "旧角色",
		PreferredName: "旧称谓",
		ResponseStyle: "旧风格",
		Version:       1,
		AutoEvo:       false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	resource.ContentHash = evolution.HashSystemUserPreference(resource)
	insertPreferenceResource(t, db, resource)
	reviewContent := "---\nagent_persona: 新角色\npreferred_name: 用户称谓\nresponse_style: 回复风格\n---\n\n新正文"
	insertMemoryReviewResult(t, db, MemoryReviewResult{
		ID:           "preference-accept",
		UserID:       "user-1",
		Target:       orm.ResourceUpdateResourceTypeUserPreference,
		Content:      reviewContent,
		State:        memoryReviewStateSuccess,
		ReviewStatus: reviewStatusPending,
		Time:         now,
	})

	req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/api/core/memory-review-results/preference-accept:accept", nil), map[string]string{"review_result_id": "preference-accept"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()

	AcceptMemoryReviewResult(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("accept user_preference failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated orm.SystemUserPreference
	if err := db.Take(&updated, "id = ?", "preference-1").Error; err != nil {
		t.Fatalf("read preference: %v", err)
	}
	if updated.Content != "新正文" || updated.AgentPersona != "新角色" || updated.PreferredName != "用户称谓" || updated.ResponseStyle != "回复风格" {
		t.Fatalf("expected frontmatter to be split into preference columns, got %#v", updated)
	}
	if strings.Contains(updated.Content, "agent_persona") || strings.Contains(updated.Content, "---") {
		t.Fatalf("content should not keep raw frontmatter, got %q", updated.Content)
	}
	if updated.ContentHash != evolution.HashSystemUserPreference(updated) {
		t.Fatalf("expected hash over split preference, got %q", updated.ContentHash)
	}
	if status := memoryReviewStatus(t, db, "preference-accept"); status != reviewStatusAccepted {
		t.Fatalf("expected accepted status, got %s", status)
	}
}

func newResourceUpdateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "resource-update.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.Conversation{},
		&orm.ChatHistory{},
		&orm.ResourceUpdateTask{},
		&orm.SkillReviewSchedulerState{},
		&orm.SkillResource{},
		&orm.SystemMemory{},
		&orm.SystemUserPreference{},
		&orm.ResourceVersion{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	db.DB.Logger = gormlogger.New(log.New(testLogWriter{t: t}, "", 0), gormlogger.Config{LogLevel: gormlogger.Silent, IgnoreRecordNotFoundError: true})
	return db.DB
}

type testLogWriter struct {
	t *testing.T
}

func (w testLogWriter) Write(p []byte) (int, error) {
	if w.t != nil {
		w.t.Log(strings.TrimSpace(string(p)))
	}
	return len(p), nil
}

func insertConversation(t *testing.T, db *gorm.DB, id, userID string, now time.Time) {
	t.Helper()
	err := db.Create(&orm.Conversation{
		ID: id,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error
	if err != nil {
		t.Fatalf("insert conversation %s: %v", id, err)
	}
}

func insertHistory(t *testing.T, db *gorm.DB, id, convID string, createTime time.Time, rawContent, content string, toolCallTurns int) {
	t.Helper()
	err := db.Create(&orm.ChatHistory{
		ID:             id,
		Seq:            int(createTime.Unix()),
		ConversationID: convID,
		RawContent:     rawContent,
		Content:        content,
		ToolCallTurns:  toolCallTurns,
		TimeMixin: orm.TimeMixin{
			CreateTime: createTime,
			UpdateTime: createTime,
		},
	}).Error
	if err != nil {
		t.Fatalf("insert history %s: %v", id, err)
	}
}

func insertTask(t *testing.T, db *gorm.DB, task orm.ResourceUpdateTask) orm.ResourceUpdateTask {
	t.Helper()
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("insert task %s: %v", task.ID, err)
	}
	return task
}

func insertSchedulerState(t *testing.T, db *gorm.DB, state orm.SkillReviewSchedulerState) {
	t.Helper()
	if err := db.Create(&state).Error; err != nil {
		t.Fatalf("insert scheduler state %s: %v", state.UserID, err)
	}
}

func marshalJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return body
}

func assertRequestJSONHasNoSensitiveFields(t *testing.T, body json.RawMessage) {
	t.Helper()
	text := string(body)
	for _, forbidden := range []string{"llm_config", "model_configs", "api_key", "min_user_turns", "min_tool_turns"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("request_json contains forbidden field %q: %s", forbidden, text)
		}
	}
}

func createSkillReviewResultsTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`
CREATE TABLE skill_review_results (
	id varchar(128) PRIMARY KEY,
	skill_name varchar(255) NOT NULL,
	type varchar(32) NOT NULL,
	userid varchar(255) NOT NULL,
	requestid varchar(128) NOT NULL,
	skill_content text NOT NULL,
	summary text,
	review_status varchar(32) NOT NULL,
	time datetime NOT NULL
)`).Error; err != nil {
		t.Fatalf("create skill_review_results: %v", err)
	}
}

func createMemoryReviewTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`
CREATE TABLE memory_review (
	id varchar(128) PRIMARY KEY,
	user_id varchar(255) NOT NULL DEFAULT '',
	target varchar(32) NOT NULL DEFAULT '',
	session_id varchar(128) NOT NULL DEFAULT '',
	source_content text NOT NULL DEFAULT '',
	content text NOT NULL DEFAULT '',
	operations json,
	state varchar(32) NOT NULL DEFAULT '',
	review_status varchar(32) NOT NULL,
	time datetime NOT NULL
)`).Error; err != nil {
		t.Fatalf("create memory_review: %v", err)
	}
}

func insertSkillReviewResult(t *testing.T, db *gorm.DB, id, userID, status string, now time.Time) {
	t.Helper()
	if err := db.Table("skill_review_results").Create(map[string]any{
		"id":            id,
		"skill_name":    "",
		"type":          "",
		"userid":        userID,
		"requestid":     "",
		"skill_content": "",
		"summary":       "",
		"review_status": status,
		"time":          now,
	}).Error; err != nil {
		t.Fatalf("insert skill review result %s: %v", id, err)
	}
}

func insertFullSkillReviewResult(t *testing.T, db *gorm.DB, row SkillReviewResult) {
	t.Helper()
	if row.ReviewStatus == "" {
		row.ReviewStatus = reviewStatusPending
	}
	if row.Time.IsZero() {
		row.Time = time.Now()
	}
	if err := db.Table("skill_review_results").Create(map[string]any{
		"id":            row.ID,
		"skill_name":    row.SkillName,
		"type":          row.Type,
		"userid":        row.UserID,
		"requestid":     row.RequestID,
		"skill_content": row.SkillContent,
		"summary":       row.Summary,
		"review_status": row.ReviewStatus,
		"time":          row.Time,
	}).Error; err != nil {
		t.Fatalf("insert full skill review result %s: %v", row.ID, err)
	}
}

func insertMemoryReviewResult(t *testing.T, db *gorm.DB, row MemoryReviewResult) {
	t.Helper()
	if row.ReviewStatus == "" {
		row.ReviewStatus = reviewStatusPending
	}
	if row.Time.IsZero() {
		row.Time = time.Now()
	}
	if err := db.Table("memory_review").Create(map[string]any{
		"id":             row.ID,
		"user_id":        row.UserID,
		"target":         row.Target,
		"session_id":     row.SessionID,
		"source_content": row.SourceContent,
		"content":        row.Content,
		"operations":     row.Operations,
		"state":          row.State,
		"review_status":  row.ReviewStatus,
		"time":           row.Time,
	}).Error; err != nil {
		t.Fatalf("insert memory review result %s: %v", row.ID, err)
	}
}

func skillReviewResultStatus(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	var row struct {
		ReviewStatus string
	}
	if err := db.Table("skill_review_results").Select("review_status").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("read skill review result %s: %v", id, err)
	}
	return row.ReviewStatus
}

func memoryReviewStatus(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	var row struct {
		ReviewStatus string
	}
	if err := db.Table("memory_review").Select("review_status").Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatalf("read memory review result %s: %v", id, err)
	}
	return row.ReviewStatus
}

func insertSkillResource(t *testing.T, db *gorm.DB, row orm.SkillResource) {
	t.Helper()
	if row.RelativePath == "" && row.NodeType == evolution.SkillNodeTypeParent {
		row.RelativePath = evolution.ParentSkillRelativePath(row.Category, row.SkillName)
	}
	if row.FileExt == "" {
		row.FileExt = "md"
	}
	if row.MimeType == "" {
		row.MimeType = "text/markdown; charset=utf-8"
	}
	if row.ContentSize == 0 && row.Content != "" {
		row.ContentSize = int64(len([]byte(row.Content)))
	}
	if row.UpdateStatus == "" {
		row.UpdateStatus = evolution.UpdateStatusUpToDate
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("insert skill resource %s: %v", row.ID, err)
	}
}

func insertMemoryResource(t *testing.T, db *gorm.DB, row orm.SystemMemory) {
	t.Helper()
	autoEvo := row.AutoEvo
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("insert memory resource %s: %v", row.ID, err)
	}
	if err := db.Model(&orm.SystemMemory{}).Where("id = ?", row.ID).Update("auto_evo", autoEvo).Error; err != nil {
		t.Fatalf("set memory auto_evo %s: %v", row.ID, err)
	}
}

func insertPreferenceResource(t *testing.T, db *gorm.DB, row orm.SystemUserPreference) {
	t.Helper()
	autoEvo := row.AutoEvo
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("insert preference resource %s: %v", row.ID, err)
	}
	if err := db.Model(&orm.SystemUserPreference{}).Where("id = ?", row.ID).Update("auto_evo", autoEvo).Error; err != nil {
		t.Fatalf("set preference auto_evo %s: %v", row.ID, err)
	}
}

func skillContent(name, body string) string {
	return skillContentWithCategory(name, name+" description", body, "")
}

func skillContentWithCategory(name, description, body, category string) string {
	lines := []string{
		"---",
		"name: " + name,
		"description: " + description,
	}
	if strings.TrimSpace(category) != "" {
		lines = append(lines, "category: "+category)
	}
	lines = append(lines, "---", body)
	return strings.Join(lines, "\n")
}
