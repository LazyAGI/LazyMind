package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func algorithmFrame(t *testing.T, data map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"code": 200, "msg": "success", "data": data, "cost": 0})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func runFinishedFrame(t *testing.T, runID string) string {
	t.Helper()
	return algorithmFrame(t, map[string]any{"runtime_event": map[string]any{
		"schema_version": 1,
		"event_id":       "evt_test",
		"run_id":         runID,
		"type":           RuntimeEventRunFinished,
		"data": map[string]any{
			"status": "completed", "reason": "normal", "partial_output": true,
		},
	}})
}

func streamServer(t *testing.T, runID string, lines ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Conversation.RunID != runID {
			t.Errorf("run_id = %q, want %q", req.Conversation.RunID, runID)
		}
		_, _ = w.Write([]byte(strings.Join(lines, "\n") + "\n"))
	}))
}

func collectUpstream(t *testing.T, serverURL, runID string) []UpstreamStreamChunk {
	t.Helper()
	stream, _, err := StreamChatUpstream(context.Background(), serverURL, map[string]any{"run_id": runID})
	if err != nil {
		t.Fatal(err)
	}
	var chunks []UpstreamStreamChunk
	for chunk := range stream {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func TestStreamChatUpstreamRequiresRunFinished(t *testing.T) {
	server := streamServer(t, "run_test", algorithmFrame(t, map[string]any{"text": "partial"}))
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 2 || chunks[1].Err == nil || !strings.Contains(chunks[1].Err.Error(), "without run_finished") {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestStreamChatUpstreamBuffersTerminalUntilEOF(t *testing.T) {
	server := streamServer(t, "run_test",
		algorithmFrame(t, map[string]any{"text": "ok"}),
		runFinishedFrame(t, "run_test"),
	)
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 2 || chunks[0].Text != "ok" || chunks[1].RuntimeEvent == nil || chunks[1].Err != nil {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestStreamChatUpstreamPreservesPerformanceMetricsOnTerminal(t *testing.T) {
	server := streamServer(t, "run_test", algorithmFrame(t, map[string]any{
		"runtime_event": map[string]any{
			"schema_version": 1,
			"event_id":       "evt_performance",
			"run_id":         "run_test",
			"type":           RuntimeEventRunFinished,
			"data": map[string]any{
				"status": "completed", "reason": "normal", "partial_output": true,
			},
		},
		"performance_metrics": map[string]any{
			"schema_version": 1,
			"turn_seq":       3,
			"model_steps":    2,
			"tool_steps":     1,
			"wall_ms":        1200,
			"model_ms":       900,
			"tool_ms":        200,
			"input_tokens":   100,
			"output_tokens":  20,
			"cached_tokens":  40,
		},
	}))
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 1 || chunks[0].PerformanceMetrics == nil {
		t.Fatalf("performance metrics were dropped: %#v", chunks)
	}
	metrics := chunks[0].PerformanceMetrics
	if metrics.SchemaVersion != 1 || metrics.TurnSeq == nil || *metrics.TurnSeq != 3 {
		t.Fatalf("unexpected performance envelope: %#v", metrics)
	}
	if metrics.ModelMS == nil || *metrics.ModelMS != 900 || metrics.CachedTokens == nil || *metrics.CachedTokens != 40 {
		t.Fatalf("unexpected performance facts: %#v", metrics)
	}
}

func TestStreamChatUpstreamPreservesTerminalOnAbnormalEOF(t *testing.T) {
	frame := runFinishedFrame(t, "run_test") + "\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(frame)+16))
		_, _ = w.Write([]byte(frame))
	}))
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 1 || chunks[0].RuntimeEvent == nil || chunks[0].Err != nil {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestStreamChatUpstreamRejectsPayloadAfterTerminal(t *testing.T) {
	server := streamServer(t, "run_test",
		runFinishedFrame(t, "run_test"),
		algorithmFrame(t, map[string]any{"text": "late"}),
	)
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 1 || chunks[0].Err == nil || !strings.Contains(chunks[0].Err.Error(), "after run_finished") {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestStreamChatUpstreamRejectsDuplicateTerminal(t *testing.T) {
	server := streamServer(t, "run_test", runFinishedFrame(t, "run_test"), runFinishedFrame(t, "run_test"))
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 1 || chunks[0].Err == nil || !strings.Contains(chunks[0].Err.Error(), "duplicate run_finished") {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestStreamChatUpstreamRejectsPayloadOnTerminalFrame(t *testing.T) {
	server := streamServer(t, "run_test", algorithmFrame(t, map[string]any{
		"text": "late",
		"runtime_event": map[string]any{
			"schema_version": 1,
			"event_id":       "evt_test",
			"run_id":         "run_test",
			"type":           RuntimeEventRunFinished,
			"data": map[string]any{
				"status": "completed", "reason": "normal", "partial_output": true,
			},
		},
	}))
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 1 || chunks[0].Err == nil || !strings.Contains(chunks[0].Err.Error(), "combined run_finished") {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestStreamChatUpstreamRejectsMismatchedRunIDAsPureError(t *testing.T) {
	server := streamServer(t, "run_test", runFinishedFrame(t, "wrong_run"))
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 1 || chunks[0].Err == nil || !strings.Contains(chunks[0].Err.Error(), "run_id mismatch") {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
	if chunks[0].RuntimeEvent != nil || hasBusinessStreamPayload(chunks[0]) {
		t.Fatalf("protocol error was not emitted as a pure error chunk: %#v", chunks[0])
	}
}

func TestStreamChatUpstreamRejectsMalformedFrame(t *testing.T) {
	server := streamServer(t, "run_test", "not-json")
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 1 || chunks[0].Err == nil || !strings.Contains(chunks[0].Err.Error(), "invalid algorithm stream frame") {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestStreamChatUpstreamRejectsMalformedFrameAfterTerminal(t *testing.T) {
	server := streamServer(t, "run_test", runFinishedFrame(t, "run_test"), "not-json")
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 1 || chunks[0].Err == nil || !strings.Contains(chunks[0].Err.Error(), "invalid algorithm stream frame") {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestRunTerminalRejectsInvalidContract(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "invalid combination", data: `{"status":"completed","reason":"runtime_failure","partial_output":false}`},
		{name: "missing partial output", data: `{"status":"failed","reason":"runtime_failure"}`},
		{name: "invalid partial output", data: `{"status":"failed","reason":"runtime_failure","partial_output":null}`},
		{name: "null model invoked", data: `{"status":"completed","reason":"normal","partial_output":true,"model_invoked":null}`},
		{name: "string model invoked", data: `{"status":"completed","reason":"normal","partial_output":true,"model_invoked":"false"}`},
		{name: "numeric model invoked", data: `{"status":"completed","reason":"normal","partial_output":true,"model_invoked":0}`},
		{name: "completed with code", data: `{"status":"completed","reason":"normal","code":"rate_limited","partial_output":false}`},
		{name: "cancelled with code", data: `{"status":"cancelled","reason":"user_cancelled","code":"user_cancelled","partial_output":false}`},
		{name: "model incomplete without code", data: `{"status":"interrupted","reason":"model_incomplete","partial_output":true}`},
		{name: "model incomplete with failure code", data: `{"status":"interrupted","reason":"model_incomplete","code":"rate_limited","partial_output":true}`},
		{name: "model failure without code", data: `{"status":"failed","reason":"model_failure","partial_output":false}`},
		{name: "runtime failure without code", data: `{"status":"failed","reason":"runtime_failure","partial_output":false}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := &ChatRuntimeEvent{Type: RuntimeEventRunFinished, Data: json.RawMessage(test.data)}
			if _, err := event.Terminal(); err == nil {
				t.Fatal("invalid run terminal was accepted")
			}
		})
	}
}

func TestRunTerminalPreservesOptionalModelInvocation(t *testing.T) {
	for _, value := range []string{"", "false", "true"} {
		t.Run("model_invoked="+value, func(t *testing.T) {
			raw := `{"status":"completed","reason":"normal","partial_output":true`
			if value != "" {
				raw += `,"model_invoked":` + value
			}
			raw += `}`
			event := storedRunEvent("run_test", json.RawMessage(raw))
			terminal, err := event.Terminal()
			if err != nil || terminal.Status != "completed" {
				t.Fatalf("valid invocation flag rejected: %#v err=%v", terminal, err)
			}
			if terminal.modelWasInvoked() != (value != "false") {
				t.Fatalf("unexpected invocation interpretation: %#v", terminal)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(event.Data, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["model_invoked"]) != value {
				t.Fatalf("stored terminal lost invocation flag: %s", event.Data)
			}
		})
	}
}

func TestRuntimeEventValidatesModelRetryScheduled(t *testing.T) {
	valid := json.RawMessage(`{"model_call_id":"call-1","retry_index":1,"max_attempts":3,"delay_ms":1000,"future_field":true}`)
	if err := (&ChatRuntimeEvent{
		SchemaVersion: 1, EventID: "evt-1", RunID: "run-1", Type: RuntimeEventModelRetryScheduled, Data: valid,
	}).Validate("run-1"); err != nil {
		t.Fatalf("valid retry event rejected: %v", err)
	}

	invalid := []string{
		`{}`,
		`{"model_call_id":"call-1","retry_index":0,"max_attempts":3,"delay_ms":0}`,
		`{"model_call_id":"call-1","retry_index":3,"max_attempts":3,"delay_ms":0}`,
		`{"model_call_id":"call-1","retry_index":1,"max_attempts":3,"delay_ms":-1}`,
		`{"model_call_id":"call-1","retry_index":1.5,"max_attempts":3,"delay_ms":0}`,
	}
	for _, raw := range invalid {
		event := &ChatRuntimeEvent{
			SchemaVersion: 1, EventID: "evt-1", RunID: "run-1", Type: RuntimeEventModelRetryScheduled, Data: json.RawMessage(raw),
		}
		if err := event.Validate("run-1"); err == nil {
			t.Fatalf("invalid retry event was accepted: %s", raw)
		}
	}
}

func TestRuntimeEventValidatesModelCallFinished(t *testing.T) {
	valid := []string{
		`{"model_call_id":"call-1","attempt_count":1,"kind":"finish","has_semantic_output":true,"finish":"stop","future_field":true}`,
		`{"model_call_id":"call-1","attempt_count":2,"kind":"failure","has_semantic_output":false,"failure":{"origin":"http","code":"rate_limited","future_field":true}}`,
	}
	for _, raw := range valid {
		event := &ChatRuntimeEvent{
			SchemaVersion: 1, EventID: "evt-1", RunID: "run-1", Type: RuntimeEventModelCallFinished, Data: json.RawMessage(raw),
		}
		if err := event.Validate("run-1"); err != nil {
			t.Fatalf("valid model terminal rejected: %s: %v", raw, err)
		}
	}

	invalid := []string{
		`{}`,
		`{"model_call_id":"call-1","attempt_count":0,"kind":"finish","has_semantic_output":false,"finish":"stop"}`,
		`{"model_call_id":"call-1","attempt_count":1,"kind":"finish","has_semantic_output":false}`,
		`{"model_call_id":"call-1","attempt_count":1,"kind":"finish","has_semantic_output":false,"finish":"custom"}`,
		`{"model_call_id":"call-1","attempt_count":1,"kind":"finish","has_semantic_output":false,"finish":"stop","failure":{"origin":"http","code":"rate_limited"}}`,
		`{"model_call_id":"call-1","attempt_count":1,"kind":"failure","has_semantic_output":false}`,
		`{"model_call_id":"call-1","attempt_count":1,"kind":"failure","has_semantic_output":false,"failure":{"origin":"custom","code":"rate_limited"}}`,
		`{"model_call_id":"call-1","attempt_count":1,"kind":"failure","has_semantic_output":false,"failure":{"origin":"http","code":"custom"}}`,
	}
	for _, raw := range invalid {
		event := &ChatRuntimeEvent{
			SchemaVersion: 1, EventID: "evt-1", RunID: "run-1", Type: RuntimeEventModelCallFinished, Data: json.RawMessage(raw),
		}
		if err := event.Validate("run-1"); err == nil {
			t.Fatalf("invalid model terminal was accepted: %s", raw)
		}
	}
}

func TestStoredRunEventRejectsInvalidTerminal(t *testing.T) {
	event := storedRunEvent("run_test", json.RawMessage(`{"status":"completed","reason":"runtime_failure","partial_output":false}`))
	terminal, err := event.Terminal()
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Status != "failed" || terminal.Reason != "runtime_failure" || terminal.Code != "missing_persisted_terminal" {
		t.Fatalf("unexpected fallback terminal: %#v", terminal)
	}
}

func TestRunTerminalPreservesPublicFailureMetadata(t *testing.T) {
	event := runFinishedEvent("run_test", RunTerminal{
		Status:        "failed",
		Reason:        "model_failure",
		Code:          "rate_limited",
		PartialOutput: false,
		ModelCallID:   "call_test",
		DiagnosticID:  "diag_test",
	})

	terminal, err := event.Terminal()
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Code != "rate_limited" || terminal.DiagnosticID != "diag_test" {
		t.Fatalf("provider failure metadata was not preserved: %#v", terminal)
	}
}

func TestStoredRunEventDropsLegacyProviderTransportFields(t *testing.T) {
	event := storedRunEvent("run_test", json.RawMessage(`{
		"status":"failed",
		"reason":"model_failure",
		"code":"rate_limited",
		"partial_output":false,
		"diagnostic_id":"diag_test",
		"provider_http_status":429,
		"retry_after_ms":2000
	}`))

	if err := event.Validate("run_test"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(event.Data), "provider_http_status") || strings.Contains(string(event.Data), "retry_after_ms") {
		t.Fatalf("legacy transport fields were re-exposed: %s", event.Data)
	}
}
