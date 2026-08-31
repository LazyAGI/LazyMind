package chat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/state"
)

func newRunDecisionTestStore(t *testing.T) state.Store {
	t.Helper()
	store, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestRunDecisionUserCancelWinsWhileRunIsActive(t *testing.T) {
	ctx := context.Background()
	store := newRunDecisionTestStore(t)
	won, err := claimUserCancelDecision(ctx, store, "conv", "history", "run-1")
	if err != nil || !won {
		t.Fatalf("claim cancellation: won=%v err=%v", won, err)
	}

	terminal := resolveRunTerminal(ctx, store, "conv", "history", "run-1", &RunTerminal{
		Status: "failed", Reason: "runtime_failure", Code: "upstream_stream_failed",
		PartialOutput: true,
	}, true, "upstream_terminal")

	if terminal.Status != "cancelled" || terminal.Reason != "user_cancelled" || !terminal.PartialOutput || terminal.Code != "" {
		t.Fatalf("unexpected cancellation terminal: %#v", terminal)
	}
}

func TestRunDecisionAcceptedFailureIsNotOverwrittenByStop(t *testing.T) {
	ctx := context.Background()
	store := newRunDecisionTestStore(t)
	failure := &RunTerminal{
		Status: "failed", Reason: "model_failure", Code: "rate_limited",
		PartialOutput: false,
	}
	accepted := resolveRunTerminal(
		ctx, store, "conv", "history", "run-1", failure, false, "upstream_terminal",
	)
	if accepted.Status != "failed" || accepted.Reason != "model_failure" {
		t.Fatalf("unexpected accepted terminal: %#v", accepted)
	}

	won, err := claimUserCancelDecision(ctx, store, "conv", "history", "run-1")
	if err != nil {
		t.Fatalf("claim late cancellation: %v", err)
	}
	if won {
		t.Fatal("late cancellation overwrote accepted failure")
	}

	resolved := resolveRunTerminal(ctx, store, "conv", "history", "run-1", &RunTerminal{
		Status: "cancelled", Reason: "user_cancelled", PartialOutput: true,
	}, true, "late_terminal")
	if *resolved != *failure {
		t.Fatalf("accepted failure changed: got %#v want %#v", resolved, failure)
	}
}

func TestRunDecisionRepeatedUserCancelIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newRunDecisionTestStore(t)
	for attempt := 0; attempt < 2; attempt++ {
		won, err := claimUserCancelDecision(ctx, store, "conv", "history", "run-1")
		if err != nil || !won {
			t.Fatalf("claim cancellation attempt %d: won=%v err=%v", attempt+1, won, err)
		}
	}

	terminal := resolveRunTerminal(ctx, store, "conv", "history", "run-1", &RunTerminal{
		Status: "completed", Reason: "normal", PartialOutput: true,
	}, true, "late_terminal")
	if terminal.Status != "cancelled" || terminal.Reason != "user_cancelled" {
		t.Fatalf("repeated cancellation changed the winning decision: %#v", terminal)
	}
}

func TestRunDecisionIsIsolatedByRunID(t *testing.T) {
	ctx := context.Background()
	store := newRunDecisionTestStore(t)
	if won, err := claimUserCancelDecision(ctx, store, "conv", "history", "run-1"); err != nil || !won {
		t.Fatalf("claim first run cancellation: won=%v err=%v", won, err)
	}

	second := resolveRunTerminal(ctx, store, "conv", "history", "run-2", &RunTerminal{
		Status: "failed", Reason: "runtime_failure", Code: "upstream_stream_failed",
		PartialOutput: false,
	}, false, "upstream_terminal")
	if second.Status != "failed" || second.Reason != "runtime_failure" {
		t.Fatalf("first run cancellation leaked into second run: %#v", second)
	}
}

func TestResolveRuntimeChunkDecisionRewritesEventToWinningCancellation(t *testing.T) {
	ctx := context.Background()
	store := newRunDecisionTestStore(t)
	if won, err := claimUserCancelDecision(ctx, store, "conv", "history", "run-1"); err != nil || !won {
		t.Fatalf("claim cancellation: won=%v err=%v", won, err)
	}
	candidateEvent := failedRunEvent("run-1", "upstream_stream_failed", true)
	candidate, _ := candidateEvent.Terminal()

	decision := resolveRuntimeChunkDecision(
		ctx, store, "conv", "history", "run-1",
		runtimeChunkDecision{Event: candidateEvent, Terminal: candidate, Stop: true}, true,
	)
	terminal, err := decision.Event.Terminal()
	if err != nil {
		t.Fatalf("parse resolved event: %v", err)
	}
	if terminal.Status != "cancelled" || decision.Terminal.Status != "cancelled" || !decision.Stop {
		t.Fatalf("unexpected resolved decision: %#v terminal=%#v", decision, terminal)
	}
}

func TestExternalTerminalProjectionPersistsCoreDecision(t *testing.T) {
	ctx := context.Background()
	app, db := newExternalChatTestApplication(t)
	createExternalChatTestRun(t, app, "run-external-decision")
	job, err := app.claim(ctx, "user-1", ChatExecutorCodex, "host-1")
	if err != nil || job == nil {
		t.Fatalf("claim external run: job=%#v err=%v", job, err)
	}
	if _, err := app.appendEvent(
		ctx, "user-1", job.RunID, "host-1", job.LeaseToken,
		externalChatEvent{EventID: "failed-1", Type: "failed", Error: "provider failed"},
	); err != nil {
		t.Fatalf("append external failure: %v", err)
	}

	store := newRunDecisionTestStore(t)
	if won, err := claimUserCancelDecision(
		ctx, store, "conversation-1", "history-run-external-decision", job.RunID,
	); err != nil || !won {
		t.Fatalf("claim cancellation: won=%v err=%v", won, err)
	}
	if err := projectExternalChatRunStatus(ctx, db, store, "user-1", job.RunID); err != nil {
		t.Fatalf("project external terminal: %v", err)
	}

	var history orm.ChatHistory
	if err := db.Where("id = ?", "history-run-external-decision").Take(&history).Error; err != nil {
		t.Fatalf("load projected history: %v", err)
	}
	var terminal RunTerminal
	if err := json.Unmarshal(history.RunTerminal, &terminal); err != nil {
		t.Fatalf("decode projected terminal: %v", err)
	}
	if history.RunStatus != "cancelled" || terminal.Status != "cancelled" || terminal.Reason != "user_cancelled" {
		t.Fatalf("external history did not persist Core decision: history=%#v terminal=%#v", history, terminal)
	}
}
