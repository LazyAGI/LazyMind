package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestStreamChatUpstreamRejectsMalformedFrame(t *testing.T) {
	server := streamServer(t, "run_test", "not-json")
	defer server.Close()

	chunks := collectUpstream(t, server.URL, "run_test")
	if len(chunks) != 1 || chunks[0].Err == nil || !strings.Contains(chunks[0].Err.Error(), "invalid algorithm stream frame") {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}
