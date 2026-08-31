package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lazymind/core/log"
	"lazymind/core/state"
)

const (
	runDecisionKeyPrefix = "rag/chat/run-decision:%s:%s:%s"
	runDecisionTTL       = 15 * time.Minute

	runDecisionUserCancel = "user_cancel"
	runDecisionTerminal   = "terminal"
)

type runDecision struct {
	Kind        string       `json:"kind"`
	Terminal    *RunTerminal `json:"terminal,omitempty"`
	Source      string       `json:"source"`
	DecidedAtMS int64        `json:"decided_at_ms"`
}

func runDecisionKey(conversationID, historyID, runID string) string {
	return fmt.Sprintf(runDecisionKeyPrefix, conversationID, historyID, runID)
}

func claimRunDecision(
	ctx context.Context,
	stateStore state.Store,
	conversationID, historyID, runID string,
	candidate runDecision,
) (runDecision, bool, error) {
	if stateStore == nil || strings.TrimSpace(conversationID) == "" ||
		strings.TrimSpace(historyID) == "" || strings.TrimSpace(runID) == "" {
		return candidate, true, nil
	}
	candidate.DecidedAtMS = time.Now().UnixMilli()
	payload, err := json.Marshal(candidate)
	if err != nil {
		return runDecision{}, false, err
	}
	key := runDecisionKey(conversationID, historyID, runID)
	won, err := stateStore.SetNX(ctx, key, payload, runDecisionTTL)
	if err != nil {
		return runDecision{}, false, err
	}
	if won {
		logRunDecision("chat run decision accepted", conversationID, historyID, runID, candidate, "")
		return candidate, true, nil
	}
	existingPayload, err := stateStore.Get(ctx, key)
	if err != nil {
		return runDecision{}, false, err
	}
	var existing runDecision
	if err := json.Unmarshal(existingPayload, &existing); err != nil {
		return runDecision{}, false, err
	}
	logRunDecision("chat run decision ignored", conversationID, historyID, runID, existing, candidate.Source)
	return existing, false, nil
}

func logRunDecision(message, conversationID, historyID, runID string, winner runDecision, ignoredSource string) {
	event := log.Logger.Info().
		Str("conversation_id", conversationID).
		Str("history_id", historyID).
		Str("run_id", runID).
		Str("decision_kind", winner.Kind).
		Str("decision_source", winner.Source)
	if winner.Terminal != nil {
		event = event.
			Str("terminal_status", winner.Terminal.Status).
			Str("terminal_reason", winner.Terminal.Reason).
			Str("terminal_code", winner.Terminal.Code)
	}
	if ignoredSource != "" {
		event = event.Str("ignored_source", ignoredSource)
	}
	event.Msg(message)
}

func claimUserCancelDecision(
	ctx context.Context,
	stateStore state.Store,
	conversationID, historyID, runID string,
) (bool, error) {
	winner, _, err := claimRunDecision(ctx, stateStore, conversationID, historyID, runID, runDecision{
		Kind:   runDecisionUserCancel,
		Source: "user_stop",
	})
	return err == nil && winner.Kind == runDecisionUserCancel, err
}

func resolveRunTerminal(
	ctx context.Context,
	stateStore state.Store,
	conversationID, historyID, runID string,
	candidate *RunTerminal,
	partialOutput bool,
	source string,
) *RunTerminal {
	if candidate == nil {
		fallback := RunTerminal{
			Status: "failed", Reason: "runtime_failure", Code: "missing_run_terminal",
			PartialOutput: partialOutput,
		}
		candidate = &fallback
	}
	winner, _, err := claimRunDecision(ctx, stateStore, conversationID, historyID, runID, runDecision{
		Kind:     runDecisionTerminal,
		Terminal: candidate,
		Source:   source,
	})
	if err != nil {
		log.Logger.Warn().Err(err).
			Str("conversation_id", conversationID).
			Str("history_id", historyID).
			Str("run_id", runID).
			Str("decision_source", source).
			Msg("chat run decision state unavailable; using candidate terminal")
		return candidate
	}
	if winner.Kind == runDecisionUserCancel {
		return &RunTerminal{
			Status: "cancelled", Reason: "user_cancelled", PartialOutput: partialOutput,
		}
	}
	if winner.Kind == runDecisionTerminal && winner.Terminal != nil {
		return winner.Terminal
	}
	return candidate
}
