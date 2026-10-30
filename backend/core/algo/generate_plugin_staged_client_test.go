package algo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common"
)

// TestEnsureLLMConfig returns empty map for nil, original map otherwise.
func TestEnsureLLMConfig(t *testing.T) {
	// nil → empty map
	got := ensureLLMConfig(nil)
	if got == nil {
		t.Fatal("nil should return empty map")
	}

	// Non-nil → same map
	cfg := map[string]any{"model": "gpt-4"}
	if ensureLLMConfig(cfg)["model"] != "gpt-4" {
		t.Fatal("config should be preserved")
	}
}

// TestExtractStringField extracts string from top-level or nested data map.
func TestExtractStringField(t *testing.T) {
	// Top-level key
	raw := map[string]any{"name": "test-skill"}
	if got := extractStringField(raw, "name"); got != "test-skill" {
		t.Fatalf("got %q, want test-skill", got)
	}

	// Fallback to data.key
	raw2 := map[string]any{"data": map[string]any{"name": "nested-skill"}}
	if got := extractStringField(raw2, "name"); got != "nested-skill" {
		t.Fatalf("got %q, want nested-skill", got)
	}

	// Missing key
	if got := extractStringField(map[string]any{}, "missing"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}

	// Nil map
	if got := extractStringField(nil, "name"); got != "" {
		t.Fatalf("nil got %q, want empty", got)
	}

	// Non-string value
	raw3 := map[string]any{"name": 42}
	if got := extractStringField(raw3, "name"); got != "" {
		t.Fatalf("non-string got %q, want empty", got)
	}
}

// TestExtractScripts extracts script map from top-level or nested data.
func TestExtractScripts(t *testing.T) {
	// Top-level scripts
	raw := map[string]any{
		"scripts": map[string]any{
			"main.js":   "console.log(1)",
			"helper.js": "export const x = 1",
			"nonstring": 42,
		},
	}
	scripts := extractScripts(raw)
	if len(scripts) != 2 {
		t.Fatalf("got %d scripts, want 2", len(scripts))
	}
	if scripts["main.js"] != "console.log(1)" {
		t.Fatalf("main.js = %q", scripts["main.js"])
	}

	// Nested under data
	raw2 := map[string]any{
		"data": map[string]any{
			"scripts": map[string]any{
				"index.js": "export {}",
			},
		},
	}
	scripts2 := extractScripts(raw2)
	if len(scripts2) != 1 || scripts2["index.js"] != "export {}" {
		t.Fatalf("got %v", scripts2)
	}

	// No scripts
	if got := extractScripts(map[string]any{}); got != nil {
		t.Fatalf("empty got %v, want nil", got)
	}

	// Nil
	if got := extractScripts(nil); got != nil {
		t.Fatalf("nil got %v, want nil", got)
	}
}

func TestRepairStateMachineUsesLLMTaskRunWithPatchTool(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != llmTaskRunPath {
			t.Fatalf("path = %s, want %s", r.URL.Path, llmTaskRunPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "succeeded",
			"task_id": "task-1",
			"output": map[string]any{
				"state_yaml":         "steps: {}\n",
				"remaining_warnings": []string{},
			},
		})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

	resp, err := RepairStateMachine(t.Context(), RepairStateMachineRequest{
		WorkflowYAML: "id: demo\n",
		StateYAML:    "steps: []\n",
		Target:       "statemachine",
		LLMConfig:    map[string]any{"llm": map[string]any{"model": "x"}},
	})
	if err != nil {
		t.Fatalf("RepairStateMachine error: %v", err)
	}
	if resp.StateYAML != "steps: {}\n" {
		t.Fatalf("state_yaml = %q", resp.StateYAML)
	}
	if captured["task_type"] != "workflow.repair" || captured["mode"] != "agent" {
		t.Fatalf("unexpected task envelope: %#v", captured)
	}
	options := captured["options"].(map[string]any)
	if budget := options["total_timeout_seconds"].(float64); budget < 869 || budget > 870 {
		t.Fatalf("repair budget must leave time inside HTTP deadline: %v", options)
	}
	tools, _ := captured["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "str_replace" {
		t.Fatalf("tools = %#v", captured["tools"])
	}
	input := captured["input"].(map[string]any)
	files := input["files"].([]any)
	if len(files) < 2 || files[0].(map[string]any)["path"] != "workflow.yaml" {
		t.Fatalf("files = %#v", files)
	}
}

func TestRepairTaskPreservesSafeFailureMetadata(t *testing.T) {
	for _, code := range []string{"transport_error", "request_timeout", "authentication_failed", "rate_limited", "invalid_model_output"} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]any{"detail": map[string]any{
					"error_code": code, "retryable": true, "diagnostic_id": "diag-123", "error": "secret-provider-body",
				}})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			_, err := RepairStateMachine(t.Context(), RepairStateMachineRequest{Target: "statemachine"})
			var failure *WorkflowTaskError
			if !errors.As(err, &failure) || failure.Code != code || failure.DiagnosticID != "diag-123" || !failure.Retryable {
				t.Fatalf("failure classification lost: %+v", err)
			}
			if err.Error() != "workflow AI task failed: "+code {
				t.Fatalf("unsafe error message: %v", err)
			}
		})
	}
}

func TestRepairTaskBudgetUsesCallerDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request llmTaskRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if budget := request.Options["total_timeout_seconds"].(float64); budget <= 0 || budget > 1.8 {
			t.Errorf("budget exceeds caller's remaining deadline: %v", budget)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "succeeded", "output": map[string]any{"state_yaml": "steps: {}"}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := RepairStateMachine(ctx, RepairStateMachineRequest{}); err != nil {
		t.Fatal(err)
	}
	expired, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	_, err := RepairStateMachine(expired, RepairStateMachineRequest{})
	var failure *WorkflowTaskError
	if !errors.As(err, &failure) || failure.Code != "request_timeout" {
		t.Fatalf("expired request was not classified: %v", err)
	}
}

func TestWorkflowTaskFailureSanitizesMetadataAndRecognizesGatewayTimeout(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		code   string
	}{
		{504, `{"detail":"private secret-provider-body"}`, "request_timeout"},
		{502, `{"detail":{"error_code":"transport_error","diagnostic_id":"https://private.invalid/secret"}}`, "transport_error"},
		{429, `{"detail":{"error_code":"quota_exhausted","retryable":false}}`, "quota_exhausted"},
		{502, `{"detail":{"error_code":"secret-provider-body"}}`, "service_unavailable"},
	} {
		failure := workflowTaskCallError(&common.HTTPError{StatusCode: tc.status, Body: json.RawMessage(tc.body)})
		if failure.Code != tc.code || failure.DiagnosticID != "" || strings.Contains(failure.Error(), "secret") {
			t.Fatalf("unsafe or misclassified failure: %+v", failure)
		}
		if tc.code == "quota_exhausted" && failure.Retryable {
			t.Fatal("HTTP 429 overrode a non-retryable quota failure")
		}
	}
}
