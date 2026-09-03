package chat

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/state"
)

func newRunDecisionStreamHarness(t *testing.T) (*gorm.DB, state.Store) {
	t.Helper()
	database, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatalf("connect chat database: %v", err)
	}
	if err := database.AutoMigrate(&orm.Conversation{}, &orm.ChatHistory{}, &orm.TaskCenterTask{}); err != nil {
		t.Fatalf("migrate chat database: %v", err)
	}
	now := time.Now().UTC()
	if err := database.Create(&orm.Conversation{
		ID: "conv-stream-decision",
		BaseModel: orm.BaseModel{
			CreateUserID: "user-1", CreateUserName: "user-1", CreatedAt: now, UpdatedAt: now,
		},
	}).Error; err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	return database.DB, stateStore
}

func TestStreamSingleAnswerUserCancelWinsOverCancellationEOF(t *testing.T) {
	db, stateStore := newRunDecisionStreamHarness(t)
	const historyID = "history-cancel-first"
	const runID = "run-cancel-first"
	server := streamServer(t, runID, algorithmFrame(t, map[string]any{"text": "partial answer"}))
	defer server.Close()

	if won, err := claimUserCancelDecision(
		context.Background(), stateStore, "conv-stream-decision", historyID, runID,
	); err != nil || !won {
		t.Fatalf("claim cancellation: won=%v err=%v", won, err)
	}
	recorder := httptest.NewRecorder()
	streamSingleAnswer(
		context.Background(), context.Background(), recorder, recorder, db, stateStore,
		server.URL, map[string]any{"query": "question", "run_id": runID},
		"conv-stream-decision", "question", historyID,
		chatPersistTarget{HistoryID: historyID, Seq: 1}, json.RawMessage(`{}`),
	)

	var history orm.ChatHistory
	if err := db.Where("id = ?", historyID).Take(&history).Error; err != nil {
		t.Fatalf("load cancelled history: %v", err)
	}
	terminal, err := parseRunTerminal(history.RunTerminal)
	if err != nil {
		t.Fatalf("parse cancelled terminal: %v", err)
	}
	if history.Result != "partial answer" || history.RunStatus != "cancelled" ||
		terminal.Reason != "user_cancelled" || !terminal.PartialOutput {
		t.Fatalf("unexpected cancelled history: history=%#v terminal=%#v", history, terminal)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"status":"cancelled"`) || strings.Contains(body, `"status":"failed"`) {
		t.Fatalf("unexpected cancellation stream: %s", body)
	}
}

func TestStreamSingleAnswerAcceptedModelFailureWinsOverLateStop(t *testing.T) {
	db, stateStore := newRunDecisionStreamHarness(t)
	const historyID = "history-failure-first"
	const runID = "run-failure-first"
	failureFrame := algorithmFrame(t, map[string]any{"runtime_event": map[string]any{
		"schema_version": 1,
		"event_id":       "evt-model-failure",
		"run_id":         runID,
		"type":           RuntimeEventRunFinished,
		"data": map[string]any{
			"status": "failed", "reason": "model_failure", "code": "rate_limited", "partial_output": false,
		},
	}})
	server := streamServer(t, runID, failureFrame)
	defer server.Close()

	recorder := httptest.NewRecorder()
	streamSingleAnswer(
		context.Background(), context.Background(), recorder, recorder, db, stateStore,
		server.URL, map[string]any{"query": "question", "run_id": runID},
		"conv-stream-decision", "question", historyID,
		chatPersistTarget{HistoryID: historyID, Seq: 1}, json.RawMessage(`{}`),
	)
	if won, err := claimUserCancelDecision(
		context.Background(), stateStore, "conv-stream-decision", historyID, runID,
	); err != nil || won {
		t.Fatalf("late cancellation: won=%v err=%v", won, err)
	}

	var history orm.ChatHistory
	if err := db.Where("id = ?", historyID).Take(&history).Error; err != nil {
		t.Fatalf("load failed history: %v", err)
	}
	terminal, err := parseRunTerminal(history.RunTerminal)
	if err != nil {
		t.Fatalf("parse failed terminal: %v", err)
	}
	if history.RunStatus != "failed" || terminal.Reason != "model_failure" || terminal.Code != "rate_limited" {
		t.Fatalf("late stop overwrote model failure: history=%#v terminal=%#v", history, terminal)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"status":"failed"`) || strings.Contains(body, `"status":"cancelled"`) {
		t.Fatalf("unexpected failure stream: %s", body)
	}
}
