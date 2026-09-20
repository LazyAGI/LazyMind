package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func terminalTransportCode(t *testing.T, terminal *RunTerminal) string {
	t.Helper()
	var data struct {
		Diagnostic *struct {
			Code string `json:"code"`
		} `json:"transport_diagnostic"`
	}
	if err := json.Unmarshal(terminalJSON(terminal), &data); err != nil {
		t.Fatal(err)
	}
	if data.Diagnostic == nil {
		return ""
	}
	return data.Diagnostic.Code
}

func TestRunDecisionAcceptedSuccessRejectsLateFailureFields(t *testing.T) {
	ctx := context.Background()
	stateStore := newRunDecisionTestStore(t)
	accepted := &RunTerminal{Status: "completed", Reason: "normal", PartialOutput: true}
	resolveRunTerminal(ctx, stateStore, "conversation", "history", "run", accepted, "accepted")
	late := &RunTerminal{
		Status: "failed", Reason: "runtime_failure", Code: "upstream_stream_failed",
		PartialOutput: true, ModelCallID: "late-call", DiagnosticID: "late-diagnostic",
	}
	originalLate := *late
	winner := resolveRunTerminal(ctx, stateStore, "conversation", "history", "run", late, "late_failure")
	if !reflect.DeepEqual(winner, accepted) {
		t.Errorf("accepted terminal inherited losing fields: got=%+v want=%+v", winner, accepted)
	}
	if !reflect.DeepEqual(*late, originalLate) {
		t.Errorf("resolving the winner mutated the losing candidate: got=%+v want=%+v", late, originalLate)
	}
	if _, err := parseRunTerminal(terminalJSON(winner)); err != nil {
		t.Errorf("winning terminal is no longer valid: %v", err)
	}
}

// These synthetic streams model closure ordering, not any historical Skill run.
func TestStreamTerminalTransportReconciliation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		terminal     *RunTerminal
		closure      string
		disconnected bool
		accepted     bool
		cancelled    bool
		wrongRun     bool
		wantStatus   string
		wantCode     string
		wantDiag     string
	}{
		{name: "completed abnormal EOF", terminal: &RunTerminal{Status: "completed", Reason: "normal"}, closure: "truncated", wantStatus: "completed", wantDiag: "upstream_stream_failed"},
		{name: "accepted success abnormal EOF", terminal: &RunTerminal{Status: "completed", Reason: "normal"}, closure: "truncated", accepted: true, wantStatus: "completed", wantDiag: "upstream_stream_failed"},
		{name: "committed success then transport error", closure: "truncated", accepted: true, wantStatus: "completed", wantDiag: "upstream_stream_failed"},
		{name: "partial output abnormal EOF", closure: "truncated", wantStatus: "failed", wantCode: "upstream_stream_failed", wantDiag: "upstream_stream_failed"},
		{name: "artifact before parent failure", terminal: &RunTerminal{Status: "failed", Reason: "runtime_failure", Code: "parent_execution_failed"}, closure: "truncated", wantStatus: "failed", wantCode: "parent_execution_failed", wantDiag: "upstream_stream_failed"},
		{name: "disconnected drain success", terminal: &RunTerminal{Status: "completed", Reason: "normal"}, closure: "truncated", disconnected: true, wantStatus: "completed", wantDiag: "upstream_stream_failed"},
		{name: "completed normal EOF", terminal: &RunTerminal{Status: "completed", Reason: "normal"}, wantStatus: "completed"},
		{name: "partial normal EOF", wantStatus: "failed", wantCode: "upstream_stream_failed"},
		{name: "timeout before terminal", closure: "timeout", wantStatus: "failed", wantCode: "upstream_stream_timeout", wantDiag: "upstream_stream_timeout"},
		{name: "timeout after terminal", terminal: &RunTerminal{Status: "completed", Reason: "normal"}, closure: "timeout", wantStatus: "completed", wantDiag: "upstream_stream_timeout"},
		{name: "committed success then timeout", closure: "timeout", accepted: true, wantStatus: "completed", wantDiag: "upstream_stream_timeout"},
		{name: "cancel wins over success and transport error", terminal: &RunTerminal{Status: "completed", Reason: "normal"}, closure: "truncated", cancelled: true, wantStatus: "cancelled", wantDiag: "upstream_stream_failed"},
		{name: "old run success cannot complete current run", terminal: &RunTerminal{Status: "completed", Reason: "normal"}, wrongRun: true, wantStatus: "failed", wantCode: "upstream_stream_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, stateStore := newRunDecisionStreamHarness(t)
			if err := db.AutoMigrate(&orm.ConversationArtifact{}); err != nil {
				t.Fatal(err)
			}
			const convID, historyID, runID = "conv-stream-decision", "history-transport", "run-transport"
			const artifactID = "fdd99970-d180-478c-8d4c-6d96dd0e4ec5"
			const answer = "Final answer with [result.txt](file_id:" + artifactID + ")"
			frames := []string{
				algorithmFrame(t, map[string]any{"text": answer}),
				algorithmFrame(t, map[string]any{"artifact_created": &ArtifactCreatedEvent{
					ArtifactID: artifactID, Filename: "result.txt", ContentType: "text",
					Value: json.RawMessage(`{"text":"preserved result"}`),
				}}),
			}
			if tc.terminal != nil {
				tc.terminal.PartialOutput = true
				eventRunID := runID
				if tc.wrongRun {
					eventRunID = "previous-run"
				}
				frames = append(frames, algorithmFrame(t, map[string]any{"runtime_event": runFinishedEvent(eventRunID, *tc.terminal)}))
			}
			payload := strings.Join(frames, "\n") + "\n"
			if tc.closure == "timeout" {
				t.Setenv("LAZYMIND_CHAT_UPSTREAM_TIMEOUT_SEC", "1")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.closure == "truncated" {
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)+16))
				}
				_, _ = fmt.Fprint(w, payload)
				if tc.closure == "timeout" {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			ctx := context.Background()
			if err := setChatRuntimeStatus(ctx, stateStore, convID, historyID, "generating", "", runID, nil); err != nil {
				t.Fatal(err)
			}
			if tc.accepted {
				completed := &RunTerminal{Status: "completed", Reason: "normal", PartialOutput: true}
				resolveRunTerminal(ctx, stateStore, convID, historyID, runID, completed, "accepted_terminal")
				if err := db.Create(&orm.ChatHistory{
					ID: historyID, ConversationID: convID, Seq: 1, Result: answer,
					RunID: runID, RunStatus: completed.Status, RunTerminal: terminalJSON(completed),
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.cancelled {
				if won, err := claimUserCancelDecision(ctx, stateStore, convID, historyID, runID); err != nil || !won {
					t.Fatalf("cancel: won=%v err=%v", won, err)
				}
			}
			reqCtx, disconnect := context.WithCancel(ctx)
			defer disconnect()
			if tc.disconnected {
				disconnect()
			}
			recorder := httptest.NewRecorder()
			streamSingleAnswer(ctx, reqCtx, recorder, recorder, db, stateStore, server.URL,
				map[string]any{"run_id": runID, "query": "question", "user_id": "user-1"},
				convID, "question", historyID, chatPersistTarget{HistoryID: historyID, Seq: 1}, json.RawMessage(`{}`))

			var history orm.ChatHistory
			if err := db.Where("id = ?", historyID).Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			terminal, err := parseRunTerminal(history.RunTerminal)
			if err != nil {
				t.Fatal(err)
			}
			if history.RunID != runID || history.Result != answer || history.RunStatus != tc.wantStatus || terminal.Status != tc.wantStatus || terminal.Code != tc.wantCode || !terminal.PartialOutput {
				t.Fatalf("unexpected persisted result: history=%+v terminal=%+v", history, terminal)
			}
			if got := terminalTransportCode(t, terminal); got != tc.wantDiag {
				t.Errorf("transport diagnostic = %q, want %q", got, tc.wantDiag)
			}
			status, err := getChatStatus(ctx, stateStore, convID, historyID)
			if err != nil || status.RunID != runID || status.Status != history.RunStatus || !reflect.DeepEqual(status.RunTerminal, terminal) {
				t.Fatalf("history/status mismatch: status=%+v terminal=%+v err=%v", status, terminal, err)
			}
			var artifact orm.ConversationArtifact
			if err := db.Where("id = ?", artifactID).Take(&artifact).Error; err != nil {
				t.Fatal(err)
			}
			if artifact.ConversationID != convID || artifact.HistoryID != historyID || artifact.CreateUserID != "user-1" {
				t.Fatalf("artifact not owned by parent turn: %+v", artifact)
			}
			assertStoredArtifactValue(t, artifact.Value, `{"text":"preserved result"}`)
			chunks, err := getChatChunks(ctx, stateStore, convID, historyID)
			if err != nil {
				t.Fatal(err)
			}
			terminals := 0
			for _, chunk := range chunks {
				if chunk.RuntimeEvent == nil || chunk.RuntimeEvent.Type != RuntimeEventRunFinished {
					continue
				}
				terminals++
				streamTerminal, err := chunk.RuntimeEvent.Terminal()
				if err != nil || chunk.RuntimeEvent.RunID != runID || !reflect.DeepEqual(streamTerminal, terminal) {
					t.Fatalf("history/SSE mismatch: event=%+v terminal=%+v err=%v", chunk.RuntimeEvent, terminal, err)
				}
			}
			if terminals != 1 {
				t.Fatalf("terminal frame count = %d, want 1", terminals)
			}
			if !tc.disconnected && !strings.Contains(recorder.Body.String(), string(terminalJSON(terminal))) {
				t.Fatalf("live SSE does not contain persisted terminal: %s", recorder.Body.String())
			}
			if tc.wantStatus == "completed" {
				if won, err := claimUserCancelDecision(ctx, stateStore, convID, historyID, runID); err != nil || won {
					t.Fatalf("late cancellation changed accepted success: won=%v err=%v", won, err)
				}
			}
		})
	}
}

func TestStoredTerminalTransportDiagnostic(t *testing.T) {
	for _, code := range []string{"upstream_stream_failed", "upstream_stream_timeout"} {
		t.Run(code, func(t *testing.T) {
			raw := json.RawMessage(fmt.Sprintf(`{"status":"completed","reason":"normal","partial_output":true,"transport_diagnostic":{"code":%q,"message":"private upstream details"}}`, code))
			event := storedRunEvent("current-run", raw)
			if err := event.Validate("current-run"); err != nil {
				t.Fatal(err)
			}
			terminal, err := event.Terminal()
			if err != nil || terminal.Status != "completed" || terminalTransportCode(t, terminal) != code {
				t.Fatalf("diagnostic lost during replay: event=%+v terminal=%+v err=%v", event, terminal, err)
			}
			if strings.Contains(string(event.Data), "private upstream details") {
				t.Fatalf("transport details exposed: %s", event.Data)
			}
		})
	}
	for _, diagnostic := range []string{`{"code":"unknown"}`, `{"code":123}`, `{}`, `"upstream_stream_failed"`} {
		raw := json.RawMessage(`{"status":"completed","reason":"normal","partial_output":true,"transport_diagnostic":` + diagnostic + `}`)
		if terminal, err := parseRunTerminal(raw); err == nil {
			t.Errorf("invalid diagnostic accepted: %+v", terminal)
		}
	}
}
