package chat

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/state"
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

func TestRetryChatCancelSignalRecoversFromTransientErrors(t *testing.T) {
	attempts := 0
	received, err := retryChatCancelSignal(context.Background(), func(context.Context) (bool, error) {
		attempts++
		if attempts < 3 {
			return false, errors.New("temporary state backend failure")
		}
		return true, nil
	}, nil, time.Microsecond, time.Microsecond, 2*time.Microsecond)
	if err != nil || !received || attempts != 3 {
		t.Fatalf("received=%v err=%v attempts=%d, want true/nil/3", received, err, attempts)
	}
}

func TestRetryChatCancelSignalStopsBackoffOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	received, err := retryChatCancelSignal(ctx, func(context.Context) (bool, error) {
		return false, errors.New("temporary state backend failure")
	}, func(error, time.Duration) {
		cancel()
	}, time.Hour, time.Hour, time.Hour)
	if received || !errors.Is(err, context.Canceled) {
		t.Fatalf("received=%v err=%v, want false/context canceled", received, err)
	}
}

func TestRetryChatCancelSignalEmptyPollDoesNotCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	received, err := retryChatCancelSignal(ctx, func(context.Context) (bool, error) {
		attempts++
		if attempts == 3 {
			cancel()
		}
		return false, nil
	}, nil, time.Microsecond, time.Microsecond, 2*time.Microsecond)
	if received || !errors.Is(err, context.Canceled) || attempts != 3 {
		t.Fatalf("received=%v err=%v attempts=%d, want false/context canceled/3", received, err, attempts)
	}
}

func TestClearChatDataRemovesStaleStopSignal(t *testing.T) {
	ctx := context.Background()
	stateStore, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer stateStore.Close()

	if err := setChatCancelSignal(ctx, stateStore, "conv", "history"); err != nil {
		t.Fatalf("set stop signal: %v", err)
	}
	if err := clearChatData(ctx, stateStore, "conv", "history"); err != nil {
		t.Fatalf("clear chat data: %v", err)
	}
	received, err := stateStore.LPop(ctx, chatStopKey("conv", "history"))
	if err != nil || received {
		t.Fatalf("stale stop signal received=%v err=%v, want false/nil", received, err)
	}
}

func TestCancelChatOnStopOnlyCancelsForReceivedSignal(t *testing.T) {
	stateStore, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer stateStore.Close()

	t.Run("watcher context ends without stop", func(t *testing.T) {
		watchCtx, stopWatcher := context.WithCancel(context.Background())
		chatCtx, cancelChat := context.WithCancel(context.Background())
		defer cancelChat()
		done := make(chan struct{})
		go func() {
			defer close(done)
			cancelChatOnStop(watchCtx, stateStore, "conv", "empty", cancelChat)
		}()
		stopWatcher()
		<-done
		if chatCtx.Err() != nil {
			t.Fatalf("chat context error=%v, want nil", chatCtx.Err())
		}
	})

	t.Run("received stop cancels chat", func(t *testing.T) {
		if err := setChatCancelSignal(context.Background(), stateStore, "conv", "signal"); err != nil {
			t.Fatalf("set stop signal: %v", err)
		}
		chatCtx, cancelChat := context.WithCancel(context.Background())
		defer cancelChat()
		cancelChatOnStop(context.Background(), stateStore, "conv", "signal", cancelChat)
		if !errors.Is(chatCtx.Err(), context.Canceled) {
			t.Fatalf("chat context error=%v, want context canceled", chatCtx.Err())
		}
	})
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
		DeltaMode:        ChatDeltaModeReplace,
		HistoryID:        "hist-1",
		ReasoningContent: "thinking...",
		RuntimeEvent:     completedRunEvent("run-1", true),
	}
	bs, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored ChatChunkResponse
	if err := json.Unmarshal(bs, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if restored.ConversationID != orig.ConversationID || restored.Delta != orig.Delta || restored.DeltaMode != orig.DeltaMode {
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

func TestAppendConvEventPreservesCursorAfterFormerListLimit(t *testing.T) {
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}
	defer stateStore.Close()

	const conversationID = "conv-cursor"
	for i := 0; i < 1002; i++ {
		if err := AppendConvEvent(t.Context(), stateStore, conversationID, &ConvEvent{
			Type: "task_created",
			Payload: map[string]any{
				"sequence": i,
			},
		}); err != nil {
			t.Fatalf("append event %d: %v", i, err)
		}
	}

	events, err := stateStore.LRange(t.Context(), convEventsKey(conversationID), 0, -1)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1002 {
		t.Fatalf("event count=%d want=1002", len(events))
	}

	ctx, cancel := context.WithCancel(t.Context())
	seen := []int64{}
	err = WatchConvEvents(ctx, stateStore, conversationID, 999, func(index int64, _ *ConvEvent) error {
		seen = append(seen, index)
		if len(seen) == 2 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("watch error=%v want context canceled", err)
	}
	if len(seen) != 2 || seen[0] != 1000 || seen[1] != 1001 {
		t.Fatalf("seen indexes=%v want [1000 1001]", seen)
	}
}
