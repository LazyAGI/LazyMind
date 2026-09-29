package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"lazymind/core/common/orm"
)

func TestDualTerminalTransportReconciliation(t *testing.T) {
	for _, disconnected := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnected=%v", disconnected), func(t *testing.T) {
			db, stateStore := newRunDecisionStreamHarness(t)
			if err := db.AutoMigrate(&orm.MultiAnswersChatHistory{}); err != nil {
				t.Fatal(err)
			}
			const convID = "conv-stream-decision"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req LazyChatRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				runID := req.Conversation.RunID
				payload := algorithmFrame(t, map[string]any{"text": runID}) + "\n" + runFinishedFrame(t, runID) + "\n"
				if runID == "primary-run" {
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)+16))
				}
				_, _ = fmt.Fprint(w, payload)
			}))
			defer server.Close()
			ctx := context.Background()
			reqCtx, disconnect := context.WithCancel(ctx)
			defer disconnect()
			if disconnected {
				disconnect()
			}
			recorder := httptest.NewRecorder()
			streamDualAnswer(ctx, reqCtx, recorder, recorder, db, stateStore, server.URL,
				map[string]any{"run_id": "primary-run", "secondary_run_id": "secondary-run"},
				convID, "question", "primary-history", "secondary-history",
				chatPersistTarget{HistoryID: "primary-history", Seq: 1}, json.RawMessage(`{}`))
			for _, prefix := range []string{"primary", "secondary"} {
				runID, historyID := prefix+"-run", prefix+"-history"
				var history orm.MultiAnswersChatHistory
				if err := db.Where("id = ?", historyID).Take(&history).Error; err != nil {
					t.Fatal(err)
				}
				terminal, err := parseRunTerminal(history.RunTerminal)
				if err != nil || history.RunID != runID || history.Result != runID || history.RunStatus != "completed" || terminal.Status != "completed" {
					t.Fatalf("unexpected dual history: history=%+v terminal=%+v err=%v", history, terminal, err)
				}
				wantDiag := ""
				if prefix == "primary" {
					wantDiag = "upstream_stream_failed"
				}
				if got := terminalTransportCode(t, terminal); got != wantDiag {
					t.Errorf("%s diagnostic = %q, want %q", runID, got, wantDiag)
				}
				status, err := getChatStatus(ctx, stateStore, convID, historyID)
				if err != nil || status.RunID != runID || !reflect.DeepEqual(status.RunTerminal, terminal) {
					t.Fatalf("dual status/history mismatch: status=%+v terminal=%+v err=%v", status, terminal, err)
				}
				chunks, err := getChatChunks(ctx, stateStore, convID, historyID)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, chunk := range chunks {
					if chunk.RuntimeEvent == nil {
						continue
					}
					count++
					streamTerminal, err := chunk.RuntimeEvent.Terminal()
					if err != nil || chunk.RuntimeEvent.RunID != runID || !reflect.DeepEqual(streamTerminal, terminal) {
						t.Fatalf("dual SSE/history mismatch: event=%+v terminal=%+v err=%v", chunk.RuntimeEvent, terminal, err)
					}
				}
				if count != 1 {
					t.Fatalf("%s terminal frame count = %d, want 1", runID, count)
				}
			}
		})
	}
}
