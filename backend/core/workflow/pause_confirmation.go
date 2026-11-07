package workflow

import (
	"context"
	"encoding/json"
	"net/http"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/runstate"
)

// PauseNativeWorkflowHTTP pauses only the selected native session, never its
// containing Chat. Use the same executor acknowledgement path as Chat pause.
func PauseNativeWorkflowHTTP(w http.ResponseWriter, r *http.Request) {
	if !IsWorkflowUserControlRequest(r) {
		writeWorkflowControlError(w, controlstore.Reject("PERMISSION_DENIED", "pause requires an authenticated browser request"))
		return
	}
	db := store.DB()
	var session orm.WorkflowSession
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND dismissed = false",
		common.PathVar(r, "session_id"), r.Header.Get("X-User-Id")).First(&session).Error; err != nil {
		writeWorkflowControlError(w, err)
		return
	}
	if session.ControllerHost != "lazymind" || controlstore.Controlled(session) {
		writeWorkflowControlError(w, controlstore.Reject("CONTROL_PROTOCOL_REQUIRED", "use the workflow host controls for this session"))
		return
	}
	if err := stopWorkflowSession(r.Context(), db, store.State(), &session, ""); err != nil {
		writeWorkflowControlError(w, err)
		return
	}
	common.ReplyOK(w, nil)
}

// ConfirmNativeStop is a completion receipt, not a cancellation request. The
// worker calls it after its executor (including tools and cleanup) has exited.
// The old lease token identifies that exact execution even after it was fenced.
func ConfirmNativeStop(ctx context.Context, db *gorm.DB, attemptID, token string) error {
	var stoppedSessionID string
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		stoppedSessionID = ""
		var step orm.WorkflowSessionStep
		if err := tx.Where("id = ?", attemptID).First(&step).Error; err != nil {
			return err
		}
		var session orm.WorkflowSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", step.SessionID).First(&session).Error; err != nil {
			return err
		}
		if controlstore.Controlled(session) || session.ControllerHost != "lazymind" {
			return nil
		}
		// Re-read under the session lock, shared by stop and lease transitions.
		if err := tx.Where("id = ?", attemptID).First(&step).Error; err != nil {
			return err
		}
		if step.LeaseToken != token {
			return controlstore.Reject("LEASE_LOST", "stop receipt belongs to a different execution")
		}
		if err := tx.Model(&step).Where("terminal_code = ?", "WORKFLOW_STOP_REQUESTED").UpdateColumn("terminal_code", "WORKFLOW_STOPPED").Error; err != nil {
			return err
		}
		var pending int64
		if err := tx.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND terminal_code = ?", session.ID, "WORKFLOW_STOP_REQUESTED").Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 || session.Status != "pausing" {
			return nil
		}
		if err := runstate.Write(tx, &session, SessionStatusWaiting, map[string]any{"state_version": session.StateVersion + 1}); err != nil {
			return err
		}
		session.StateVersion++
		if err := appendSessionStateEvent(tx, session, "workflow.patch", json.RawMessage(`{"status":"waiting"}`)); err != nil {
			return err
		}
		stoppedSessionID = session.ID
		return nil
	})
	if err == nil && stoppedSessionID != "" {
		NotifyWorkflowRuntimeUpdated(ctx, db, stoppedSessionID, attemptID, "paused")
	}
	return err
}

func ConfirmNativeStopHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LeaseToken string `json:"lease_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.LeaseToken == "" {
		common.ReplyErr(w, "lease_token required", http.StatusBadRequest)
		return
	}
	if err := ConfirmNativeStop(r.Context(), store.DB(), common.PathVar(r, "attempt_id"), req.LeaseToken); err != nil {
		writeWorkflowControlError(w, err)
		return
	}
	common.ReplyOK(w, nil)
}
