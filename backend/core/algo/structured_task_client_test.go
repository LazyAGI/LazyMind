package algo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestStructuredTaskPreservesObjectAndSingleAttemptContract(t *testing.T) {
	calls := 0
	endpoint := startGenerateTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/chat/llm-task:run" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["task_type"] != "paper.formula.explain" || body["mode"] != "llm" {
			t.Errorf("bad task %+v", body)
		}
		if _, ok := body["instruction"]; ok {
			t.Error("business prompt sent by Core")
		}
		if body["options"].(map[string]any)["task_version"] != "formula-explanation-v7-low-latency" {
			t.Error("missing version")
		}
		if body["input"].(map[string]any)["data"].(map[string]any)["language"] != "zh" {
			t.Error("missing typed input")
		}
		fmt.Fprint(w, `{"status":"succeeded","output":{"summary":"Meaning","symbols":[]},"usage":{"model_calls":1,"task_type":"paper.formula.explain","task_version":"formula-explanation-v7-low-latency"}}`)
	}))
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", endpoint)
	result, err := RunStructuredTask(context.Background(), StructuredTaskRequest{Task: FormulaExplanationTask, Version: FormulaExplanationTaskVersion, Input: map[string]any{"language": "zh"}})
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err = json.Unmarshal(result.Output, &output); err != nil || output["summary"] != "Meaning" {
		t.Fatalf("lost structured output: %s %v", result.Output, err)
	}
	if calls != 1 || result.Usage.ModelCalls != 1 {
		t.Fatalf("calls=%d metadata=%+v", calls, result.Usage)
	}
}

func TestStructuredTaskRejectsInvalidResponsesWithoutRetryOrRewriteFallback(t *testing.T) {
	for _, body := range []string{
		`{"status":"failed","error":"private-provider-secret"}`,
		`{"status":"succeeded","output":"{\"summary\":\"not an object\"}"}`,
		`{"status":"succeeded","output":null}`,
		`{"status":"succeeded","output":{},"usage":{"task_type":"wrong","task_version":"wrong"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			endpoint := startGenerateTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, body) }))
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", endpoint)
			_, err := RunStructuredTask(context.Background(), StructuredTaskRequest{Task: VisualExplanationTask, Version: VisualExplanationTaskVersion, Input: map[string]any{}})
			if err == nil || calls != 1 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
			if err.Error() == "private-provider-secret" {
				t.Fatal("unsafe failure")
			}
		})
	}
}

func TestStructuredTaskHTTPFailureDoesNotFallback(t *testing.T) {
	calls := 0
	endpoint := startGenerateTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(404)
		fmt.Fprint(w, `private-token`)
	}))
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", endpoint)
	_, err := RunStructuredTask(context.Background(), StructuredTaskRequest{Task: VisualExplanationTask, Version: VisualExplanationTaskVersion, Input: map[string]any{}})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestStructuredTaskPreservesSafeFailureClassificationAndUsage(t *testing.T) {
	endpoint := startGenerateTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		fmt.Fprint(w, `{"detail":{"error_code":"rate_limited","retryable":true,"diagnostic_id":"diag-1","usage":{"model_calls":1,"elapsed_ms":17}}}`)
	}))
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", endpoint)
	_, err := RunStructuredTask(context.Background(), StructuredTaskRequest{Task: VisualExplanationTask, Version: VisualExplanationTaskVersion, Input: map[string]any{}})
	failure, ok := err.(*StructuredTaskError)
	if !ok || failure.Code != "rate_limited" || !failure.Retryable || failure.DiagnosticID != "diag-1" || failure.Usage.ModelCalls != 1 {
		t.Fatalf("failure metadata lost: %+v", err)
	}
}

func TestStructuredTaskKeepsProviderAndUnknownFailureMetadata(t *testing.T) {
	for _, code := range []string{"quota_exhausted", "permission_denied", "provider_overloaded", "service_unavailable", "unknown-private-provider-error"} {
		t.Run(code, func(t *testing.T) {
			endpoint := startGenerateTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(502)
				json.NewEncoder(w).Encode(map[string]any{"detail": map[string]any{"error_code": code, "retryable": false, "diagnostic_id": "diag-safe", "usage": map[string]any{"model_calls": 1, "elapsed_ms": 42}}})
			}))
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", endpoint)
			_, err := RunStructuredTask(context.Background(), StructuredTaskRequest{Task: VisualExplanationTask, Version: VisualExplanationTaskVersion, Input: map[string]any{}})
			failure, ok := err.(*StructuredTaskError)
			want := code
			if code == "unknown-private-provider-error" {
				want = "upstream_error"
			}
			if !ok || failure.Code != want || failure.Retryable || failure.DiagnosticID != "diag-safe" || failure.Usage.ModelCalls != 1 || failure.Usage.ElapsedMS != 42 {
				t.Fatalf("lost safe error metadata: %#v", err)
			}
		})
	}
}
