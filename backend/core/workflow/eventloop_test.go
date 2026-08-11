package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/subagent"
)

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

// makeSubAgentTask inserts a sub_agent_task row directly, so EventLoop tests
// can work without going through HandleWorkflowStepCreated.
func makeSubAgentTask(t *testing.T, db interface {
	CreateTask(in subagent.CreateTaskInput) error
}, taskID, convID, sessionID, stepID string) {
	t.Helper()
}

func TestBuildWorkflowArtifactsSummaryExecutesJoinQuery(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSessionStep{ID: "attempt-1", SessionID: "session-1", StepID: "step-1",
		Attempt: 1, TaskID: "task-1", Status: "succeeded", Validity: "effective", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create session step: %v", err)
	}
	if err := db.Create(&orm.SubAgentArtifact{ID: "artifact-1", TaskID: "task-1", Slot: "output",
		ContentType: "text", Value: json.RawMessage(`{"value":"done"}`), Seq: 1, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	summary, err := buildWorkflowArtifactsSummary(t.Context(), db.DB, "session-1", "step-1")
	if err != nil {
		t.Fatalf("build summary: %v", err)
	}
	if !strings.Contains(summary, "output: done") {
		t.Fatalf("unexpected summary: %q", summary)
	}
}

// seedSession creates a session + step + sub_agent_task record for a given step.
// Returns the task ID used.
func seedSessionAndTask(t *testing.T, ctx context.Context, gdb interface {
	CreateSession(context.Context, CreateSessionInput) error
}, sessionID, convID, workflowID, stepID, taskID string) {
	t.Helper()
}

// ──────────────────────────────────────────────
// Artifact injection — moved to Python runner
// ──────────────────────────────────────────────

// injectArtifacts was removed from the Go layer (eventloop.go).
// Artifact placeholder replacement is now performed by the Python runner via
// _enrich_objective_with_artifacts() in algorithm/lazymind/chat/engine/subagent/runner.py.
// The corresponding tests live in algorithm/tests/chat/workflows/test_manager.py.

// ──────────────────────────────────────────────
// OnSubAgentDone — status routing
// ──────────────────────────────────────────────

func TestConversationPreflightMustBeReadyAndIsConsumed(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.AutoMigrate(&orm.Conversation{}); err != nil {
		t.Fatalf("migrate conversation: %v", err)
	}
	extJSON, _ := json.Marshal(map[string]any{
		"keep": "value",
		"workflow_preflight": map[string]any{
			"preflight_id": "pf-ready",
			"status":       "ready",
		},
	})
	if err := db.Create(&orm.Conversation{ID: "conv-preflight", Ext: extJSON}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	if err := validateConversationPreflight(ctx, db.DB, "conv-preflight", "pf-stale"); err == nil {
		t.Fatal("stale preflight id must be rejected")
	}
	if err := validateConversationPreflight(ctx, db.DB, "conv-preflight", "pf-ready"); err != nil {
		t.Fatalf("ready preflight rejected: %v", err)
	}
	if err := consumeConversationPreflight(ctx, db.DB, "conv-preflight", "pf-ready"); err != nil {
		t.Fatalf("consume ready preflight: %v", err)
	}
	ext, preflight := conversationPreflight(ctx, db.DB, "conv-preflight")
	if preflight != nil {
		t.Fatalf("preflight was not consumed: %v", preflight)
	}
	if ext["keep"] != "value" {
		t.Fatalf("unrelated conversation ext was lost: %v", ext)
	}
}

func TestOnSubAgentDone_SucceededManualMode(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-1", "analyze_subject", "task-1", 1); err != nil {
		t.Fatalf("step: %v", err)
	}

	// workflow_mode=dynamic in pctx → step_waiting with reason=dynamic_pause
	pctx := &WorkflowChatContext{
		SessionID:    "ps-1",
		WorkflowID:   "image-workflow",
		StepID:       "analyze_subject",
		ConvID:       "conv-1",
		UserID:       "user-1",
		WorkflowMode: "dynamic",
	}

	var gotEvent string
	var gotPayload map[string]any
	onSSE := func(eventType string, payload map[string]any) {
		gotEvent = eventType
		gotPayload = payload
	}

	OnSubAgentDone(ctx, db.DB, nil, "task-1", subagent.StatusSucceeded, "analysis done", onSSE, pctx)

	if gotEvent != "step_waiting" {
		t.Fatalf("expected step_waiting, got %q", gotEvent)
	}
	if gotPayload["session_id"] != "ps-1" {
		t.Fatalf("unexpected payload: %v", gotPayload)
	}
	if gotPayload["reason"] != "dynamic_pause" {
		t.Fatalf("expected reason=dynamic_pause, got %v", gotPayload["reason"])
	}
	interrupted, _ := gotPayload["interrupted"].(bool)
	if interrupted {
		t.Fatal("succeeded step must not set interrupted=true in step_waiting")
	}
}

func TestOnSubAgentDone_HandoffWaitsAndMergesParallelTerminalStatuses(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.DB.AutoMigrate(&orm.ChatHistory{}); err != nil {
		t.Fatalf("migrate history: %v", err)
	}
	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-history", ConversationID: "conv-history", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-history", "analyze_subject", "task-history", 1); err != nil {
		t.Fatalf("step: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-history", "generate_image", "task-image", 1); err != nil {
		t.Fatalf("parallel step: %v", err)
	}
	now := time.Now()
	for _, taskID := range []string{"task-history", "task-image"} {
		if err := db.DB.Exec(
			"INSERT INTO sub_agent_tasks (id, conversation_id, trigger_history_id, seq_in_conversation, agent_type, title, objective, mode, status, progress_pct, last_heartbeat, workspace_path, input_slots, output_slots, create_user_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			taskID, "conv-history", "history-1", 1, "workflow_step", taskID, "", "manual", "pending", 0, now, "", "[]", "[]", "", now, now,
		).Error; err != nil {
			t.Fatalf("task %s: %v", taskID, err)
		}
	}
	if err := db.DB.Create(&orm.ChatHistory{
		ID: "history-1", ConversationID: "conv-history", Seq: 1,
		Content: "画一张漫画", Result: "<think>准备执行工作流</think>",
	}).Error; err != nil {
		t.Fatalf("history: %v", err)
	}

	handOff := true
	pctx := &WorkflowChatContext{
		SessionID: "ps-history", WorkflowID: "image-workflow", StepID: "analyze_subject",
		ConvID: "conv-history", WorkflowMode: "dynamic", HandOff: &handOff,
		TriggerHistoryID: "history-1",
	}
	OnSubAgentDone(
		ctx, db.DB, nil, "task-history", subagent.StatusSucceeded,
		"Analyzed the requested manga style and saved the subject analysis.",
		func(string, map[string]any) {}, pctx,
	)
	var before orm.ChatHistory
	_ = db.DB.First(&before, "id = ?", "history-1").Error
	if before.Result != "<think>准备执行工作流</think>" {
		t.Fatalf("summary was written before parallel batch finished: %s", before.Result)
	}

	pctx.StepID = "generate_image"
	OnSubAgentDone(
		ctx, db.DB, nil, "task-image", subagent.StatusInterrupted, "user stopped",
		func(string, map[string]any) {}, pctx,
	)

	var history orm.ChatHistory
	if err := db.DB.First(&history, "id = ?", "history-1").Error; err != nil {
		t.Fatalf("reload history: %v", err)
	}
	for _, want := range []string{
		"<think>准备执行工作流</think>",
		"已完成 analyze_subject",
		"用户中断了 generate_image",
	} {
		if !strings.Contains(history.Result, want) {
			t.Fatalf("history result missing %q: %s", want, history.Result)
		}
	}
}

func TestHandoffStepName_PrefersLabelThenID(t *testing.T) {
	labels := map[string]string{"analyze_subject": "主体分析"}
	if got := handoffStepName("analyze_subject", labels); got != "主体分析（analyze_subject）" {
		t.Fatalf("labeled step: %q", got)
	}
	if got := handoffStepName("generate_image", labels); got != "generate_image" {
		t.Fatalf("fallback step: %q", got)
	}
}

func TestEnforceWorkflowConversationSettings_EnablesApprovalMode(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.DB.AutoMigrate(&orm.Conversation{}); err != nil {
		t.Fatalf("migrate conversation: %v", err)
	}
	disabled := false
	auto := "auto"
	conversation := orm.Conversation{
		ID: "conv-settings", DisplayName: "Workflow chat",
		EnableWorkflow: &disabled, WorkflowMode: &auto,
	}
	if err := db.DB.Create(&conversation).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := enforceWorkflowConversationSettings(ctx, db.DB, conversation.ID); err != nil {
		t.Fatalf("enforce settings: %v", err)
	}
	var got orm.Conversation
	if err := db.DB.First(&got, "id = ?", conversation.ID).Error; err != nil {
		t.Fatalf("reload conversation: %v", err)
	}
	if got.EnableWorkflow == nil || !*got.EnableWorkflow {
		t.Fatalf("workflow was not enabled: %#v", got.EnableWorkflow)
	}
	if got.WorkflowMode == nil || *got.WorkflowMode != "dynamic" {
		t.Fatalf("plugin mode: %#v", got.WorkflowMode)
	}
}

func TestAppendHandoffHistorySummary_SkipsInlineExecution(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.DB.AutoMigrate(&orm.ChatHistory{}); err != nil {
		t.Fatalf("migrate history: %v", err)
	}
	if err := db.DB.Create(&orm.ChatHistory{
		ID: "history-inline", ConversationID: "conv-inline-history", Seq: 1,
		Content: "continue", Result: "original result",
	}).Error; err != nil {
		t.Fatalf("history: %v", err)
	}
	handOff := false
	err := appendHandoffHistorySummary(ctx, db.DB, &WorkflowChatContext{
		ConvID: "conv-inline-history", StepID: "step-a", HandOff: &handOff,
		TriggerHistoryID: "history-inline",
	}, false)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	var history orm.ChatHistory
	_ = db.DB.First(&history, "id = ?", "history-inline").Error
	if history.Result != "original result" {
		t.Fatalf("inline execution history changed: %q", history.Result)
	}
}

func TestOnSubAgentDone_ExplicitNoHandOffWaitsForChatAgent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-inline", ConversationID: "conv-inline", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-inline", "analyze_subject", "task-inline", 1); err != nil {
		t.Fatalf("step: %v", err)
	}

	handOff := false
	pctx := &WorkflowChatContext{
		SessionID: "ps-inline", WorkflowID: "image-workflow", StepID: "analyze_subject",
		ConvID: "conv-inline", WorkflowMode: "auto", HandOff: &handOff,
	}
	var gotEvent string
	var gotReason any
	OnSubAgentDone(
		ctx, db.DB, nil, "task-inline", subagent.StatusSucceeded, "analysis done",
		func(eventType string, payload map[string]any) {
			gotEvent = eventType
			gotReason = payload["reason"]
		},
		pctx,
	)

	if gotEvent != "step_waiting" || gotReason != "inline_complete" {
		t.Fatalf("expected inline_complete step_waiting, got event=%q reason=%v", gotEvent, gotReason)
	}
}

func TestOnSubAgentDone_Interrupted_SetsWaiting(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-2", ConversationID: "conv-2", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-2", "generate_image", "task-2", 1); err != nil {
		t.Fatalf("step: %v", err)
	}

	pctx := &WorkflowChatContext{
		SessionID: "ps-2", WorkflowID: "image-workflow", StepID: "generate_image",
		ConvID: "conv-2", UserID: "user-1",
	}

	var gotEvent string
	onSSE := func(et string, _ map[string]any) {
		gotEvent = et
	}

	OnSubAgentDone(ctx, db.DB, nil, "task-2", subagent.StatusInterrupted, "heartbeat timeout", onSSE, pctx)

	// Interrupted steps now follow the unified path: session → waiting, event = step_waiting.
	// The interrupted=true payload field is no longer emitted; the subtask card carries that detail.
	if gotEvent != "step_waiting" {
		t.Fatalf("expected step_waiting for interrupted, got %q", gotEvent)
	}

	// Session status must be 'waiting'.
	s, _ := GetSession(ctx, db.DB, "ps-2")
	if s.Status != SessionStatusWaiting {
		t.Fatalf("expected session waiting, got %s", s.Status)
	}
}

func TestOnSubAgentDone_Failed_SetsSessionFailed(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-3", ConversationID: "conv-3", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-3", "optimize_prompt", "task-3", 1); err != nil {
		t.Fatalf("step: %v", err)
	}

	pctx := &WorkflowChatContext{
		SessionID: "ps-3", WorkflowID: "image-workflow", StepID: "optimize_prompt",
		ConvID: "conv-3",
	}

	var gotEvents []string
	statusAtEvent := ""
	onSSE := func(et string, _ map[string]any) {
		gotEvents = append(gotEvents, et)
		if session, err := GetSession(ctx, db.DB, "ps-3"); err == nil {
			statusAtEvent = session.Status
		}
	}

	OnSubAgentDone(ctx, db.DB, nil, "task-3", subagent.StatusFailed, "step error", onSSE, pctx)

	if len(gotEvents) != 1 || gotEvents[0] != "workflow_error" {
		t.Fatalf("expected only workflow_error, got %v", gotEvents)
	}
	if statusAtEvent != SessionStatusFailed {
		t.Fatalf("workflow_error published before failed state was durable: %s", statusAtEvent)
	}
	// Session failure is distinct from a successful approval checkpoint.
	s, _ := GetSession(ctx, db.DB, "ps-3")
	if s.Status != SessionStatusFailed {
		t.Fatalf("expected session failed, got %s", s.Status)
	}
}

func TestCheckAndFallbackIfStuck_SkipsWhenSubAgentRunning(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-stuck-1", ConversationID: "conv-stuck-1", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := UpdateSessionStatus(ctx, db.DB, "ps-stuck-1", SessionStatusActive); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-stuck-1", "generate_image", "task-stuck-1", 1); err != nil {
		t.Fatalf("CreateSessionStep: %v", err)
	}
	if err := UpdateStepStatus(ctx, db.DB, "task-stuck-1", StepStatusRunning); err != nil {
		t.Fatalf("UpdateStepStatus: %v", err)
	}

	checkAndFallbackIfStuck(ctx, db.DB, nil, func(string, map[string]any) {}, &WorkflowChatContext{
		SessionID: "ps-stuck-1",
		StepID:    "optimize_prompt",
	})

	s, err := GetSession(ctx, db.DB, "ps-stuck-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s.Status != SessionStatusActive {
		t.Fatalf("expected active while subagent running, got %q", s.Status)
	}
}

func TestCheckAndFallbackIfStuck_DemotesWhenIdle(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-stuck-2", ConversationID: "conv-stuck-2", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := UpdateSessionStatus(ctx, db.DB, "ps-stuck-2", SessionStatusActive); err != nil {
		t.Fatalf("UpdateSessionStatus: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-stuck-2", "optimize_prompt", "task-stuck-2", 1); err != nil {
		t.Fatalf("CreateSessionStep: %v", err)
	}
	if err := UpdateStepStatus(ctx, db.DB, "task-stuck-2", StepStatusSucceeded); err != nil {
		t.Fatalf("UpdateStepStatus: %v", err)
	}

	var gotEvent string
	checkAndFallbackIfStuck(ctx, db.DB, nil, func(eventType string, _ map[string]any) {
		gotEvent = eventType
	}, &WorkflowChatContext{
		SessionID: "ps-stuck-2",
		StepID:    "optimize_prompt",
	})

	s, err := GetSession(ctx, db.DB, "ps-stuck-2")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if s.Status != SessionStatusWaiting {
		t.Fatalf("expected waiting when idle, got %q", s.Status)
	}
	if gotEvent != "step_waiting" {
		t.Fatalf("expected step_waiting event, got %q", gotEvent)
	}
}

// ──────────────────────────────────────────────
// StopActiveWorkflowSession — sends task-cancel to Python
// ──────────────────────────────────────────────

func TestStopActiveWorkflowSession_SendsTaskCancel(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "stop-sess-1", ConversationID: "stop-conv-1", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := subagent.CreateTask(ctx, db.DB, subagent.CreateTaskInput{
		TaskID: "stop-task-1", ConversationID: "stop-conv-1", AgentType: "workflow_step",
		Title: "analyze_subject", Objective: "analyze_subject", CreateUserID: "user-1",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "stop-sess-1", "analyze_subject", "stop-task-1", 1); err != nil {
		t.Fatalf("CreateSessionStep: %v", err)
	}
	// Mark the step as running so StopActiveWorkflowSession picks it up.
	if err := UpdateStepStatus(ctx, db.DB, "stop-task-1", StepStatusRunning); err != nil {
		t.Fatalf("UpdateStepStatus: %v", err)
	}

	taskCancelCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workflow/task-cancel" {
			taskCancelCalls++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

	StopActiveWorkflowSession(ctx, db.DB, nil, "stop-conv-1")

	// notifyTaskCancel runs in a goroutine; give it a moment to complete.
	time.Sleep(100 * time.Millisecond)

	if taskCancelCalls == 0 {
		t.Fatal("expected at least one /api/workflow/task-cancel call")
	}
}

func TestStopActiveWorkflowSession_CancelsAllPendingAndRunningAttempts(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "stop-sess-parallel", ConversationID: "stop-conv-parallel", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, item := range []struct {
		stepID string
		taskID string
		status string
	}{
		{stepID: "queued_branch", taskID: "stop-task-pending", status: StepStatusPending},
		{stepID: "active_branch", taskID: "stop-task-running", status: StepStatusRunning},
	} {
		if _, err := subagent.CreateTask(ctx, db.DB, subagent.CreateTaskInput{
			TaskID: item.taskID, ConversationID: "stop-conv-parallel", AgentType: "workflow_step",
			Title: item.stepID, Objective: item.stepID, CreateUserID: "user-1",
		}); err != nil {
			t.Fatalf("CreateTask(%s): %v", item.taskID, err)
		}
		if _, err := CreateSessionStep(ctx, db.DB, "stop-sess-parallel", item.stepID, item.taskID, 1); err != nil {
			t.Fatalf("CreateSessionStep(%s): %v", item.taskID, err)
		}
		if item.status == StepStatusRunning {
			if err := UpdateStepStatus(ctx, db.DB, item.taskID, item.status); err != nil {
				t.Fatalf("UpdateStepStatus(%s): %v", item.taskID, err)
			}
		}
	}

	var mu sync.Mutex
	cancelled := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workflow/task-cancel" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			cancelled[body["task_id"]] = true
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

	StopActiveWorkflowSession(ctx, db.DB, nil, "stop-conv-parallel")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		allCancelled := cancelled["stop-task-pending"] && cancelled["stop-task-running"]
		mu.Unlock()
		if allCancelled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, taskID := range []string{"stop-task-pending", "stop-task-running"} {
		task, err := subagent.GetTask(ctx, db.DB, taskID)
		if err != nil || task == nil {
			t.Fatalf("GetTask(%s): task=%v err=%v", taskID, task, err)
		}
		if task.Status != subagent.StatusInterrupted {
			t.Errorf("task %s status = %q, want interrupted", taskID, task.Status)
		}
		step, err := GetStepByTaskID(ctx, db.DB, taskID)
		if err != nil || step == nil {
			t.Fatalf("GetStepByTaskID(%s): step=%v err=%v", taskID, step, err)
		}
		if step.Status != StepStatusInterrupted {
			t.Errorf("step %s status = %q, want interrupted", taskID, step.Status)
		}
		mu.Lock()
		wasCancelled := cancelled[taskID]
		mu.Unlock()
		if !wasCancelled {
			t.Errorf("task %s did not receive a Python cancel request", taskID)
		}
	}
}

// ──────────────────────────────────────────────
// OnSubAgentDone — parallel step completion
// ──────────────────────────────────────────────

func TestOnSubAgentDone_ParallelStepsAllDone(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "par-sess-1", ConversationID: "par-conv-1", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	// Two parallel steps: complete step-A first, then step-B.
	if _, err := CreateSessionStep(ctx, db.DB, "par-sess-1", "step_a", "par-task-a", 1); err != nil {
		t.Fatalf("step_a: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "par-sess-1", "step_b", "par-task-b", 1); err != nil {
		t.Fatalf("step_b: %v", err)
	}

	// Mark step_a succeeded; step_b is still running — should NOT trigger DriverAgent.
	driverCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		driverCalls++
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"next_step":null}`)
	}))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

	onSSE := func(_ string, _ map[string]any) {}

	OnSubAgentDone(ctx, db.DB, nil, "par-task-a", "succeeded", "", onSSE, nil)
	if driverCalls != 0 {
		t.Fatalf("expected 0 driver calls while step_b still running, got %d", driverCalls)
	}
}

func TestOnSubAgentDone_ParallelStepsPartialDone(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "par-sess-2", ConversationID: "par-conv-2", WorkflowID: "image-workflow",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "par-sess-2", "only_step", "par-task-only", 1); err != nil {
		t.Fatalf("step: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"next_step":null}`)
	}))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

	onSSE := func(_ string, _ map[string]any) {}

	// Only step completes — should not panic.
	OnSubAgentDone(ctx, db.DB, nil, "par-task-only", "succeeded", "", onSSE, nil)
}
