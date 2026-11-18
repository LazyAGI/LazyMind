package algo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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
	for _, code := range []string{"task_timeout", "quota_exhausted", "permission_denied", "provider_overloaded", "service_unavailable", "unknown-private-provider-error"} {
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

func TestPaperGuideStructuredTaskTransportPreservesChapterID(t *testing.T) {
	endpoint := startGenerateTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			TaskType string `json:"task_type"`
			Input    struct {
				Data struct {
					ChapterID string `json:"chapter_id"`
				} `json:"data"`
			} `json:"input"`
			Options map[string]string `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode structured task: %v", err)
		}
		if request.TaskType != "paper.explainer.chapter" || request.Options["task_version"] != "paper-guide-v26" ||
			request.Input.Data.ChapterID != "overview" {
			t.Errorf("lost guide request metadata: %+v", request)
		}
		fmt.Fprint(w, `{"status":"succeeded","output":{"id":"overview"},"usage":{"model_calls":1,"task_type":"paper.explainer.chapter","task_version":"paper-guide-v26"}}`)
	}))
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", endpoint)
	result, err := RunStructuredTask(context.Background(), StructuredTaskRequest{
		Task: "paper.explainer.chapter", Version: "paper-guide-v26", Input: map[string]any{"chapter_id": "overview"},
	})
	if err != nil || string(result.Output) != `{"id":"overview"}` {
		t.Fatalf("response %s: %v", result.Output, err)
	}
}

func TestStructuredTaskSuccessContractMismatchRetainsSafeTypedUsage(t *testing.T) {
	for _, field := range []string{"task_type", "task_version", "model_calls"} {
		t.Run(field, func(t *testing.T) {
			usage := StructuredTaskUsage{Task: "paper.explainer.plan", Version: "paper-guide-v26", ModelCalls: 1, ElapsedMS: 19}
			switch field {
			case "task_type":
				usage.Task = "paper.explainer.chapter"
			case "task_version":
				usage.Version = "paper-guide-v2"
			case "model_calls":
				usage.ModelCalls = 2
			}
			calls := 0
			endpoint := startGenerateTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				json.NewEncoder(w).Encode(map[string]any{"status": "succeeded", "output": map[string]any{"private": "provider-secret"}, "usage": usage})
			}))
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", endpoint)
			result, err := RunStructuredTask(t.Context(), StructuredTaskRequest{Task: "paper.explainer.plan", Version: "paper-guide-v26"})
			var failure *StructuredTaskError
			if !errors.As(err, &failure) || failure.Code != "invalid_model_output" || failure.Usage != usage || failure.Retryable || len(result.Output) != 0 || calls != 1 {
				t.Fatalf("wrong classification, usage or attempts: result=%+v err=%#v calls=%d", result, err, calls)
			}
			if strings.Contains(err.Error(), "provider-secret") {
				t.Fatal("raw output leaked")
			}
		})
	}
}

type structuredTaskTrip func(*http.Request) (*http.Response, error)

func (trip structuredTaskTrip) RoundTrip(r *http.Request) (*http.Response, error) { return trip(r) }

func TestStructuredTaskTransportTimeoutDefaultsAndRejectsNegative(t *testing.T) {
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://fixture.invalid")
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	calls := 0
	http.DefaultTransport = structuredTaskTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if remaining := time.Until(deadline); !ok || remaining > 120*time.Second || remaining < 119*time.Second {
			t.Errorf("default HTTP deadline %v (present=%t)", remaining, ok)
		}
		body := `{"status":"succeeded","output":{},"usage":{"model_calls":1,"task_type":"paper.formula.explain","task_version":"formula-explanation-v7-low-latency"}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	req := StructuredTaskRequest{Task: FormulaExplanationTask, Version: FormulaExplanationTaskVersion}
	if _, err := RunStructuredTask(context.Background(), req); err != nil || calls != 1 {
		t.Fatalf("default call: calls=%d err=%v", calls, err)
	}
	req.TransportTimeout = -time.Second
	_, err := RunStructuredTask(context.Background(), req)
	var taskErr *StructuredTaskError
	if !errors.As(err, &taskErr) || taskErr.Code != "invalid_task_config" || calls != 1 {
		t.Fatalf("negative timeout reached transport: calls=%d err=%v", calls, err)
	}
}
