package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"lazymind/core/subagent"
)

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

// makeSubAgentTask inserts a sub_agent_task row directly, so EventLoop tests
// can work without going through HandlePluginStepCreated.
func makeSubAgentTask(t *testing.T, db interface{ CreateTask(in subagent.CreateTaskInput) error }, taskID, convID, sessionID, stepID string) {
	t.Helper()
}

// seedSession creates a session + step + sub_agent_task record for a given step.
// Returns the task ID used.
func seedSessionAndTask(t *testing.T, ctx context.Context, gdb interface{
	CreateSession(context.Context, CreateSessionInput) error
}, sessionID, convID, pluginID, stepID, taskID string) {
	t.Helper()
}

// ──────────────────────────────────────────────
// injectArtifacts
// ──────────────────────────────────────────────

func TestInjectArtifacts_NoPlaceholders(t *testing.T) {
	objective := "Generate a beautiful landscape image."
	db := newTestDB(t)
	ctx := context.Background()

	// No session steps exist, but that's fine — no placeholders to fill.
	result := injectArtifacts(ctx, db.DB, "ps-1", objective)
	if result != objective {
		t.Fatalf("expected unchanged objective, got %q", result)
	}
}

func TestInjectArtifacts_ReplacesTextArtifact(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Create session + step + task.
	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-1", ConversationID: "conv-1", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	_, err := subagent.CreateTask(ctx, db.DB, subagent.CreateTaskInput{
		TaskID: "task-a", ConversationID: "conv-1", AgentType: "plugin_step",
		Title: "image-plugin:analyze_subject", Mode: "manual",
	})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-1", "analyze_subject", "task-a", 1); err != nil {
		t.Fatalf("step: %v", err)
	}
	// Mark succeeded.
	if err := subagent.UpdateFinalStatus(ctx, db.DB, "task-a", subagent.StatusSucceeded, "done"); err != nil {
		t.Fatalf("final status: %v", err)
	}
	// Save an artifact.
	if err := subagent.SaveArtifact(ctx, db.DB, "task-a", "subject_analysis", "text",
		json.RawMessage(`{"text":"A cyberpunk city at night"}`), 1); err != nil {
		t.Fatalf("artifact: %v", err)
	}
	// Update step status so injectArtifacts picks it up.
	if err := UpdateStepStatus(ctx, db.DB, "task-a", StepStatusSucceeded); err != nil {
		t.Fatalf("step status: %v", err)
	}

	objective := "Create an image based on the analysis: {{subject_analysis}}"
	result := injectArtifacts(ctx, db.DB, "ps-1", objective)
	if !strings.Contains(result, "cyberpunk") {
		t.Fatalf("expected placeholder replaced, got %q", result)
	}
	if strings.Contains(result, "{{subject_analysis}}") {
		t.Fatalf("placeholder not replaced in %q", result)
	}
}

func TestInjectArtifacts_ReplacesURLArtifact(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-2", ConversationID: "conv-2", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	_, err := subagent.CreateTask(ctx, db.DB, subagent.CreateTaskInput{
		TaskID: "task-b", ConversationID: "conv-2", AgentType: "plugin_step",
		Title: "image-plugin:generate_image", Mode: "manual",
	})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-2", "generate_image", "task-b", 1); err != nil {
		t.Fatalf("step: %v", err)
	}
	_ = subagent.UpdateFinalStatus(ctx, db.DB, "task-b", subagent.StatusSucceeded, "done")
	_ = subagent.SaveArtifact(ctx, db.DB, "task-b", "raw_image_url", "image",
		json.RawMessage(`{"url":"https://cdn.example.com/img.png"}`), 1)
	_ = UpdateStepStatus(ctx, db.DB, "task-b", StepStatusSucceeded)

	objective := "Enhance {{raw_image_url}} with HDR."
	result := injectArtifacts(ctx, db.DB, "ps-2", objective)
	if !strings.Contains(result, "https://cdn.example.com/img.png") {
		t.Fatalf("URL not injected, got %q", result)
	}
}

func TestInjectArtifacts_IgnoresPendingSteps(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "ps-3", ConversationID: "conv-3", PluginID: "image-plugin",
	}); err != nil {
		t.Fatalf("session: %v", err)
	}
	_, err := subagent.CreateTask(ctx, db.DB, subagent.CreateTaskInput{
		TaskID: "task-c", ConversationID: "conv-3", AgentType: "plugin_step",
		Title: "image-plugin:optimize_prompt", Mode: "manual",
	})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, "ps-3", "optimize_prompt", "task-c", 1); err != nil {
		t.Fatalf("step: %v", err)
	}
	// Step still pending — injectArtifacts must leave placeholder intact.
	_ = subagent.SaveArtifact(ctx, db.DB, "task-c", "optimized_prompt", "text",
		json.RawMessage(`{"text":"a fine English prompt"}`), 1)

	objective := "Use prompt {{optimized_prompt}} to generate."
	result := injectArtifacts(ctx, db.DB, "ps-3", objective)
	if !strings.Contains(result, "{{optimized_prompt}}") {
		t.Fatalf("placeholder should be preserved for pending step, got %q", result)
	}
}

// ──────────────────────────────────────────────
// OnSubAgentDone — status routing
// ──────────────────────────────────────────────

func TestOnSubAgentDone_SucceededManualMode(t *testing.T) {
	t.Setenv("LAZYMIND_PLUGIN_MODE", "manual")

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

	pctx := &PluginChatContext{
		SessionID: "ps-1", PluginID: "image-plugin", StepID: "analyze_subject",
		ConvID: "conv-1", UserID: "user-1", HistoryID: "hist-1",
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
		ConvID: "conv-2", UserID: "user-1", HistoryID: "hist-2",
	}

	var gotEvent string
	var gotPayload map[string]any
	onSSE := func(et string, pl map[string]any) {
		gotEvent = et
		gotPayload = pl
	}

	OnSubAgentDone(ctx, db.DB, nil, "task-2", subagent.StatusInterrupted, "heartbeat timeout", onSSE, pctx)

	if gotEvent != "step_waiting" {
		t.Fatalf("expected step_waiting for interrupted, got %q", gotEvent)
	}
	if gotPayload["interrupted"] != true {
		t.Fatalf("expected interrupted=true in payload, got %v", gotPayload)
	}

	// Session status must be 'waiting' not 'failed'.
	s, _ := GetSession(ctx, db.DB, "ps-2")
	if s.Status != SessionStatusWaiting {
		t.Fatalf("expected session waiting, got %s", s.Status)
	}
}

func TestOnSubAgentDone_Failed_SetsSessionFailed(t *testing.T) {
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

	var gotEvent string
	onSSE := func(et string, _ map[string]any) { gotEvent = et }

	OnSubAgentDone(ctx, db.DB, nil, "task-3", subagent.StatusFailed, "step error", onSSE, pctx)

	if gotEvent != "plugin_error" {
		t.Fatalf("expected plugin_error, got %q", gotEvent)
	}
	s, _ := GetSession(ctx, db.DB, "ps-3")
	if s.Status != SessionStatusFailed {
		t.Fatalf("expected session failed, got %s", s.Status)
	}
}

// ──────────────────────────────────────────────
// callDriverAgent — mock HTTP server
// ──────────────────────────────────────────────

func TestCallDriverAgent_ParsesVerdict(t *testing.T) {
	cases := []struct {
		body            string
		wantVerdict     string
		wantReasonHas   string
	}{
		{
			body:          `{"verdict":"PASS","reason":"Prompt looks good."}`,
			wantVerdict:   "PASS",
			wantReasonHas: "good",
		},
		{
			body:          `{"verdict":"done","reason":"All steps complete."}`,
			wantVerdict:   "DONE",
			wantReasonHas: "complete",
		},
		{
			body:          `{"verdict":"RETRY","reason":"No artifact found."}`,
			wantVerdict:   "RETRY",
			wantReasonHas: "artifact",
		},
		{
			body:          `{"verdict":"FAIL","reason":"Repeated failures."}`,
			wantVerdict:   "FAIL",
			wantReasonHas: "failures",
		},
	}

	for _, tc := range cases {
		t.Run(tc.wantVerdict, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			// Point chat service endpoint at the mock server.
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

			verdict, reason := callDriverAgent("image-plugin", "optimize_prompt", "step output", "ps-1")
			if verdict != tc.wantVerdict {
				t.Fatalf("expected verdict %s, got %s", tc.wantVerdict, verdict)
			}
			if tc.wantReasonHas != "" && !strings.Contains(reason, tc.wantReasonHas) {
				t.Fatalf("expected reason to contain %q, got %q", tc.wantReasonHas, reason)
			}
		})
	}
}

func TestCallDriverAgent_DefaultsToPassOnError(t *testing.T) {
	// Point to a non-existent server so the HTTP call fails.
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://127.0.0.1:19999")

	verdict, _ := callDriverAgent("image-plugin", "generate_image", "result", "ps-1")
	if verdict != "PASS" {
		t.Fatalf("expected PASS fallback, got %s", verdict)
	}
}

func TestCallDriverAgent_DefaultsToPassOnUnknownVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"verdict":"UNKNOWN","reason":"weird"}`)
	}))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)

	verdict, _ := callDriverAgent("image-plugin", "analyze_subject", "output", "ps-1")
	if verdict != "PASS" {
		t.Fatalf("expected PASS for unknown verdict, got %s", verdict)
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
// buildSyntheticMessage
// ──────────────────────────────────────────────

func TestBuildSyntheticMessage(t *testing.T) {
	cases := []struct {
		verdict string
		stepID  string
		reason  string
		wantHas []string
	}{
		{"PASS", "optimize_prompt", "looks good", []string{"optimize_prompt", "looks good", "Proceed"}},
		{"RETRY", "generate_image", "no url", []string{"generate_image", "no url", "Retry"}},
	}
	for _, tc := range cases {
		msg := buildSyntheticMessage(tc.verdict, tc.stepID, tc.reason)
		for _, want := range tc.wantHas {
			if !strings.Contains(msg, want) {
				t.Errorf("verdict=%s: message %q missing %q", tc.verdict, msg, want)
			}
		}
	}
}

// ──────────────────────────────────────────────
// defaultMode
// ──────────────────────────────────────────────

func TestDefaultMode(t *testing.T) {
	os.Unsetenv("LAZYMIND_PLUGIN_MODE")
	if defaultMode() != "auto" {
		t.Fatal("expected auto when env unset")
	}
	t.Setenv("LAZYMIND_PLUGIN_MODE", "manual")
	if defaultMode() != "manual" {
		t.Fatal("expected manual")
	}
	t.Setenv("LAZYMIND_PLUGIN_MODE", "invalid")
	if defaultMode() != "auto" {
		t.Fatal("expected auto for invalid value")
	}
}
