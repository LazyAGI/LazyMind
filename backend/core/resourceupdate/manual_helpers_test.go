package resourceupdate

import (
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

// TestManualSkillReviewWindowFromState computes window from state and config.
func TestManualSkillReviewWindowFromState(t *testing.T) {
	cfg := DefaultConfig()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	// State with LastWindowEnd set
	state := orm.SkillReviewSchedulerState{
		LastWindowEnd: now.Add(-2 * time.Hour),
	}
	start, end := manualSkillReviewWindowFromState(state, cfg, now)
	if !end.Equal(now) {
		t.Fatalf("end = %v, want %v", end, now)
	}
	if !start.Equal(now.Add(-2 * time.Hour)) {
		t.Fatalf("start = %v, want %v", start, now.Add(-2*time.Hour))
	}

	// Zero state: start clamped to MaxWindow ago
	zeroState := orm.SkillReviewSchedulerState{}
	start2, end2 := manualSkillReviewWindowFromState(zeroState, cfg, now)
	if start2.Before(now.Add(-cfg.MaxWindow)) {
		t.Fatalf("start too early: %v", start2)
	}
	if !end2.Equal(now) {
		t.Fatalf("end = %v, want %v", end2, now)
	}
}

// TestManualSkillReviewSummaryFromStats builds a summary response from stats.
func TestManualSkillReviewSummaryFromStats(t *testing.T) {
	cfg := DefaultConfig()
	start := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	stats := HistoryStats{
		UserTurnCount:         10,
		ToolCallCount:         20,
		QualifiedSessionCount: 3,
	}
	task := orm.ResourceUpdateTask{ID: "task-1", TriggerID: "trigger-1"}

	summary := manualSkillReviewSummaryFromStats(stats, cfg, start, end, task, "req-1")
	if summary.QualifiedSessionCount != 3 {
		t.Fatalf("qualified = %d, want 3", summary.QualifiedSessionCount)
	}
	if summary.QuantityThreshold != manualSkillReviewQuantityThreshold {
		t.Fatalf("threshold = %d, want %d", summary.QuantityThreshold, manualSkillReviewQuantityThreshold)
	}
	if !summary.WindowStart.Equal(start) {
		t.Fatalf("start = %v, want %v", summary.WindowStart, start)
	}
	if summary.RunningRequestID != "req-1" {
		t.Fatalf("requestID = %q, want req-1", summary.RunningRequestID)
	}
	if summary.RunningTask == nil || summary.RunningTask.ID != "task-1" {
		t.Fatal("running task should be set")
	}

	// Empty task -> no running task in response
	summary2 := manualSkillReviewSummaryFromStats(stats, cfg, start, end, orm.ResourceUpdateTask{}, "")
	if summary2.RunningTask != nil {
		t.Fatal("empty task should yield nil running task")
	}
}

// TestSkillTaskRequestID extracts request ID from task request JSON.
func TestSkillTaskRequestID(t *testing.T) {
	// Empty task
	if got := skillTaskRequestID(orm.ResourceUpdateTask{}); got != "" {
		t.Fatalf("empty got %q, want empty", got)
	}

	// Valid request
	body, _ := json.Marshal(skillGenerateRequestJSON{RequestID: "req-123"})
	task := orm.ResourceUpdateTask{RequestJSON: body}
	if got := skillTaskRequestID(task); got != "req-123" {
		t.Fatalf("got %q, want req-123", got)
	}

	// Invalid JSON
	task2 := orm.ResourceUpdateTask{RequestJSON: json.RawMessage("invalid")}
	if got := skillTaskRequestID(task2); got != "" {
		t.Fatalf("invalid got %q, want empty", got)
	}
}

// TestNewManualSkillGenerateTask creates a frozen window task.
func TestNewManualSkillGenerateTask(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	stats := HistoryStats{
		UserTurnCount:         5,
		ToolCallCount:         10,
		QualifiedSessionCount: 2,
		QualifiedSessionIDs:   []string{"s1", "s2"},
	}
	task, requestID, err := newManualSkillGenerateTask("user-1", stats, now.Add(-2*time.Hour), now, now)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.UserID != "user-1" {
		t.Fatalf("user_id = %q", task.UserID)
	}
	if task.TaskType != orm.ResourceUpdateTaskTypeGenerateReview {
		t.Fatalf("task_type = %q", task.TaskType)
	}
	if task.TriggerType != orm.ResourceUpdateTriggerTypeManual {
		t.Fatalf("trigger_type = %q", task.TriggerType)
	}
	if requestID == "" {
		t.Fatal("requestID should not be empty")
	}

	// Verify frozen window request
	var req skillGenerateRequestJSON
	if err := json.Unmarshal(task.RequestJSON, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if !req.WindowFrozen {
		t.Fatal("manual task should have frozen window")
	}
	if req.QualifiedSessionCount != 2 {
		t.Fatalf("session count = %d, want 2", req.QualifiedSessionCount)
	}
}
