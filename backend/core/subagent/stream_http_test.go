package subagent

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

// sseTestRecorder wraps a strings.Builder and implements http.Flusher for SSE tests.
type sseTestRecorder struct {
	strings.Builder
	header http.Header
}

func (r *sseTestRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}
func (r *sseTestRecorder) WriteHeader(statusCode int) {}
func (r *sseTestRecorder) Flush()                     {}

// TestWriteTaskSSE_WritesSSEFormat produces proper SSE data lines.
func TestWriteTaskSSE_WritesSSEFormat(t *testing.T) {
	rec := &sseTestRecorder{}
	ev := map[string]any{"type": "text", "content": "hello"}
	writeTaskSSE(rec, rec, ev)

	body := rec.String()
	if !strings.Contains(body, "data: ") {
		t.Fatalf("expected SSE data prefix, got: %s", body)
	}
	if !strings.Contains(body, `"type":"text"`) {
		t.Fatalf("expected type field in SSE body: %s", body)
	}
}

// TestWriteTaskSSE_WritesEvenWithoutFlush writes body even when flusher is nil.
func TestWriteTaskSSE_WritesEvenWithoutFlush(t *testing.T) {
	rec := &sseTestRecorder{}
	ev := map[string]any{"type": "done"}
	writeTaskSSE(rec, nil, ev) // nil flusher
	if rec.Len() == 0 {
		t.Fatal("expected non-empty body even with nil flusher")
	}
}

// TestEmitTerminal_WritesTerminalSSE produces a terminal event with status and summary.
func TestEmitTerminal_WritesTerminalSSE(t *testing.T) {
	rec := &sseTestRecorder{}
	emitTerminal(rec, rec, "task-1", "succeeded", "All done")

	body := rec.String()
	if !strings.Contains(body, "succeeded") {
		t.Fatalf("expected succeeded in terminal event: %s", body)
	}
	if !strings.Contains(body, "All done") {
		t.Fatalf("expected summary in terminal event: %s", body)
	}
	if !strings.Contains(body, "data: ") {
		t.Fatalf("expected SSE data prefix: %s", body)
	}
}

// TestEmitTerminal_EmptyStatus writes SSE even with empty status and summary.
func TestEmitTerminal_EmptyStatus(t *testing.T) {
	rec := &sseTestRecorder{}
	emitTerminal(rec, rec, "task-2", "", "")

	body := rec.String()
	if !strings.Contains(body, "data: ") {
		t.Fatalf("expected SSE data even with empty status: %s", body)
	}
}

// TestStepToTaskEvent_RoundTripAssembled verifies step → event can be serialized to SSE.
func TestStepToTaskEvent_RoundTripAssembled(t *testing.T) {
	s := &orm.SubAgentStep{
		Seq:     1,
		Role:    "text",
		Content: json.RawMessage(`{"content":"hello world"}`),
	}
	ev := stepToTaskEvent("task-r", s)
	if ev == nil {
		t.Fatal("expected non-nil event")
	}
	// Verify event can be serialized to SSE without panic.
	rec := &sseTestRecorder{}
	writeTaskSSE(rec, nil, ev)
	if rec.Len() == 0 {
		t.Fatal("expected non-empty SSE output")
	}
}
