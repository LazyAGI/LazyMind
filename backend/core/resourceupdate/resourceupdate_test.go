package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"lazymind/core/preferencefile"
	"lazymind/core/skillv2/taskguard"
	"lazymind/core/state"
	"lazymind/core/store"
)

func TestSkillReviewWorkerDefersWithoutConsumingAttempt(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	insertSkillReviewStats(t, db, map[string]any{
		"id": "org-running", "requestid": "org-running", "userid": "user-1", "status": "organize_apply",
		"started_at": now.Format(time.RFC3339Nano), "duration_ms": 0, "summary": "{}",
	})
	insertTask(t, db, orm.ResourceUpdateTask{
		ID: "scheduled-review", TaskType: orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill, UserID: "user-1",
		TriggerType: orm.ResourceUpdateTriggerTypeScheduled, TriggerID: "scheduled-review",
		Status: orm.ResourceUpdateTaskStatusPending,
		RequestJSON: marshalJSON(t, skillGenerateRequestJSON{
			RequestID: "review_scheduled", UserID: "user-1", WindowFrozen: true,
			StartTime: formatTaskTime(now.Add(-time.Hour)), EndTime: formatTaskTime(now),
			QualifiedSessionCount: 1, QuantityThreshold: 1,
		}),
		NextRunAt: now, CreatedAt: now, UpdatedAt: now,
	})
	worker := NewWorker(db, Config{WorkerBatchSize: 1, WorkerLockTTL: time.Minute, MaxAttempts: 1}, "defer-review")
	worker.clock = func() time.Time { return now }
	called := false
	worker.callers.Skill = func(context.Context, algo.SkillReviewRequest) (*algo.SkillReviewResponse, int, error) {
		called = true
		return nil, 0, nil
	}
	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if called || result.Retried != 1 || result.Failed != 0 {
		t.Fatalf("unexpected deferred result: called=%v result=%#v", called, result)
	}
	var task orm.ResourceUpdateTask
	if err := db.Take(&task, "id = ?", "scheduled-review").Error; err != nil {
		t.Fatalf("load deferred task: %v", err)
	}
	if task.Status != orm.ResourceUpdateTaskStatusPending || task.AttemptCount != 0 || !task.NextRunAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("deferred task = %#v", task)
	}
}

func TestAutoEvoDraftWaitsForEditorThenCommits(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	createMemoryReviewTable(t, db)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	insertSkillResource(t, db, orm.SkillResource{
		ID: "skill-auto-draft", OwnerUserID: "user-1", Category: "system", SkillName: "auto-draft",
		Content: skillContent("auto-draft", "old"), Version: 1, AutoEvo: true, IsEnabled: true,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), CreateUserID: "user-1",
	})
	newContent := skillContent("auto-draft", "new")
	newHash := evolution.HashContent(newContent)
	if err := db.Create(&orm.SkillV2Blob{
		Hash: newHash, Size: int64(len(newContent)), Mime: "text/markdown; charset=utf-8", FileType: "markdown",
		StorageBackend: "postgres", Content: []byte(newContent), CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("insert draft blob: %v", err)
	}
	if err := db.Model(&orm.SkillV2Draft{}).Where("skill_id = ?", "skill-auto-draft").Updates(map[string]any{
		"task_id": "session-editor", "conversation_id": "session-editor", "version": 2, "draft_updated_at": now, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("claim draft: %v", err)
	}
	if err := db.Create(&orm.SkillV2DraftEntry{
		SkillID: "skill-auto-draft", Path: "SKILL.md", Op: "upsert", EntryType: "file", BlobHash: &newHash,
		Size: int64(len(newContent)), Mime: "text/markdown; charset=utf-8", FileType: "markdown", Mode: 0o644, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("insert draft entry: %v", err)
	}

	scanner := NewScanner(db, Config{}, "draft-scanner")
	scanner.clock = func() time.Time { return now }
	scanResult, err := scanner.RunOnce(context.Background())
	if err != nil || scanResult.SkillDraftTasksCreated != 1 {
		t.Fatalf("scan auto draft: result=%#v err=%v", scanResult, err)
	}
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	if err := stateStore.Set(context.Background(), "lazymind:conversation_idle:ttl:session-editor", []byte("active"), time.Minute); err != nil {
		t.Fatalf("set editor activity: %v", err)
	}
	workerNow := now
	worker := NewWorker(db, Config{WorkerBatchSize: 1, WorkerLockTTL: time.Minute, MaxAttempts: 1}, "draft-worker", stateStore)
	worker.clock = func() time.Time { return workerNow }
	first, err := worker.RunOnce(context.Background())
	if err != nil || first.Retried != 1 {
		t.Fatalf("defer auto commit: result=%#v err=%v", first, err)
	}
	var task orm.ResourceUpdateTask
	if err := db.Where("task_type = ?", orm.ResourceUpdateTaskTypeAutoCommitSkillDraft).Take(&task).Error; err != nil {
		t.Fatalf("load auto commit task: %v", err)
	}
	if task.AttemptCount != 0 || task.Status != orm.ResourceUpdateTaskStatusPending {
		t.Fatalf("deferred auto commit task=%#v", task)
	}
	if err := stateStore.Del(context.Background(), "lazymind:conversation_idle:ttl:session-editor"); err != nil {
		t.Fatalf("clear editor activity: %v", err)
	}
	workerNow = now.Add(time.Minute)
	second, err := worker.RunOnce(context.Background())
	if err != nil || second.Done != 1 {
		t.Fatalf("complete auto commit: result=%#v err=%v", second, err)
	}
	if got := readSkillV2HeadContent(t, db, "skill-auto-draft"); got != newContent {
		t.Fatalf("committed content = %q, want %q", got, newContent)
	}
	var draft orm.SkillV2Draft
	if err := db.Take(&draft, "skill_id = ?", "skill-auto-draft").Error; err != nil {
		t.Fatalf("load committed draft: %v", err)
	}
	if draft.TaskID != "" || draft.ConversationID != nil || draft.DraftUpdatedAt != nil {
		t.Fatalf("draft ownership not cleared: %#v", draft)
	}
}

func TestAutoEvoCreateDraftCommitsInitialRevisionAfterEditorIdle(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	createMemoryReviewTable(t, db)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	content := skillContent("auto-created", "initial")
	hash := evolution.HashContent(content)
	if err := db.Create(&orm.SkillV2Skill{
		ID:                 "skill-auto-create",
		OwnerUserID:        "user-1",
		CreateUserID:       "user-1",
		Category:           "system",
		SkillName:          "auto-created",
		Tags:               []byte("[]"),
		RelativeRoot:       "system/auto-created",
		SkillMDPath:        "SKILL.md",
		Version:            1,
		AutoEvo:            true,
		AutoEvoApplyStatus: "idle",
		IsEnabled:          false,
		UpdateStatus:       evolution.UpdateStatusUpToDate,
		CreatedAt:          now,
		UpdatedAt:          now,
	}).Error; err != nil {
		t.Fatalf("insert create draft skill: %v", err)
	}
	if err := db.Create(&orm.SkillV2Blob{
		Hash:           hash,
		Size:           int64(len(content)),
		Mime:           "text/markdown; charset=utf-8",
		FileType:       "markdown",
		StorageBackend: "postgres",
		Content:        []byte(content),
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("insert create draft blob: %v", err)
	}
	if err := db.Create(&orm.SkillV2Draft{
		SkillID:     "skill-auto-create",
		DraftStatus: "auto_pending",
		TaskID:      "session-editor",
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("insert create draft: %v", err)
	}
	if err := db.Create(&orm.SkillV2DraftEntry{
		SkillID:   "skill-auto-create",
		Path:      "SKILL.md",
		Op:        "upsert",
		EntryType: "file",
		BlobHash:  &hash,
		Size:      int64(len(content)),
		Mime:      "text/markdown; charset=utf-8",
		FileType:  "markdown",
		Mode:      0o644,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("insert create draft entry: %v", err)
	}

	scanner := NewScanner(db, Config{}, "draft-scanner")
	scanner.clock = func() time.Time { return now }
	scanResult, err := scanner.RunOnce(context.Background())
	if err != nil || scanResult.SkillDraftTasksCreated != 1 {
		t.Fatalf("scan create draft: result=%#v err=%v", scanResult, err)
	}
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	worker := NewWorker(db, Config{WorkerBatchSize: 1, WorkerLockTTL: time.Minute, MaxAttempts: 1}, "draft-worker", stateStore)
	worker.clock = func() time.Time { return now }
	result, err := worker.RunOnce(context.Background())
	if err != nil || result.Done != 1 {
		t.Fatalf("auto commit create draft: result=%#v err=%v", result, err)
	}

	var skill orm.SkillV2Skill
	if err := db.Where("id = ?", "skill-auto-create").Take(&skill).Error; err != nil {
		t.Fatalf("query committed skill: %v", err)
	}
	if skill.HeadRevisionID == nil {
		t.Fatal("auto commit did not create head revision")
	}
	var revision orm.SkillV2Revision
	if err := db.Where("id = ?", *skill.HeadRevisionID).Take(&revision).Error; err != nil {
		t.Fatalf("query initial revision: %v", err)
	}
	if revision.RevisionNo != 1 || revision.ParentRevisionID != nil {
		t.Fatalf("initial revision = %#v", revision)
	}
	if got := readSkillV2HeadContent(t, db, "skill-auto-create"); got != content {
		t.Fatalf("committed content = %q, want %q", got, content)
	}
	var draft orm.SkillV2Draft
	if err := db.Where("skill_id = ?", "skill-auto-create").Take(&draft).Error; err != nil {
		t.Fatalf("query reset draft: %v", err)
	}
	if draft.BaseRevisionID == nil || *draft.BaseRevisionID != *skill.HeadRevisionID || draft.DraftStatus != "" {
		t.Fatalf("reset draft = %#v", draft)
	}
}

func TestAcceptSkillReviewResultsMapsDraftTaskIDToRequestID(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	insertSkillReviewStats(t, db, map[string]any{
		"id":          "review_request_20260713100000_user-1",
		"requestid":   "review_request",
		"userid":      "user-1",
		"status":      "completed",
		"started_at":  now.Format(time.RFC3339Nano),
		"duration_ms": 100,
		"summary":     "{}",
	})
	insertFullSkillReviewResult(t, db, SkillReviewResult{
		ID:           "result-1",
		UserID:       "user-1",
		RequestID:    "review_request",
		ReviewStatus: reviewStatusPending,
		Time:         now,
	})

	if err := acceptSkillReviewResultsForDraftTask(context.Background(), db, "user-1", "review_request_20260713100000_user-1"); err != nil {
		t.Fatalf("accept review result: %v", err)
	}
	if status := skillReviewResultStatus(t, db, "result-1"); status != reviewStatusAccepted {
		t.Fatalf("review result status = %q, want accepted", status)
	}
}

func TestAutoEvoDraftDefersWhenEditorStatusIsUnavailable(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	insertSkillResource(t, db, orm.SkillResource{
		ID: "skill-status-unavailable", OwnerUserID: "user-1", SkillName: "status-unavailable",
		Content: skillContent("status-unavailable", "old"), Version: 1, AutoEvo: true,
		CreatedAt: now, UpdatedAt: now, CreateUserID: "user-1",
	})
	if err := db.Model(&orm.SkillV2Draft{}).Where("skill_id = ?", "skill-status-unavailable").Updates(map[string]any{
		"task_id": "session-editor", "conversation_id": "session-editor", "version": 2,
	}).Error; err != nil {
		t.Fatalf("claim draft: %v", err)
	}
	if err := db.Create(&orm.SkillV2DraftEntry{
		SkillID: "skill-status-unavailable", Path: "SKILL.md", Op: "delete", UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("insert draft entry: %v", err)
	}
	worker := NewWorker(db, Config{}, "status-unavailable-worker")
	outcome := worker.handleAutoCommitSkillDraft(context.Background(), orm.ResourceUpdateTask{
		UserID:     "user-1",
		ResourceID: "skill-status-unavailable",
		RequestJSON: marshalJSON(t, skillDraftAutoCommitRequestJSON{
			TaskID:       "session-editor",
			DraftVersion: 2,
		}),
	})
	if !outcome.Deferred || outcome.ErrorCode != taskguard.ReasonTaskStatusUnavailable {
		t.Fatalf("outcome = %#v, want deferred status-unavailable", outcome)
	}
}

func TestCountSkillReviewHistoryStatsFiltersUserAndHalfOpenWindow(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	start := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	insertConversation(t, db, "conv-u1", "user-1", start)
	insertConversation(t, db, "conv-at-end", "user-1", end)
	insertConversation(t, db, "conv-before", "user-1", start.Add(-time.Nanosecond))
	insertConversation(t, db, "conv-u2", "user-2", start)

	insertHistory(t, db, "h-before-history", "conv-u1", start.Add(-time.Nanosecond), "before history", "", 2)
	insertHistory(t, db, "h-end-history", "conv-u1", end, "end history", "", 1)
	insertHistory(t, db, "h-conv-at-end", "conv-at-end", end, "conversation at end", "", 9)
	insertHistory(t, db, "h-conv-before", "conv-before", start, "conversation before", "", 9)
	insertHistory(t, db, "h-other-user", "conv-u2", start.Add(15*time.Minute), "other", "", 9)

	stats, err := CountSkillReviewHistoryStats(ctx, db, "user-1", start, end, 2, 6)
	if err != nil {
		t.Fatalf("count stats: %v", err)
	}
	if stats.UserTurnCount != 2 {
		t.Fatalf("expected 2 user turns, got %d", stats.UserTurnCount)
	}
	if stats.ToolCallCount != 3 {
		t.Fatalf("expected tool call sum 3, got %d", stats.ToolCallCount)
	}
	if stats.QualifiedSessionCount != 0 {
		t.Fatalf("expected no qualified session, got %d", stats.QualifiedSessionCount)
	}
}

func TestCountSkillReviewHistoryStatsCountsTrajectoryToolCalls(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	start := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	insertConversation(t, db, "conv-u1", "user-1", start.Add(10*time.Minute))
	insertHistoryWithResult(
		t,
		db,
		"h-multi-tool",
		"conv-u1",
		start.Add(10*time.Minute),
		"turn one",
		"",
		toolCallResultTags("h-multi-tool", 5),
		0,
	)
	insertHistory(t, db, "h-two", "conv-u1", start.Add(20*time.Minute), "turn two", "", 2)
	insertHistory(t, db, "h-three", "conv-u1", start.Add(30*time.Minute), "turn three", "", 1)

	stats, err := CountSkillReviewHistoryStats(ctx, db, "user-1", start, end, 3, 8)
	if err != nil {
		t.Fatalf("count stats: %v", err)
	}
	if stats.UserTurnCount != 3 || stats.ToolCallCount != 8 || stats.QualifiedSessionCount != 1 {
		t.Fatalf("expected trajectory-style 3/8/1, got %#v", stats)
	}
}

func TestCountSkillReviewHistoryStatsDoesNotCombineWeakConversations(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	start := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	insertSkillReviewConversation(t, db, "conv-a", "user-1", start.Add(10*time.Minute), 1, 3)
	insertSkillReviewConversation(t, db, "conv-b", "user-1", start.Add(20*time.Minute), 1, 3)
	insertSkillReviewConversation(t, db, "conv-c", "user-1", start.Add(30*time.Minute), 1, 2)

	stats, err := CountSkillReviewHistoryStats(ctx, db, "user-1", start, end, 3, 8)
	if err != nil {
		t.Fatalf("count stats: %v", err)
	}
	if stats.UserTurnCount != 3 || stats.ToolCallCount != 8 {
		t.Fatalf("expected aggregate 3/8, got user=%d tool=%d", stats.UserTurnCount, stats.ToolCallCount)
	}
	if stats.QualifiedSessionCount != 0 {
		t.Fatalf("weak conversations must not count as qualified sessions, got %d", stats.QualifiedSessionCount)
	}
}

func TestDefaultSkillReviewStageQuantityThresholds(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(cfg.Stages))
	}
	want := []int{5, 10, 20}
	for index, threshold := range want {
		if cfg.Stages[index].QuantityThreshold != threshold {
			t.Fatalf("stage %d quantity threshold: got %d want %d", index, cfg.Stages[index].QuantityThreshold, threshold)
		}
	}
}

func TestManualSkillReviewSummaryCountsDepositableSessions(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	insertSkillReviewConversation(t, db, "conv-qualified", "user-1", start.Add(10*time.Minute), 3, 8)
	insertSkillReviewConversation(t, db, "conv-weak-a", "user-1", start.Add(20*time.Minute), 1, 3)
	insertSkillReviewConversation(t, db, "conv-weak-b", "user-1", start.Add(30*time.Minute), 2, 4)
	insertSkillReviewConversation(t, db, "conv-other", "user-2", start.Add(40*time.Minute), 3, 8)
	insertSchedulerState(t, db, orm.SkillReviewSchedulerState{
		UserID:        "user-1",
		LastWindowEnd: start,
		NextRunAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	summary, err := buildManualSkillReviewSummary(ctx, db, "user-1", DefaultConfig(), now)
	if err != nil {
		t.Fatalf("build summary: %v", err)
	}
	if summary.QualifiedSessionCount != 1 {
		t.Fatalf("expected one qualified session, got %#v", summary)
	}
	if summary.UserTurnCount != 6 || summary.ToolCallCount != 15 {
		t.Fatalf("expected aggregate counts preserved for observability, got %#v", summary)
	}
	if !summary.WindowStart.Equal(start) || !summary.WindowEnd.Equal(now) {
		t.Fatalf("unexpected window: %#v", summary)
	}
	if summary.MinUserTurns != 3 || summary.MinToolTurns != 8 || summary.QuantityThreshold != 1 {
		t.Fatalf("unexpected thresholds: %#v", summary)
	}
}

func TestManualSkillReviewSummarySettlesDoneActiveTask(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	windowEnd := now.Add(-10 * time.Minute)
	insertSkillReviewConversation(t, db, "conv-reviewed", "user-1", start.Add(10*time.Minute), 3, 8)
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "done-active-task",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		TriggerType:  orm.ResourceUpdateTriggerTypeManual,
		TriggerID:    "skill-review:done-active-task",
		Status:       orm.ResourceUpdateTaskStatusDone,
		RequestJSON: marshalJSON(t, skillGenerateRequestJSON{
			RequestID:    "done-request",
			UserID:       "user-1",
			StartTime:    formatTaskTime(start),
			EndTime:      formatTaskTime(windowEnd),
			WindowFrozen: true,
		}),
		NextRunAt:  start,
		CreatedAt:  start,
		UpdatedAt:  windowEnd,
		FinishedAt: ptrTime(windowEnd),
	})
	insertSchedulerState(t, db, orm.SkillReviewSchedulerState{
		UserID:        "user-1",
		LastWindowEnd: start,
		NextRunAt:     now.Add(time.Hour),
		ActiveTaskID:  "done-active-task",
		CreatedAt:     start,
		UpdatedAt:     windowEnd,
	})

	summary, err := buildManualSkillReviewSummary(ctx, db, "user-1", DefaultConfig(), now)
	if err != nil {
		t.Fatalf("build summary: %v", err)
	}
	if summary.QualifiedSessionCount != 0 || !summary.WindowStart.Equal(windowEnd) || summary.RunningTask != nil {
		t.Fatalf("expected done active task to be settled before summary, got %#v", summary)
	}
	var state orm.SkillReviewSchedulerState
	if err := db.First(&state, "user_id = ?", "user-1").Error; err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.ActiveTaskID != "" || !state.LastWindowEnd.Equal(windowEnd) {
		t.Fatalf("expected state window advanced after summary settlement, got %#v", state)
	}
}

func TestCreateManualSkillReviewTaskFreezesWindowAndBlocksDuplicate(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	insertSkillReviewConversation(t, db, "conv-qualified", "user-1", start.Add(10*time.Minute), 3, 8)
	insertSchedulerState(t, db, orm.SkillReviewSchedulerState{
		UserID:        "user-1",
		LastWindowEnd: start,
		NextRunAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	task, summary, err := createManualSkillReviewTask(ctx, db, "user-1", DefaultConfig(), now)
	if err != nil {
		t.Fatalf("create manual task: %v", err)
	}
	if task.TriggerType != orm.ResourceUpdateTriggerTypeManual ||
		task.TaskType != orm.ResourceUpdateTaskTypeGenerateReview ||
		task.ResourceType != orm.ResourceUpdateResourceTypeSkill ||
		task.Status != orm.ResourceUpdateTaskStatusPending {
		t.Fatalf("unexpected manual task: %#v", task)
	}
	if summary.QualifiedSessionCount != 1 || summary.RunningTask == nil || summary.RunningTask.ID != task.ID || summary.RunningRequestID == "" {
		t.Fatalf("unexpected summary after manual run: %#v", summary)
	}
	var frozen skillGenerateRequestJSON
	if err := json.Unmarshal(task.RequestJSON, &frozen); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if !frozen.WindowFrozen ||
		frozen.StartTriggerReason != "manual" ||
		frozen.TriggerReason != "manual" ||
		frozen.QuantityThreshold != 1 ||
		frozen.QualifiedSessionCount != 1 ||
		frozen.RequestID != summary.RunningRequestID ||
		frozen.StartTime != formatTaskTime(start) ||
		frozen.EndTime != formatTaskTime(now) {
		t.Fatalf("unexpected frozen request: %#v", frozen)
	}
	var state orm.SkillReviewSchedulerState
	if err := db.First(&state, "user_id = ?", "user-1").Error; err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.ActiveTaskID != task.ID {
		t.Fatalf("expected manual task to become active task, got %#v", state)
	}

	if _, _, err := createManualSkillReviewTask(ctx, db, "user-1", DefaultConfig(), now); !errors.Is(err, errReviewConflict) {
		t.Fatalf("expected duplicate manual run to conflict, got %v", err)
	}
}

func TestCreateManualSkillReviewTaskReleasesTerminalActiveTask(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	insertSkillReviewConversation(t, db, "conv-qualified", "user-1", start.Add(10*time.Minute), 3, 8)
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "failed-active-task",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		TriggerType:  orm.ResourceUpdateTriggerTypeScheduled,
		TriggerID:    "skill-review:failed-active-task",
		Status:       orm.ResourceUpdateTaskStatusFailed,
		ErrorCode:    "skill_review_call_failed",
		ErrorMessage: "chat service refused connection",
		RequestJSON: marshalJSON(t, skillGenerateRequestJSON{
			RequestID:    "failed-request",
			UserID:       "user-1",
			StartTime:    formatTaskTime(start),
			EndTime:      formatTaskTime(now.Add(-time.Hour)),
			WindowFrozen: true,
		}),
		NextRunAt:  now.Add(-time.Hour),
		CreatedAt:  now.Add(-time.Hour),
		UpdatedAt:  now.Add(-time.Hour),
		FinishedAt: ptrTime(now.Add(-30 * time.Minute)),
	})
	insertSchedulerState(t, db, orm.SkillReviewSchedulerState{
		UserID:           "user-1",
		LastWindowEnd:    start,
		NextRunAt:        now.Add(time.Hour),
		ActiveTaskID:     "failed-active-task",
		LastErrorCode:    "skill_review_call_failed",
		LastErrorMessage: "chat service refused connection",
		CreatedAt:        now.Add(-time.Hour),
		UpdatedAt:        now.Add(-time.Hour),
	})

	task, summary, err := createManualSkillReviewTask(ctx, db, "user-1", DefaultConfig(), now)
	if err != nil {
		t.Fatalf("create manual task with terminal active task: %v", err)
	}
	if task.TriggerType != orm.ResourceUpdateTriggerTypeManual || summary.RunningTask == nil || summary.RunningTask.ID != task.ID {
		t.Fatalf("unexpected manual task after releasing terminal active task: task=%#v summary=%#v", task, summary)
	}
	var state orm.SkillReviewSchedulerState
	if err := db.First(&state, "user_id = ?", "user-1").Error; err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.ActiveTaskID != task.ID || state.LastErrorCode != "" || state.LastErrorMessage != "" {
		t.Fatalf("expected manual task to replace failed active task, got %#v", state)
	}
}

func TestSkillReviewTaskListIncludesPendingAndDoneCoreTaskUntilRunStats(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewStatsTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "pending-task",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		TriggerType:  orm.ResourceUpdateTriggerTypeManual,
		TriggerID:    "skill_review_manual:user-1:req-pending",
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON: marshalJSON(t, skillGenerateRequestJSON{
			RequestID:    "req-pending",
			UserID:       "user-1",
			WindowFrozen: true,
		}),
		NextRunAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	})
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "manual-task",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		TriggerType:  orm.ResourceUpdateTriggerTypeManual,
		TriggerID:    "skill_review_manual:user-1:req-1",
		Status:       orm.ResourceUpdateTaskStatusDone,
		RequestJSON: marshalJSON(t, skillGenerateRequestJSON{
			RequestID:    "req-1",
			UserID:       "user-1",
			WindowFrozen: true,
		}),
		NextRunAt:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		StartedAt:  ptrTime(now),
		FinishedAt: ptrTime(now.Add(time.Second)),
	})

	resp, err := buildSkillReviewTaskList(ctx, db, "user-1", orm.ResourceUpdateTaskStatusRunning, "", 1, 1000)
	if err != nil {
		t.Fatalf("build task list: %v", err)
	}
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Fatalf("expected pending and done core task to stay in running list, got %#v", resp)
	}
	statuses := make(map[string]string, len(resp.Items))
	for _, item := range resp.Items {
		statuses[item.RequestID] = item.Status
	}
	if statuses["req-pending"] != orm.ResourceUpdateTaskStatusPending ||
		statuses["req-1"] != orm.ResourceUpdateTaskStatusRunning {
		t.Fatalf("unexpected running list statuses: %#v", statuses)
	}

	filtered, err := buildSkillReviewTaskList(ctx, db, "user-1", orm.ResourceUpdateTaskStatusRunning, "req-1", 1, 1000)
	if err != nil {
		t.Fatalf("build request filtered task list: %v", err)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].RequestID != "req-1" {
		t.Fatalf("expected request filter to keep only req-1, got %#v", filtered)
	}
}

func TestSkillReviewTaskListDropsCompletedRunFromRunningFilter(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewStatsTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "manual-task",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		TriggerType:  orm.ResourceUpdateTriggerTypeManual,
		TriggerID:    "skill_review_manual:user-1:req-1",
		Status:       orm.ResourceUpdateTaskStatusDone,
		RequestJSON: marshalJSON(t, skillGenerateRequestJSON{
			RequestID:    "req-1",
			UserID:       "user-1",
			WindowFrozen: true,
		}),
		NextRunAt:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		StartedAt:  ptrTime(now),
		FinishedAt: ptrTime(now.Add(time.Second)),
	})
	insertSkillReviewStats(t, db, map[string]any{
		"id":          "stats-1",
		"requestid":   "req-1",
		"userid":      "user-1",
		"status":      "completed",
		"started_at":  "2026-06-09T10:00:00Z",
		"duration_ms": 94000,
		"summary": map[string]any{
			"skill_count":   1,
			"created_count": 1,
			"updated_count": 0,
		},
	})

	running, err := buildSkillReviewTaskList(ctx, db, "user-1", orm.ResourceUpdateTaskStatusRunning, "req-1", 1, 1000)
	if err != nil {
		t.Fatalf("build running task list: %v", err)
	}
	if running.Total != 0 || len(running.Items) != 0 {
		t.Fatalf("expected completed run to leave running list, got %#v", running)
	}

	all, err := buildSkillReviewTaskList(ctx, db, "user-1", "", "req-1", 1, 1000)
	if err != nil {
		t.Fatalf("build all task list: %v", err)
	}
	if all.Total != 1 || len(all.Items) != 1 ||
		all.Items[0].Status != orm.ResourceUpdateTaskStatusDone ||
		all.Items[0].RunStatus != "completed" ||
		all.Items[0].ResultCount != 1 {
		t.Fatalf("unexpected completed task status: %#v", all)
	}
}

func TestSkillReviewTaskListUsesCompletedAlgorithmRunForLegacyFailedCoreTask(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewStatsTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 7, 0, 0, 0, time.UTC)
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "legacy-failed-task",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		TriggerType:  orm.ResourceUpdateTriggerTypeManual,
		TriggerID:    "skill_review_manual:user-1:req-legacy",
		Status:       orm.ResourceUpdateTaskStatusFailed,
		RequestJSON: marshalJSON(t, skillGenerateRequestJSON{
			RequestID:    "req-legacy",
			UserID:       "user-1",
			WindowFrozen: true,
		}),
		ErrorCode:    "skill_review_unexpected_response",
		AttemptCount: 3,
		NextRunAt:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	insertSkillReviewStats(t, db, map[string]any{
		"id": "algorithm-completed", "requestid": "req-legacy", "userid": "user-1", "status": "completed",
		"started_at": "2026-07-16T15:00:01Z", "duration_ms": 1000,
		"summary": map[string]any{
			"counts": map[string]any{"draft": 1},
			"apply": map[string]any{
				"output_count": 1,
				"applied":      []map[string]any{{"type": "new", "name": "generated-skill"}},
			},
		},
	})
	insertSkillReviewStats(t, db, map[string]any{
		"id": "algorithm-zombie", "requestid": "req-legacy", "userid": "user-1", "status": "review_draft",
		"started_at": "2026-07-16T15:01:01Z", "duration_ms": 1, "summary": map[string]any{"stage": "review_draft"},
	})

	resp, err := buildSkillReviewTaskList(ctx, db, "user-1", "", "req-legacy", 1, 1000)
	if err != nil {
		t.Fatalf("build task list: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Status != orm.ResourceUpdateTaskStatusDone ||
		resp.Items[0].RunStatus != "completed" || resp.Items[0].ResultCount != 1 ||
		resp.Items[0].Task.Status != orm.ResourceUpdateTaskStatusFailed {
		t.Fatalf("unexpected reconciled task status: %#v", resp)
	}
}

func TestSkillReviewTaskListUsesBoundAlgorithmTaskID(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewStatsTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 7, 0, 0, 0, time.UTC)
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "bound-task",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		TriggerType:  orm.ResourceUpdateTriggerTypeManual,
		TriggerID:    "skill_review_manual:user-1:req-bound",
		Status:       orm.ResourceUpdateTaskStatusDone,
		ResultID:     "algorithm-bound",
		RequestJSON:  marshalJSON(t, skillGenerateRequestJSON{RequestID: "req-bound", UserID: "user-1", WindowFrozen: true}),
		NextRunAt:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	insertSkillReviewStats(t, db, map[string]any{
		"id": "algorithm-other", "requestid": "req-bound", "userid": "user-1", "status": "completed",
		"started_at": "2026-07-16T15:00:01Z", "duration_ms": 1000, "summary": map[string]any{"skill_count": 1},
	})
	insertSkillReviewStats(t, db, map[string]any{
		"id": "algorithm-bound", "requestid": "req-bound", "userid": "user-1", "status": "review_miner",
		"started_at": "2026-07-16T15:01:01Z", "duration_ms": 1, "summary": map[string]any{"stage": "review_miner"},
	})

	resp, err := buildSkillReviewTaskList(ctx, db, "user-1", "", "req-bound", 1, 1000)
	if err != nil {
		t.Fatalf("build task list: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Status != orm.ResourceUpdateTaskStatusRunning || resp.Items[0].RunStatus != "review_miner" {
		t.Fatalf("unexpected bound task status: %#v", resp)
	}
}

func TestSkillPreflightFreezesRequestAndSkipsWhenBelowThreshold(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	insertConversation(t, db, "conv-u1", "user-1", start.Add(10*time.Minute))
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
		Stages:             []Stage{{Window: time.Hour, Interval: time.Hour, QuantityThreshold: 1, Successes: 0}},
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
	if !frozen.WindowFrozen || frozen.RequestID == "" || frozen.UserTurnCount != 1 || frozen.ToolCallCount != 0 ||
		frozen.QualifiedSessionCount != 0 || frozen.QuantityThreshold != 1 {
		t.Fatalf("unexpected frozen request: %#v", frozen)
	}
	if frozen.StartTime != formatTaskTime(start) || frozen.EndTime != formatTaskTime(now) {
		t.Fatalf("expected worker to freeze state window, got %#v", frozen)
	}
	assertRequestJSONHasNoSensitiveFields(t, got.RequestJSON)
}

func TestSkillWorkerCallsReviewWithoutPendingSkillResults(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	insertConversation(t, db, "conv-u1", "user-1", start.Add(10*time.Minute))
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
		Stages:           []Stage{{Window: time.Hour, Interval: time.Hour, QuantityThreshold: 1, Successes: 0}},
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
		return &algo.SkillReviewResponse{Code: 0, Data: algo.SkillReviewData{Status: "pending", RequestID: req.RequestID, TaskID: "review_task_1"}}, 200, nil
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
	if captured.MinUserTurns != 2 || captured.MinToolTurns != 2 {
		t.Fatalf("skill review request should use backend thresholds, got %#v", captured)
	}
	if !strings.HasPrefix(captured.RequestID, skillReviewRequestIDPrefix) {
		t.Fatalf("skill review requestid should use review task mode, got %#v", captured.RequestID)
	}
	if strings.Contains(string(capturedBody), "skill_base_dir") || strings.Contains(string(capturedBody), "fs_base_url") {
		t.Fatalf("skill review request must not include non-contract fields: %s", string(capturedBody))
	}

	var got orm.ResourceUpdateTask
	if err := db.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("read task: %v", err)
	}
	if got.Status != orm.ResourceUpdateTaskStatusDone {
		t.Fatalf("expected done task, got %s", got.Status)
	}
	if got.ResultID != "review_task_1" || got.AttemptCount != 1 {
		t.Fatalf("expected one accepted algorithm task, got result_id=%q attempts=%d", got.ResultID, got.AttemptCount)
	}
	assertRequestJSONHasNoSensitiveFields(t, got.RequestJSON)
	if status := skillReviewResultStatus(t, db, "pending-1"); status != "pending" {
		t.Fatalf("expected pending-1 unchanged, got %s", status)
	}
	if status := skillReviewResultStatus(t, db, "pending-2"); status != "pending" {
		t.Fatalf("expected pending-2 unchanged, got %s", status)
	}
	if status := skillReviewResultStatus(t, db, "accepted-1"); status != "accepted" {
		t.Fatalf("expected accepted-1 unchanged, got %s", status)
	}
	if status := skillReviewResultStatus(t, db, "other-user"); status != "pending" {
		t.Fatalf("expected other-user unchanged, got %s", status)
	}
}

func TestSkillWorkerPassesManualThresholds(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewResultsTable(t, db)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	insertTask(t, db, orm.ResourceUpdateTask{
		ID:           "manual-skill-task",
		TaskType:     orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill,
		UserID:       "user-1",
		TriggerType:  orm.ResourceUpdateTriggerTypeManual,
		TriggerID:    "skill-review:manual-skill-task",
		Status:       orm.ResourceUpdateTaskStatusPending,
		RequestJSON: marshalJSON(t, skillGenerateRequestJSON{
			RequestID:             "manual-request",
			UserID:                "user-1",
			StartTime:             formatTaskTime(start),
			EndTime:               formatTaskTime(now),
			QuantityThreshold:     1,
			QualifiedSessionCount: 1,
			StartTriggerReason:    "manual",
			WindowFrozen:          true,
		}),
		NextRunAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	})

	var captured algo.SkillReviewRequest
	worker := NewWorker(db, Config{
		MinUserTurns:     3,
		MinToolTurns:     8,
		WorkerBatchSize:  1,
		WorkerLockTTL:    time.Minute,
		MaxAttempts:      1,
		RetryBackoffBase: time.Minute,
		RetryBackoffMax:  time.Minute,
	}, "worker-manual-skill")
	worker.clock = func() time.Time { return now }
	worker.loadLLMConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return nil, nil
	}
	worker.callers.Skill = func(_ context.Context, req algo.SkillReviewRequest) (*algo.SkillReviewResponse, int, error) {
		captured = req
		return &algo.SkillReviewResponse{Code: 0, Data: algo.SkillReviewData{Status: "running", RequestID: req.RequestID, TaskID: "review_manual_task"}}, 200, nil
	}

	result, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("worker run: %v", err)
	}
	if result.Done != 1 {
		t.Fatalf("expected one done task, got %#v", result)
	}
	if captured.RequestID != "review_manual-request" || captured.MinUserTurns != 3 || captured.MinToolTurns != 8 {
		t.Fatalf("unexpected manual skill review request: %#v", captured)
	}
}

func TestSchedulerCreatesOneActiveTaskAndSettlesDoneTask(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	insertConversation(t, db, "conv-u1", "user-1", start.Add(10*time.Minute))
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
		Stages:                []Stage{{Window: 4 * time.Hour, Interval: time.Hour, QuantityThreshold: 1, Successes: 1}, {Window: 8 * time.Hour, Interval: 2 * time.Hour, QuantityThreshold: 2, Successes: 0}},
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
	insertSkillReviewConversation(t, db, "conv-a", "user-1", start.Add(10*time.Minute), 1, 3)
	insertSkillReviewConversation(t, db, "conv-b", "user-1", start.Add(20*time.Minute), 1, 3)
	insertSkillReviewConversation(t, db, "conv-c", "user-1", start.Add(30*time.Minute), 1, 2)
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
		MinUserTurns:          3,
		MinToolTurns:          8,
		QuantityCheckInterval: time.Second,
		MinInterval:           time.Second,
		MaxWindow:             24 * time.Hour,
		Stages:                []Stage{{Window: 4 * time.Hour, Interval: time.Hour, QuantityThreshold: 1, Successes: 0}},
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

func TestSchedulerDefersSkillReviewWhileMaintenanceTaskIsRunning(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	insertConversation(t, db, "conv-deferred", "user-1", start.Add(10*time.Minute))
	insertHistory(t, db, "h-deferred-1", "conv-deferred", start.Add(10*time.Minute), "turn one", "", 1)
	insertHistory(t, db, "h-deferred-2", "conv-deferred", start.Add(20*time.Minute), "turn two", "", 1)
	insertSchedulerState(t, db, orm.SkillReviewSchedulerState{
		UserID:        "user-1",
		LastWindowEnd: start,
		NextRunAt:     now,
		CreatedAt:     now,
		UpdatedAt:     now,
	})
	insertSkillReviewStats(t, db, map[string]any{
		"id": "org-running", "requestid": "org-running", "userid": "user-1", "status": "organize_apply",
		"started_at": now.Format(time.RFC3339Nano), "duration_ms": 0, "summary": "{}",
	})

	scheduler := NewScheduler(db, Config{
		SchedulerBatchSize:    1,
		SchedulerLockTTL:      time.Minute,
		SchedulerRetryDelay:   time.Minute,
		MinUserTurns:          2,
		MinToolTurns:          2,
		QuantityCheckInterval: time.Second,
		MinInterval:           time.Second,
		MaxWindow:             24 * time.Hour,
		Stages:                []Stage{{Window: 4 * time.Hour, Interval: time.Hour, QuantityThreshold: 1}},
	}, "scheduler-deferred")
	scheduler.clock = func() time.Time { return now }

	result, err := scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("scheduler run: %v", err)
	}
	if result.CreatedTasks != 0 {
		t.Fatalf("blocked scheduler created task: %#v", result)
	}
	var taskCount int64
	if err := db.Model(&orm.ResourceUpdateTask{}).Count(&taskCount).Error; err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("blocked scheduler task count = %d, want 0", taskCount)
	}
	var stateRow orm.SkillReviewSchedulerState
	if err := db.Take(&stateRow, "user_id = ?", "user-1").Error; err != nil {
		t.Fatalf("load scheduler state: %v", err)
	}
	if stateRow.ActiveTaskID != "" || stateRow.LastErrorCode != taskguard.ReasonMaintenanceTaskRunning ||
		stateRow.LockedUntil == nil || !stateRow.LockedUntil.Equal(now.Add(time.Minute)) {
		t.Fatalf("deferred scheduler state = %#v", stateRow)
	}

	if err := db.Table("skill_review_stats").Where("id = ?", "org-running").Update("status", "completed").Error; err != nil {
		t.Fatalf("complete maintenance task: %v", err)
	}
	now = now.Add(time.Minute + time.Second)
	result, err = scheduler.RunOnce(ctx)
	if err != nil {
		t.Fatalf("scheduler resumed run: %v", err)
	}
	if result.CreatedTasks != 1 {
		t.Fatalf("scheduler did not resume after blocking task: %#v", result)
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
	var createdSkill orm.SkillV2Skill
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
	var updated orm.SkillV2Skill
	if err := db.Take(&updated, "id = ?", "skill-apply").Error; err != nil {
		t.Fatalf("read skill: %v", err)
	}
	updatedContent := readSkillV2HeadContent(t, db, updated.ID)
	if updatedContent != newContent || updated.Version != 3 {
		t.Fatalf("skill not applied correctly: version=%d content=%q", updated.Version, updatedContent)
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
	var updated orm.SkillV2Skill
	if err := db.Take(&updated, "id = ?", "skill-skip").Error; err != nil {
		t.Fatalf("read skill: %v", err)
	}
	updatedContent := readSkillV2HeadContent(t, db, updated.ID)
	if updatedContent != oldContent || updated.Version != 1 {
		t.Fatalf("skill should remain unchanged, got content=%q version=%d", updatedContent, updated.Version)
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
	var row orm.SkillV2Skill
	if err := db.Take(&row, "owner_user_id = ? AND skill_name = ?", "user-1", "new-skill").Error; err != nil {
		t.Fatalf("read created skill: %v", err)
	}
	rowContent := readSkillV2HeadContent(t, db, row.ID)
	if rowContent != strings.TrimSpace(rawNewContent) || row.Category != "system" || row.AutoEvo {
		t.Fatalf("created skill mismatch: category=%q auto_evo=%v content=%q", row.Category, row.AutoEvo, rowContent)
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

func TestSkillReviewResultDetailReturnsRunStats(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	createSkillReviewStatsTable(t, db)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	insertSkillReviewStats(t, db, map[string]any{
		"id":          "stats-detail",
		"requestid":   "request-detail",
		"userid":      "user-1",
		"status":      "completed",
		"started_at":  now.Format(time.RFC3339),
		"duration_ms": 1000,
		"summary": map[string]any{
			"skill_count":    5,
			"created_count":  1,
			"updated_count":  2,
			"skipped_count":  1,
			"failed_count":   1,
			"accepted_count": 3,
		},
	})
	insertSkillReviewStats(t, db, map[string]any{
		"id":          "stats-other-user",
		"requestid":   "request-detail",
		"userid":      "user-2",
		"status":      "completed",
		"started_at":  now.Format(time.RFC3339),
		"duration_ms": 1000,
		"summary": map[string]any{
			"skill_count": 99,
		},
	})

	req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/core/skill-review-results/request-detail", nil), map[string]string{"review_result_id": "request-detail"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetSkillReviewResult(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Code int                      `json:"code"`
		Data skillReviewStatsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if resp.Data.ID != "stats-detail" ||
		resp.Data.RequestID != "request-detail" ||
		resp.Data.UserID != "user-1" ||
		resp.Data.Status != "completed" ||
		resp.Data.DurationMS != 1000 {
		t.Fatalf("unexpected stats detail response: %#v", resp.Data)
	}
	if resp.Data.SkillCount != 5 ||
		resp.Data.CreatedCount != 1 ||
		resp.Data.UpdatedCount != 2 ||
		resp.Data.SkippedCount != 1 ||
		resp.Data.FailedCount != 1 {
		t.Fatalf("unexpected stats counts: %#v", resp.Data)
	}
	var summary map[string]int
	if err := json.Unmarshal(resp.Data.Summary, &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary["accepted_count"] != 3 {
		t.Fatalf("expected raw summary to be preserved, got %#v", summary)
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
	memoryContent, memoryResource := readPersonalResourceHeadContent(t, db, "memory-1")
	if memoryContent != "new memory" || memoryResource.Version != 2 {
		t.Fatalf("memory not updated: version=%d content=%q", memoryResource.Version, memoryContent)
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
	updatedContent, _ := readPersonalResourceHeadContent(t, db, "preference-1")
	updated, err := preferencefile.ParseFileContent(updatedContent)
	if err != nil {
		t.Fatalf("parse updated preference: %v", err)
	}
	if updated.Content != "新正文" || updated.AgentPersona != "新角色" || updated.PreferredName != "用户称谓" || updated.ResponseStyle != "回复风格" {
		t.Fatalf("expected frontmatter in preference file, got %#v", updated)
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
		&orm.PersonalResource{},
		&orm.PersonalResourceBlob{},
		&orm.PersonalResourceRevision{},
		&orm.PersonalResourceDraft{},
		&orm.PersonalResourceReviewSession{},
		&orm.PersonalResourceReviewActionBatch{},
		&orm.PersonalResourceReviewActionItem{},
		&orm.SkillV2Skill{},
		&orm.SkillV2Blob{},
		&orm.SkillV2Revision{},
		&orm.SkillV2RevisionEntry{},
		&orm.SkillV2Draft{},
		&orm.SkillV2DraftEntry{},
		&orm.SkillDraftReviewSession{},
		&orm.SkillDraftReviewActionBatch{},
		&orm.SkillDraftReviewActionItem{},
		&orm.SkillSearchIndex{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_active_skill_maintenance_admission
		ON resource_update_tasks(user_id)
		WHERE resource_type = 'skill'
		  AND task_type IN ('generate_review', 'organize_skill')
		  AND status IN ('pending', 'running')`).Error; err != nil {
		t.Fatalf("create active skill maintenance admission index: %v", err)
	}
	createSkillReviewStatsTable(t, db.DB)
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
	insertHistoryWithResult(t, db, id, convID, createTime, rawContent, content, toolCallResultTags(id, toolCallTurns), toolCallTurns)
}

func insertHistoryWithResult(t *testing.T, db *gorm.DB, id, convID string, createTime time.Time, rawContent, content, result string, toolCallTurns int) {
	t.Helper()
	storedContent := content
	if storedContent == "" {
		storedContent = rawContent
	}
	err := db.Create(&orm.ChatHistory{
		ID:             id,
		Seq:            int(createTime.Unix()),
		ConversationID: convID,
		RawContent:     rawContent,
		Content:        storedContent,
		Result:         result,
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

func toolCallResultTags(prefix string, count int) string {
	if count <= 0 {
		return ""
	}
	var builder strings.Builder
	for index := 0; index < count; index++ {
		fmt.Fprintf(
			&builder,
			`<tool_call>{"id":"%s-call-%d","name":"calculator","arguments":{}}</tool_call>`,
			prefix,
			index+1,
		)
	}
	return builder.String()
}

func insertSkillReviewConversation(t *testing.T, db *gorm.DB, convID, userID string, start time.Time, userTurns, toolTurns int) {
	t.Helper()
	insertConversation(t, db, convID, userID, start)
	for i := 0; i < userTurns; i++ {
		turnToolCalls := 0
		if i == 0 {
			turnToolCalls = toolTurns
		}
		insertHistory(
			t,
			db,
			fmt.Sprintf("%s-h-%d", convID, i),
			convID,
			start.Add(time.Duration(i)*time.Minute),
			fmt.Sprintf("turn %d", i+1),
			"",
			turnToolCalls,
		)
	}
	if userTurns == 0 && toolTurns > 0 {
		insertHistory(t, db, convID+"-tool-only", convID, start, "", "", toolTurns)
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

func ptrTime(v time.Time) *time.Time {
	return &v
}

func firstNonEmptyTest(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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

func createSkillReviewStatsTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`
	CREATE TABLE IF NOT EXISTS skill_review_stats (
		id varchar(128) PRIMARY KEY,
		requestid varchar(128) NOT NULL,
		userid varchar(255) NOT NULL,
		status varchar(32) NOT NULL,
		started_at text NOT NULL,
		duration_ms integer NOT NULL DEFAULT 0,
		summary json NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create skill_review_stats: %v", err)
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

func insertSkillReviewStats(t *testing.T, db *gorm.DB, row map[string]any) {
	t.Helper()
	if summary, ok := row["summary"]; ok {
		switch summary.(type) {
		case string, []byte:
		default:
			row["summary"] = string(marshalJSON(t, summary))
		}
	}
	if err := db.Table("skill_review_stats").Create(row).Error; err != nil {
		t.Fatalf("insert skill review stats: %v", err)
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

func readSkillV2HeadContent(t *testing.T, db *gorm.DB, skillID string) string {
	t.Helper()
	var skill orm.SkillV2Skill
	if err := db.Take(&skill, "id = ?", skillID).Error; err != nil {
		t.Fatalf("read skill %s: %v", skillID, err)
	}
	if skill.HeadRevisionID == nil {
		t.Fatalf("skill %s has no head revision", skillID)
	}
	var entry orm.SkillV2RevisionEntry
	if err := db.Take(&entry, "revision_id = ? AND path = ?", *skill.HeadRevisionID, "SKILL.md").Error; err != nil {
		t.Fatalf("read skill head entry %s: %v", skillID, err)
	}
	if entry.BlobHash == nil {
		t.Fatalf("skill %s SKILL.md has no blob", skillID)
	}
	var blob orm.SkillV2Blob
	if err := db.Take(&blob, "hash = ?", *entry.BlobHash).Error; err != nil {
		t.Fatalf("read skill blob %s: %v", skillID, err)
	}
	return string(blob.Content)
}

func readPersonalResourceHeadContent(t *testing.T, db *gorm.DB, resourceID string) (string, orm.PersonalResource) {
	t.Helper()
	var resource orm.PersonalResource
	if err := db.Take(&resource, "id = ?", resourceID).Error; err != nil {
		t.Fatalf("read personal resource %s: %v", resourceID, err)
	}
	if resource.HeadRevisionID == nil {
		t.Fatalf("personal resource %s has no head revision", resourceID)
	}
	var revision orm.PersonalResourceRevision
	if err := db.Take(&revision, "id = ? AND resource_id = ?", *resource.HeadRevisionID, resource.ID).Error; err != nil {
		t.Fatalf("read personal resource revision %s: %v", resourceID, err)
	}
	var blob orm.PersonalResourceBlob
	if err := db.Take(&blob, "hash = ?", revision.BlobHash).Error; err != nil {
		t.Fatalf("read personal resource blob %s: %v", resourceID, err)
	}
	return string(blob.Content), resource
}

func insertSkillResource(t *testing.T, db *gorm.DB, row orm.SkillResource) {
	t.Helper()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	if row.Category == "" {
		row.Category = "system"
	}
	if row.ContentHash == "" {
		row.ContentHash = evolution.HashContent(row.Content)
	}
	if row.Version <= 0 {
		row.Version = 1
	}
	hash := row.ContentHash
	revisionID := row.ID + "-rev"
	head := revisionID
	blobHash := hash
	if err := db.Create(&orm.SkillV2Blob{
		Hash:           hash,
		Size:           int64(len([]byte(row.Content))),
		Mime:           "text/markdown; charset=utf-8",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        []byte(row.Content),
		CreatedAt:      row.CreatedAt,
	}).Error; err != nil {
		t.Fatalf("insert skill blob %s: %v", row.ID, err)
	}
	if err := db.Create(&orm.SkillV2Skill{
		ID:                 row.ID,
		OwnerUserID:        row.OwnerUserID,
		OwnerUserName:      row.OwnerUserName,
		CreateUserID:       firstNonEmptyTest(row.CreateUserID, row.OwnerUserID),
		CreateUserName:     row.CreateUserName,
		Category:           row.Category,
		SkillName:          row.SkillName,
		Description:        row.Description,
		RelativeRoot:       row.Category + "/" + row.SkillName,
		SkillMDPath:        "SKILL.md",
		HeadRevisionID:     &head,
		Version:            row.Version,
		AutoEvo:            row.AutoEvo,
		AutoEvoGeneration:  row.AutoEvoGeneration,
		AutoEvoApplyStatus: "idle",
		IsEnabled:          row.IsEnabled,
		UpdateStatus:       evolution.UpdateStatusUpToDate,
		Ext:                []byte(row.Ext),
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}).Error; err != nil {
		t.Fatalf("insert skill v2 %s: %v", row.ID, err)
	}
	if err := db.Create(&orm.SkillV2Revision{
		ID:         revisionID,
		SkillID:    row.ID,
		RevisionNo: row.Version,
		TreeHash:   hash,
		Message:    "seed",
		CreatedAt:  row.CreatedAt,
	}).Error; err != nil {
		t.Fatalf("insert skill revision %s: %v", row.ID, err)
	}
	if err := db.Create(&orm.SkillV2RevisionEntry{
		RevisionID: revisionID,
		Path:       "SKILL.md",
		EntryType:  "file",
		BlobHash:   &blobHash,
		Size:       int64(len([]byte(row.Content))),
		Mime:       "text/markdown; charset=utf-8",
		FileType:   "markdown",
		Binary:     false,
		Mode:       0o644,
	}).Error; err != nil {
		t.Fatalf("insert skill revision entry %s: %v", row.ID, err)
	}
	if err := db.Create(&orm.SkillV2Draft{
		SkillID:        row.ID,
		BaseRevisionID: &head,
		Version:        1,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}).Error; err != nil {
		t.Fatalf("insert skill draft %s: %v", row.ID, err)
	}
}

func insertMemoryResource(t *testing.T, db *gorm.DB, row orm.SystemMemory) {
	t.Helper()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	insertPersonalResource(t, db, row.ID, row.UserID, orm.ResourceUpdateResourceTypeMemory, row.Content, row.Version, row.AutoEvo, row.AutoEvoGeneration, row.CreatedAt, row.UpdatedAt)
}

func insertPreferenceResource(t *testing.T, db *gorm.DB, row orm.SystemUserPreference) {
	t.Helper()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	insertPersonalResource(t, db, row.ID, row.UserID, orm.ResourceUpdateResourceTypeUserPreference, evolution.FormatSystemUserPreferenceForChat(row), row.Version, row.AutoEvo, row.AutoEvoGeneration, row.CreatedAt, row.UpdatedAt)
}

func insertPersonalResource(t *testing.T, db *gorm.DB, id, userID, resourceType, content string, version int64, autoEvo bool, autoEvoGeneration int64, createdAt, updatedAt time.Time) {
	t.Helper()
	if version <= 0 {
		version = 1
	}
	path := "memory/memory.md"
	if resourceType == orm.ResourceUpdateResourceTypeUserPreference {
		path = "memory/user.md"
	}
	hash := evolution.HashContent(content)
	revisionID := id + "-rev"
	head := revisionID
	if err := db.Create(&orm.PersonalResourceBlob{
		Hash:           hash,
		Size:           int64(len([]byte(content))),
		Mime:           "text/markdown; charset=utf-8",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        []byte(content),
		CreatedAt:      createdAt,
	}).Error; err != nil {
		t.Fatalf("insert personal resource blob %s: %v", id, err)
	}
	if err := db.Create(&orm.PersonalResource{
		ID:                 id,
		UserID:             userID,
		ResourceType:       resourceType,
		HeadRevisionID:     &head,
		Version:            version,
		AutoEvo:            autoEvo,
		AutoEvoGeneration:  autoEvoGeneration,
		AutoEvoApplyStatus: evolution.AutoEvoApplyStatusIdle,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
	}).Error; err != nil {
		t.Fatalf("insert personal resource %s: %v", id, err)
	}
	if err := db.Model(&orm.PersonalResource{}).Where("id = ?", id).Updates(map[string]any{
		"auto_evo":            autoEvo,
		"auto_evo_generation": autoEvoGeneration,
	}).Error; err != nil {
		t.Fatalf("set personal resource auto_evo %s: %v", id, err)
	}
	if err := db.Create(&orm.PersonalResourceRevision{
		ID:          revisionID,
		ResourceID:  id,
		RevisionNo:  version,
		Path:        path,
		BlobHash:    hash,
		ContentHash: hash,
		Size:        int64(len([]byte(content))),
		Mime:        "text/markdown; charset=utf-8",
		FileType:    "markdown",
		Binary:      false,
		Message:     "seed",
		CreatedAt:   createdAt,
	}).Error; err != nil {
		t.Fatalf("insert personal resource revision %s: %v", id, err)
	}
	if err := db.Create(&orm.PersonalResourceDraft{
		ResourceID:     id,
		BaseRevisionID: &head,
		Path:           path,
		BlobHash:       hash,
		ContentHash:    hash,
		Size:           int64(len([]byte(content))),
		Mime:           "text/markdown; charset=utf-8",
		FileType:       "markdown",
		Binary:         false,
		Version:        1,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}).Error; err != nil {
		t.Fatalf("insert personal resource draft %s: %v", id, err)
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
