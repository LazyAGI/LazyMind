package chat

import (
	"encoding/json"
	"testing"
)

// --- Key generation functions ---

// TestChatStreamKey generates the correct Redis key format.
func TestChatStreamKey(t *testing.T) {
	got := chatStreamKey("conv-1", "hist-2")
	want := "rag/chat/stream:conv-1:hist-2"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestChatStatusKey generates the correct Redis key format.
func TestChatStatusKey(t *testing.T) {
	got := chatStatusKey("conv-abc")
	want := "rag/chat/status:conv-abc"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestChatStopKey generates the correct Redis key format.
func TestChatStopKey(t *testing.T) {
	got := chatStopKey("conv-1", "hist-1")
	want := "rag/chat/stop:conv-1:hist-1"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestChatMultiKey generates the correct Redis key format.
func TestChatMultiKey(t *testing.T) {
	got := chatMultiKey("conv-1", "primary-h")
	want := "rag/chat/multi:conv-1:primary-h"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestChatInputKey generates the correct Redis key format.
func TestChatInputKey(t *testing.T) {
	got := chatInputKey("conv-1", "hist-1")
	want := "rag/chat/input:conv-1:hist-1"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestConvEventsKey generates the correct Redis key format.
func TestConvEventsKey(t *testing.T) {
	got := convEventsKey("conv-abc")
	want := "rag/conv/events:conv-abc"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestChatStreamKey_EmptyParameters still produces a valid key with empty segments.
func TestChatStreamKey_EmptyParameters(t *testing.T) {
	got := chatStreamKey("", "")
	want := "rag/chat/stream::"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// --- JSON serialization roundtrip ---

// TestChatStatusRoundTrip marshals and unmarshals successfully.
func TestChatStatusRoundTrip(t *testing.T) {
	orig := ChatStatus{
		Status:        "generating",
		CurrentResult: "thinking...",
		LastUpdate:    1700000000,
		TotalChunks:   5,
	}
	bs, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored ChatStatus
	if err := json.Unmarshal(bs, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.Status != orig.Status || restored.TotalChunks != orig.TotalChunks {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", restored, orig)
	}
}

// TestChatInputRoundTrip marshals and unmarshals successfully.
func TestChatInputRoundTrip(t *testing.T) {
	orig := ChatInput{
		RawContent: "hello world",
		Seq:        1,
		CreatedAt:  1700000000,
		Ext:        json.RawMessage(`{"key":"value"}`),
	}
	bs, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored ChatInput
	if err := json.Unmarshal(bs, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.RawContent != orig.RawContent || restored.Seq != orig.Seq {
		t.Fatalf("roundtrip mismatch")
	}
}

// TestMultiAnswerInfoRoundTrip marshals and unmarshals successfully.
func TestMultiAnswerInfoRoundTrip(t *testing.T) {
	orig := MultiAnswerInfo{
		PrimaryHistoryID:   "h1",
		SecondaryHistoryID: "h2",
		Seq:                3,
		CreatedAt:          1700000000,
	}
	bs, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored MultiAnswerInfo
	if err := json.Unmarshal(bs, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.PrimaryHistoryID != orig.PrimaryHistoryID {
		t.Fatalf("roundtrip mismatch")
	}
}

// TestChatChunkResponseRoundTrip marshals and unmarshals successfully.
func TestChatChunkResponseRoundTrip(t *testing.T) {
	orig := ChatChunkResponse{
		ConversationID:   "conv-1",
		Seq:              1,
		Delta:            "hello",
		FinishReason:     "stop",
		HistoryID:        "hist-1",
		ReasoningContent: "thinking...",
		ProviderStatus: &ProviderStatusEvent{
			ModelCallID: "call-1", HTTPStatus: intPtr(200), FinishReason: stringPtr("length"),
		},
	}
	bs, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored ChatChunkResponse
	if err := json.Unmarshal(bs, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.ConversationID != orig.ConversationID || restored.Delta != orig.Delta ||
		restored.ProviderStatus == nil || *restored.ProviderStatus.FinishReason != "length" {
		t.Fatalf("roundtrip mismatch")
	}
}

// TestConvEventRoundTrip marshals and unmarshals successfully.
func TestConvEventRoundTrip(t *testing.T) {
	orig := ConvEvent{
		Type:    "task_created",
		Payload: map[string]any{"task_id": "t1", "title": "My Task"},
	}
	bs, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored ConvEvent
	if err := json.Unmarshal(bs, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.Type != orig.Type {
		t.Fatalf("roundtrip mismatch: type = %q", restored.Type)
	}
}

// --- AppendConvEvent nil guards ---

// TestAppendConvEvent_NilStore returns nil without panic.
func TestAppendConvEvent_NilStore(t *testing.T) {
	if err := AppendConvEvent(t.Context(), nil, "conv-1", &ConvEvent{Type: "test"}); err != nil {
		t.Fatalf("expected nil for nil store, got %v", err)
	}
}

// TestAppendConvEvent_EmptyConversationID returns nil.
func TestAppendConvEvent_EmptyConversationID(t *testing.T) {
	if err := AppendConvEvent(t.Context(), nil, "", &ConvEvent{Type: "test"}); err != nil {
		t.Fatalf("expected nil for empty conversation id, got %v", err)
	}
}

// TestAppendConvEvent_NilEvent returns nil.
func TestAppendConvEvent_NilEvent(t *testing.T) {
	if err := AppendConvEvent(t.Context(), nil, "conv-1", nil); err != nil {
		t.Fatalf("expected nil for nil event, got %v", err)
	}
}
