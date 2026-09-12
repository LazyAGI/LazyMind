package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"lazymind/core/localworkspace"
	"lazymind/core/log"
	"lazymind/core/state"
	"lazymind/core/store"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

const (
	runDecisionKeyPrefix = "rag/chat/run-decision:%s:%s:%s"
	runDecisionTTL       = 24 * time.Hour
	terminalWriteTimeout = 5 * time.Second

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
	var winner runDecision
	var won bool
	err = withChatRunUpdate(ctx, conversationID, func() error {
		var claimErr error
		won, claimErr = stateStore.SetNX(ctx, key, payload, runDecisionTTL)
		if claimErr != nil {
			return claimErr
		}
		winner = candidate
		if !won {
			existing, err := stateStore.Get(ctx, key)
			if err != nil {
				return err
			}
			return json.Unmarshal(existing, &winner)
		}
		return nil
	})
	if err != nil {
		return runDecision{}, false, err
	}
	logRunDecision("chat run decision resolved", conversationID, historyID, runID, winner, candidate.Source)
	return winner, won, nil
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

// claimUserCancelDecision records the stop intent when possible and returns
// whether user cancellation is the authoritative winner, including retries
// after an earlier user-cancel claim.
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

// terminalWriteContext lets terminal state survive a disconnected request while
// keeping every detached write bounded. Callers must always invoke cancel.
func terminalWriteContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), terminalWriteTimeout)
}

func resolveRunTerminal(
	ctx context.Context,
	stateStore state.Store,
	conversationID, historyID, runID string,
	candidate *RunTerminal,
	source string,
) *RunTerminal {
	if candidate == nil {
		fallback := RunTerminal{
			Status: "failed", Reason: "runtime_failure", Code: "missing_run_terminal",
			PartialOutput: false,
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
			Status: "cancelled", Reason: "user_cancelled", PartialOutput: candidate.PartialOutput,
		}
	}
	if winner.Kind == runDecisionTerminal && winner.Terminal != nil {
		return winner.Terminal
	}
	return candidate
}

// ValidateWorkspaceRun uses the pre-dispatch run registration; a ChatHistory
// row need not exist yet. A cancellation/terminal decision always fences it.
func ValidateWorkspaceRun(ctx context.Context, stateStore state.Store, req localworkspace.OperationRequest) (*localworkspace.ContextSnapshot, error) {
	invalid := localworkspace.Error("binding_conflict", 409, "conflict")
	if stateStore == nil || req.RunID == "" || req.HistoryID == "" || req.TaskID != "" || req.Generation != "" {
		return nil, invalid
	}
	current, err := getChatStatus(ctx, stateStore, req.ConversationID, req.HistoryID)
	if err != nil {
		return nil, err
	}
	if current.RunID != req.RunID || current.Status != "generating" || current.RunTerminal != nil {
		return nil, invalid
	}
	decided, err := stateStore.Exists(ctx, runDecisionKey(req.ConversationID, req.HistoryID, req.RunID))
	if err != nil {
		return nil, err
	}
	if decided {
		return nil, invalid
	}
	input, err := getChatInput(ctx, stateStore, req.ConversationID, req.HistoryID)
	if err != nil || len(input.Ext) == 0 {
		return nil, invalid
	}
	ext := map[string]any{}
	if json.Unmarshal(input.Ext, &ext) != nil {
		return nil, invalid
	}
	snapshot := localworkspace.SnapshotFromMetadata(ext["workspace_context"])
	if snapshot == nil {
		return nil, invalid
	}
	if snapshot.WorkspaceID != req.WorkspaceID {
		return nil, invalid
	}
	return snapshot, nil
}

// Serialize state-backed run transitions with file commits using the existing
// conversation row. No expiring mutex can release a writer that is still live.
func withChatRunUpdate(ctx context.Context, conversationID string, update func() error) error {
	db := store.DB()
	if db == nil {
		return update()
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.Conversation{}).Where("id = ?", conversationID).
			UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		return update()
	})
}
