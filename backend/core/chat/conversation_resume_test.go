package chat

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/state"
)

func TestResumeSingleAnswerSendsCachedTerminalExactlyOnceWhileStatusGenerating(t *testing.T) {
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	const conversationID = "conversation-resume"
	const historyID = "history-resume"
	terminalEvent := completedRunEvent("run-resume", true)
	terminal, err := terminalEvent.Terminal()
	if err != nil {
		t.Fatal(err)
	}
	if err := setChatRuntimeStatus(ctx, stateStore, conversationID, historyID, "generating", "answer", "run-resume", nil); err != nil {
		t.Fatal(err)
	}
	if err := appendChatChunk(ctx, stateStore, conversationID, historyID, &ChatChunkResponse{
		ConversationID: conversationID, HistoryID: historyID, Delta: "answer", RuntimeEvent: terminalEvent,
	}); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		resumeSingleAnswerChat(ctx, stateStore, conversationID, historyID, recorder, recorder)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := setChatRuntimeStatus(ctx, stateStore, conversationID, historyID, "completed", "answer", "run-resume", terminal); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("resume did not finish")
	}
	if count := strings.Count(recorder.Body.String(), `"type":"run_finished"`); count != 1 {
		t.Fatalf("run_finished count=%d, body=%s", count, recorder.Body.String())
	}
}
