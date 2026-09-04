package chat

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/state"
)

func decodeChatChunkSSE(t *testing.T, body string) []ChatChunkResponse {
	t.Helper()
	chunks := make([]ChatChunkResponse, 0)
	for _, frame := range strings.Split(body, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		if !strings.HasPrefix(frame, "data: ") {
			t.Fatalf("unexpected SSE frame %q", frame)
		}
		var envelope struct {
			Result ChatChunkResponse `json:"result"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "data: ")), &envelope); err != nil {
			t.Fatalf("decode SSE frame %q: %v", frame, err)
		}
		chunks = append(chunks, envelope.Result)
	}
	return chunks
}

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
	chunks := decodeChatChunkSSE(t, recorder.Body.String())
	if len(chunks) == 0 || chunks[0].Delta != "answer" || chunks[0].DeltaMode != ChatDeltaModeReplace {
		t.Fatalf("first resumed chunk = %#v, want full answer with replace mode", chunks)
	}
}

func TestResumeMultiAnswerStartsEachHistoryWithEmptyReplace(t *testing.T) {
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })

	ctx := context.Background()
	const conversationID = "conversation-multi-resume"
	const primaryHistoryID = "history-primary"
	const secondaryHistoryID = "history-secondary"
	for _, chunk := range []*ChatChunkResponse{
		{ConversationID: conversationID, Seq: 1, HistoryID: primaryHistoryID, Delta: "primary"},
		{ConversationID: conversationID, Seq: 1, HistoryID: secondaryHistoryID, Delta: "secondary"},
	} {
		if err := appendChatChunk(ctx, stateStore, conversationID, chunk.HistoryID, chunk); err != nil {
			t.Fatal(err)
		}
		if err := setChatRuntimeStatus(ctx, stateStore, conversationID, chunk.HistoryID, "completed", chunk.Delta, "", nil); err != nil {
			t.Fatal(err)
		}
	}

	recorder := httptest.NewRecorder()
	resumeMultiAnswerChat(ctx, stateStore, conversationID, &MultiAnswerInfo{
		PrimaryHistoryID: primaryHistoryID, SecondaryHistoryID: secondaryHistoryID, Seq: 1,
	}, recorder, recorder)

	chunks := decodeChatChunkSSE(t, recorder.Body.String())
	if len(chunks) != 4 {
		t.Fatalf("chunk count=%d, want 4: %#v", len(chunks), chunks)
	}
	for index, historyID := range []string{primaryHistoryID, secondaryHistoryID} {
		chunk := chunks[index]
		if chunk.HistoryID != historyID || chunk.Delta != "" || chunk.DeltaMode != ChatDeltaModeReplace {
			t.Fatalf("announcement %d = %#v, want empty replace for %s", index, chunk, historyID)
		}
	}
	if chunks[2].HistoryID != primaryHistoryID || chunks[2].Delta != "primary" || chunks[2].DeltaMode != "" {
		t.Fatalf("primary replay = %#v, want implicit append", chunks[2])
	}
	if chunks[3].HistoryID != secondaryHistoryID || chunks[3].Delta != "secondary" || chunks[3].DeltaMode != "" {
		t.Fatalf("secondary replay = %#v, want implicit append", chunks[3])
	}
}

func TestResumeSingleAnswerUsesReplaceWhenOnlyFullStatusExists(t *testing.T) {
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })

	ctx := context.Background()
	terminalEvent := completedRunEvent("run-status-only", true)
	terminal, err := terminalEvent.Terminal()
	if err != nil {
		t.Fatal(err)
	}
	if err := setChatRuntimeStatus(
		ctx, stateStore, "conversation-status-only", "history-status-only",
		"completed", "full status result", "run-status-only", terminal,
	); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	resumeSingleAnswerChat(
		ctx, stateStore, "conversation-status-only", "history-status-only", recorder, recorder,
	)
	chunks := decodeChatChunkSSE(t, recorder.Body.String())
	if len(chunks) < 2 || chunks[0].HistoryID != "history-status-only" || chunks[0].Delta != "" || chunks[0].DeltaMode != ChatDeltaModeReplace {
		t.Fatalf("first resumed chunk = %#v, want empty replace baseline", chunks)
	}
	if chunks[1].Delta != "full status result" || chunks[1].DeltaMode != ChatDeltaModeAppend {
		t.Fatalf("status result chunk = %#v, want full status result appended after baseline", chunks[1])
	}
}

func TestDatabaseFullResumeUsesReplaceMode(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ChatHistory{}, &orm.ChatRunPerformance{})
	if err := db.Create(&orm.ChatHistory{
		ID: "history-db-resume", ConversationID: "conversation-db-resume", Seq: 1, Result: "full result",
		RunID: "run-db-resume", RunStatus: "completed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := persistRunPerformance(context.Background(), db.DB, runPerformanceRecord{
		RunID: "run-db-resume", ConversationID: "conversation-db-resume", HistoryID: "history-db-resume",
		UserID: "user-db-resume", Status: "completed",
		Metrics: &RunPerformanceMetrics{SchemaVersion: 1, Steps: 1, ModelSteps: 1, ModelMS: metricInt64(250)},
	}); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		resume func(*httptest.ResponseRecorder)
	}{
		{name: "without state store", resume: func(recorder *httptest.ResponseRecorder) {
			resumeFromDBOnly(context.Background(), db.DB, "user-db-resume", "conversation-db-resume", recorder, recorder)
		}},
		{name: "completed", resume: func(recorder *httptest.ResponseRecorder) {
			resumeCompletedFromDB(context.Background(), db.DB, "user-db-resume", "conversation-db-resume", recorder, recorder)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.resume(recorder)
			chunks := decodeChatChunkSSE(t, recorder.Body.String())
			if len(chunks) == 0 || chunks[0].Delta != "full result" || chunks[0].DeltaMode != ChatDeltaModeReplace {
				t.Fatalf("first resumed chunk = %#v, want full result with replace mode", chunks)
			}
			if len(chunks) < 2 || chunks[1].PerformanceMetrics == nil || chunks[1].PerformanceMetrics.ModelMS == nil ||
				*chunks[1].PerformanceMetrics.ModelMS != 250 {
				t.Fatalf("terminal resume did not restore performance metrics: %#v", chunks)
			}
		})
	}
}
