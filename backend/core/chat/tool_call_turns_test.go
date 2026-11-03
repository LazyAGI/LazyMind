package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
)

func requireToolCallTurns(t *testing.T, chunk *ChatChunkResponse, want int) {
	t.Helper()
	if chunk == nil || chunk.ToolCallTurns == nil || *chunk.ToolCallTurns != want {
		t.Fatalf("expected explicit tool_call_turns=%d, got %+v", want, chunk)
	}
}

func requireTerminalToolCallTurns(t *testing.T, chunks []ChatChunkResponse, historyID string, want int) {
	t.Helper()
	terminals := 0
	for _, chunk := range chunks {
		if chunk.HistoryID == historyID && chunk.RuntimeEvent != nil && chunk.RuntimeEvent.Type == RuntimeEventRunFinished {
			requireToolCallTurns(t, &chunk, want)
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("expected one terminal for %s, got %d", historyID, terminals)
	}
}

func TestToolCallTurnsLiveReplayAndHistoryAgree(t *testing.T) {
	for _, mode := range []string{"stream", "disconnected", "nonstream"} {
		for _, turns := range []int{0, 8} {
			t.Run(fmt.Sprintf("%s/%d", mode, turns), func(t *testing.T) {
				db, stateStore := newRunDecisionStreamHarness(t)
				const runID, historyID, convID = "count-run", "count-history", "conv-stream-decision"
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request LazyChatRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					fmt.Fprintln(w, algorithmFrame(t, map[string]any{"text": "answer"}))
					fmt.Fprintln(w, algorithmFrame(t, map[string]any{"runtime_event": completedRunEvent(request.Conversation.RunID, true), "tool_call_turns": turns}))
				}))
				defer server.Close()
				body := map[string]any{"query": "q", "run_id": runID}
				target := chatPersistTarget{HistoryID: historyID, Seq: 1}
				recorder := httptest.NewRecorder()
				if mode == "nonstream" {
					handleNonStreamChat(recorder, t.Context(), db, stateStore, server.URL, body, convID, "q", target, nil)
					var response struct {
						Data ChatChunkResponse `json:"data"`
					}
					if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					requireToolCallTurns(t, &response.Data, turns)
				} else {
					reqCtx := t.Context()
					if mode == "disconnected" {
						var cancel context.CancelFunc
						reqCtx, cancel = context.WithCancel(reqCtx)
						cancel()
					}
					streamSingleAnswer(t.Context(), reqCtx, recorder, recorder, db, stateStore, server.URL, body,
						convID, "q", historyID, target, nil)
					if mode == "stream" {
						requireTerminalToolCallTurns(t, decodeChatChunkSSE(t, recorder.Body.String()), historyID, turns)
					}
					resume := httptest.NewRecorder()
					resumeSingleAnswerChat(t.Context(), stateStore, convID, historyID, resume, resume)
					requireToolCallTurns(t, &decodeChatChunkSSE(t, resume.Body.String())[0], turns)
				}
				var history orm.ChatHistory
				if err := db.First(&history, "id = ?", historyID).Error; err != nil {
					t.Fatal(err)
				}
				if history.ToolCallTurns != turns {
					t.Fatalf("stored count=%d, want %d", history.ToolCallTurns, turns)
				}
				cold := httptest.NewRecorder()
				resumeFromDBOnly(t.Context(), db, "user-1", convID, cold, cold)
				requireToolCallTurns(t, &decodeChatChunkSSE(t, cold.Body.String())[0], turns)
			})
		}
	}
}

func TestToolCallTurnsReplayDoesNotSumOrRestorePriorRun(t *testing.T) {
	eight, zero := 8, 0
	chunks := []*ChatChunkResponse{
		{ModelOutput: &ChatModelOutput{RunID: "old", CallIndex: 0, Phase: "commit"}, ToolCallTurns: &eight},
		{RuntimeEvent: completedRunEvent("old", true), ToolCallTurns: &eight},
		{}, // An absent count must not reset the observed count.
	}
	requireToolCallTurns(t, mergeChunksToFirstChunk(chunks), 8)
	chunks = append(chunks,
		&ChatChunkResponse{ModelOutput: &ChatModelOutput{RunID: "new", CallIndex: 0, Phase: "commit"}},
		&ChatChunkResponse{RuntimeEvent: completedRunEvent("new", true), ToolCallTurns: &zero},
	)
	requireToolCallTurns(t, mergeChunksToFirstChunk(chunks), 0)
}

func TestToolCallTurnsDualAnswerSurvivesDisconnect(t *testing.T) {
	db, stateStore := newRunDecisionStreamHarness(t)
	if err := db.AutoMigrate(&orm.MultiAnswersChatHistory{}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request LazyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		turns := 3
		if request.Conversation.RunID == "first-run" {
			turns = 8
		}
		fmt.Fprintln(w, algorithmFrame(t, map[string]any{"text": "answer"}))
		fmt.Fprintln(w, algorithmFrame(t, map[string]any{"runtime_event": completedRunEvent(request.Conversation.RunID, true), "tool_call_turns": turns}))
	}))
	defer server.Close()
	reqCtx, cancel := context.WithCancel(t.Context())
	cancel()
	recorder := httptest.NewRecorder()
	streamDualAnswer(t.Context(), reqCtx, recorder, recorder, db, stateStore, server.URL,
		map[string]any{"query": "q", "run_id": "first-run", "secondary_run_id": "second-run"},
		"conv-stream-decision", "q", "first", "second", chatPersistTarget{Seq: 1}, nil)
	for id, want := range map[string]int{"first": 8, "second": 3} {
		var history orm.MultiAnswersChatHistory
		if err := db.First(&history, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if history.ToolCallTurns != want {
			t.Fatalf("candidate %s count=%d, want %d", id, history.ToolCallTurns, want)
		}
		chunks, err := getChatChunks(t.Context(), stateStore, "conv-stream-decision", id)
		if err != nil {
			t.Fatal(err)
		}
		requireToolCallTurns(t, mergeChunksToFirstChunk(chunks), want)
	}
}
