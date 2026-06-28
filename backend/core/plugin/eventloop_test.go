package plugin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/subagent"
)

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

// makeSubAgentTask inserts a sub_agent_task row directly, so EventLoop tests
// can work without going through HandlePluginStepCreated.
func makeSubAgentTask(t *testing.T, db interface {
	CreateTask(in subagent.CreateTaskInput) error
}, taskID, convID, sessionID, stepID string) {
	t.Helper()
}

// seedSession creates a session + step + sub_agent_task record for a given step.
// Returns the task ID used.
func seedSessionAndTask(t *testing.T, ctx context.Context, gdb interface {
	CreateSession(context.Context, CreateSessionInput) error
}, sessionID, convID, pluginID, stepID, taskID string) {
	t.Helper()
}

// ──────────────────────────────────────────────
// Artifact injection — moved to Python runner
// ──────────────────────────────────────────────

// injectArtifacts was removed from the Go layer (eventloop.go).
// Artifact placeholder replacement is now performed by the Python runner via
// _enrich_objective_with_artifacts() in algorithm/lazymind/chat/engine/subagent/runner.py.
// The corresponding tests live in algorithm/tests/chat/plugins/test_manager.py.

// ──────────────────────────────────────────────
// OnSubAgentDone — status routing
// ──────────────────────────────────────────────

func TestOnSubAgentDone_SucceededManualMode(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-1", "analyze_subject", "task-1", 1); err != nil {
		t.Fatalf("step: %v", err)
	}

	// plugin_mode=dynamic in pctx → step_waiting with reason=dynamic_pause
	pctx := &PluginChatContext{
		SessionID:  "ps-1",
		PluginID:   "image-plugin",
		StepID:     "analyze_subject",
		ConvID:     "conv-1",
		UserID:     "user-1",
		PluginMode: "dynamic",
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

func TestOnSubAgentDone_Interrupted_SetsWaiting(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-2", ConversationID: "conv-2", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-2", "generate_image", "task-2", 1); err != nil {
		t.Fatalf("step: %v", err)
	}

	pctx := &PluginChatContext{
		SessionID: "ps-2", PluginID: "image-plugin", StepID: "generate_image",
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

func TestOnSubAgentDone_Failed_SetsSessionWaiting(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-3", ConversationID: "conv-3", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-3", "optimize_prompt", "task-3", 1); err != nil {
		t.Fatalf("step: %v", err)
	}

	pctx := &PluginChatContext{
		SessionID: "ps-3", PluginID: "image-plugin", StepID: "optimize_prompt",
		ConvID: "conv-3",
	}

	var gotEvents []string
	onSSE := func(et string, _ map[string]any) { gotEvents = append(gotEvents, et) }

	OnSubAgentDone(ctx, db.DB, nil, "task-3", subagent.StatusFailed, "step error", onSSE, pctx)

	// Failed path: first plugin_error (frontend can show error detail on subtask card),
	// then step_waiting (session is demoted to waiting, not failed).
	if len(gotEvents) < 2 || gotEvents[0] != "plugin_error" || gotEvents[len(gotEvents)-1] != "step_waiting" {
		t.Fatalf("expected [plugin_error ... step_waiting], got %v", gotEvents)
	}
	// Session must be waiting so the user can retry.
	s, _ := GetSession(ctx, db.DB, "ps-3")
	if s.Status != SessionStatusWaiting {
		t.Fatalf("expected session waiting, got %s", s.Status)
	}
}

// ──────────────────────────────────────────────
// callDriverAgent — mock HTTP server
// ──────────────────────────────────────────────

func TestCallDriverAgent_ReturnsMessage(t *testing.T) {
	cases := []struct {
		body       string
		wantMsgHas string
	}{
		{
			body:       `{"message":"optimized_prompt saved with 65 words."}`,
			wantMsgHas: "optimized_prompt",
		},
		{
			body:       `{"message":"enhanced_image_url saved. The pipeline is complete."}`,
			wantMsgHas: "complete",
		},
		{
			body:       `{"message":"No artifact found; prompt generation may have failed."}`,
			wantMsgHas: "artifact",
		},
	}

	for i, tc := range cases {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

			msg, fallback := callDriverAgent("image-plugin", "optimize_prompt", "step output", "ps-1", nil, nil, "")
			if fallback {
				t.Fatalf("unexpected fallback")
			}
			if !strings.Contains(msg, tc.wantMsgHas) {
				t.Fatalf("expected message to contain %q, got %q", tc.wantMsgHas, msg)
			}
		})
	}
}

func TestCallDriverAgent_DefaultsToFallbackOnError(t *testing.T) {
	// Point to a non-existent server so the HTTP call fails.
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://127.0.0.1:19999")

	msg, fallback := callDriverAgent("image-plugin", "generate_image", "result", "ps-1", nil, nil, "")
	if !fallback {
		t.Fatal("expected fallback=true on connection error")
	}
	if !strings.Contains(msg, "generate_image") {
		t.Fatalf("fallback message should contain step ID, got %q", msg)
	}
}

func TestCallDriverAgent_DefaultsToFallbackOnEmptyMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"message":""}`)
	}))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

	msg, fallback := callDriverAgent("image-plugin", "analyze_subject", "output", "ps-1", nil, nil, "")
	if fallback {
		t.Fatal("empty message should not trigger fallback; got fallback=true")
	}
	if !strings.Contains(msg, "analyze_subject") {
		t.Fatalf("fallback message should contain step ID, got %q", msg)
	}
}

func TestCheckAndFallbackIfStuck_SkipsWhenSubAgentRunning(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-stuck-1", ConversationID: "conv-stuck-1", PluginID: "image-plugin",
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

	checkAndFallbackIfStuck(ctx, db.DB, nil, func(string, map[string]any) {}, &PluginChatContext{
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
		SessionID: "ps-stuck-2", ConversationID: "conv-stuck-2", PluginID: "image-plugin",
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
	}, &PluginChatContext{
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
// resolveSlotBinding — mock Python API
// ──────────────────────────────────────────────

func TestResolveSlotBinding_FoundBinding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pluginID := r.URL.Query().Get("plugin_id")
		artifactKey := r.URL.Query().Get("artifact_key")
		if pluginID != "image-plugin" || artifactKey != "enhanced_image_url" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"slot_id":"enhanced_image_output","cardinality":"list"}`)
	}))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

	slotID, cardinality := resolveSlotBinding("image-plugin", "enhanced_image_url")
	if slotID != "enhanced_image_output" {
		t.Fatalf("expected enhanced_image_output, got %q", slotID)
	}
	if cardinality != "list" {
		t.Fatalf("expected list cardinality, got %q", cardinality)
	}
}

func TestResolveSlotBinding_NoBinding_ReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"slot_id":"","cardinality":"single"}`)
	}))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

	slotID, _ := resolveSlotBinding("image-plugin", "some_internal_artifact")
	if slotID != "" {
		t.Fatalf("expected empty slotID, got %q", slotID)
	}
}

// ──────────────────────────────────────────────
// StopActivePluginSession — sends task-cancel to Python
// ──────────────────────────────────────────────

func TestStopActivePluginSession_SendsTaskCancel(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "stop-sess-1", ConversationID: "stop-conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "stop-sess-1", "analyze_subject", "stop-task-1", 1); err != nil {
		t.Fatalf("CreateSessionStep: %v", err)
	}
	// Mark the step as running so StopActivePluginSession picks it up.
	if err := UpdateStepStatus(ctx, db.DB, "stop-task-1", StepStatusRunning); err != nil {
		t.Fatalf("UpdateStepStatus: %v", err)
	}

	taskCancelCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "task-cancel") {
			taskCancelCalls++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

	StopActivePluginSession(ctx, db.DB, nil, "stop-conv-1")

	// notifyTaskCancel runs in a goroutine; give it a moment to complete.
	time.Sleep(100 * time.Millisecond)

	if taskCancelCalls == 0 {
		t.Fatal("expected at least one /api/plugin/task-cancel call")
	}
}

// ──────────────────────────────────────────────
// OnSubAgentDone — parallel step completion
// ──────────────────────────────────────────────

func TestOnSubAgentDone_ParallelStepsAllDone(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "par-sess-1", ConversationID: "par-conv-1", PluginID: "image-plugin",
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
		SessionID: "par-sess-2", ConversationID: "par-conv-2", PluginID: "image-plugin",
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
