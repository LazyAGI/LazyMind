package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

type approvalPreferenceRequest struct {
	StepID           string `json:"step_id"`
	Scope            string `json:"scope"`
	ApprovalRequired bool   `json:"approval_required"`
}

// NativeControlCommand travels with the Chat request that actually continues
// execution. Ordinary chat text never grants approval or resume permission.
type NativeControlCommand struct {
	SessionID            string `json:"session_id"`
	Kind                 string `json:"kind"`
	StepID               string `json:"step_id"`
	ExpectedStateVersion int64  `json:"expected_state_version"`
	PreferenceScope      string `json:"preference_scope"`
	// Assigned by Chat after admission, never accepted from the browser.
	HistoryID string `json:"-"`
}

// Native controllers bind their current Chat turn separately from the immutable
// trigger history. A resumed workflow belongs to the newly admitted turn.
type nativeTurnBinding struct {
	HistoryID string `json:"chat_history_id"`
}

// BeginNativeControlTurn is called only after Chat has accepted a streaming run.
// An unsent/refused request cannot change the workflow's state.
func BeginNativeControlTurn(ctx context.Context, db *gorm.DB, owner, conversationID string, req NativeControlCommand) (int64, error) {
	var version int64
	if req.Kind != "approve" && req.Kind != "resume" && req.Kind != "retry" && req.Kind != "rewind" {
		return 0, controlstore.Reject("INVALID_COMMAND", "expected approve, resume, retry or rewind")
	}
	approval := req.Kind == "approve"
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session orm.WorkflowSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND create_user_id = ? AND conversation_id = ? AND dismissed = false", req.SessionID, owner, conversationID).First(&session).Error; err != nil {
			return err
		}
		if controlstore.Controlled(session) {
			return controlstore.Reject("CONTROL_PROTOCOL_REQUIRED", "use workflow control to confirm this checkpoint")
		}
		if session.StateVersion != req.ExpectedStateVersion {
			return controlstore.Reject("STATE_VERSION_CONFLICT", "workflow changed; reload before approving")
		}
		display, err := displaystate.Read(tx, session)
		if err != nil {
			return err
		}
		if approval && (display.Status != "waiting" || req.StepID == "" || display.ApprovalStepID != req.StepID) {
			return controlstore.Reject("INVALID_COMMAND", "no matching approval checkpoint")
		}
		if req.Kind == "resume" && (display.Status != "paused" || req.PreferenceScope != "") {
			return controlstore.Reject("INVALID_COMMAND", "no paused workflow to resume")
		}
		if req.Kind == "retry" && (display.Status != "failed" || req.PreferenceScope != "") {
			return controlstore.Reject("INVALID_COMMAND", "no failed workflow to retry")
		}
		if req.Kind == "rewind" {
			if (display.Status != "completed" && display.Status != "failed") || req.StepID == "" || req.PreferenceScope != "" {
				return controlstore.Reject("INVALID_COMMAND", "no completed or failed workflow to rewind")
			}
			projected, err := projectSession(ctx, tx, &session)
			if err != nil {
				return err
			}
			if !containsProjectionStep(projected.Projection.Rewindable, req.StepID) &&
				!(display.Status == "completed" && containsProjectionStep(projected.Projection.Continue, req.StepID)) {
				return controlstore.Reject("INVALID_COMMAND", "step is not available for rewind or completed continuation")
			}
		}
		if approval {
			var step orm.WorkflowSessionStep
			if err := tx.Where("session_id = ? AND step_id = ? AND validity <> ? AND status = ?", session.ID, req.StepID, "stale", "succeeded").Order("attempt DESC").First(&step).Error; err != nil {
				return err
			}
			now := time.Now().UTC()
			if err := tx.Create(&orm.WorkflowReviewCheckpoint{ID: common.GenerateID(), SessionID: session.ID, AttemptID: step.ID, StepID: step.StepID, Status: "accepted", AcceptedBy: owner, AcceptedAt: &now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		if req.PreferenceScope != "" {
			if _, err := saveWorkflowApprovalPreference(tx, session.CreateUserID, session.WorkflowID, req.StepID, req.PreferenceScope); err != nil {
				return err
			}
		}
		status := SessionStatusActive
		if req.Kind == "rewind" {
			// Only transfer Chat ownership here. The existing transition command
			// must still see completed/failed to apply its original recovery rules.
			status = session.Status
		}
		if approval {
			projected, err := projectSession(ctx, tx, &session)
			if err != nil {
				return err
			}
			if projected.Display.Status == "waiting" {
				status = SessionStatusWaiting
			} else if projected.Projection.Completed {
				status = SessionStatusCompleted
			}
		}
		binding, err := json.Marshal(nativeTurnBinding{HistoryID: req.HistoryID})
		if err != nil {
			return err
		}
		result := tx.Model(&orm.WorkflowSession{}).Where("id = ? AND state_version = ?", session.ID, req.ExpectedStateVersion).
			Updates(map[string]any{"status": status, "control_binding_json": string(binding), "state_version": gorm.Expr("state_version + 1"), "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return controlstore.Reject("STATE_VERSION_CONFLICT", "workflow changed; reload before approving")
		}
		session.StateVersion++
		version = session.StateVersion
		session.Status = status
		session.ControlBindingJSON = string(binding)
		payload, _ := json.Marshal(map[string]any{"status": status})
		return appendSessionStateEvent(tx, session, "workflow.patch", payload)
	})
	return version, err
}

// A rejected/failed Chat run must not leave a released workflow pretending to
// execute. The version fence protects a newer dispatch, pause or user decision.
func FinishNativeControlTurn(ctx context.Context, db *gorm.DB, sessionID string, version int64) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session orm.WorkflowSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sessionID).First(&session).Error; err != nil {
			return err
		}
		if session.StateVersion != version || session.Status != SessionStatusActive {
			return nil
		}
		return failIdleSession(ctx, tx, sessionID)
	})
}

// workflowApprovalPreferenceAllSteps is stored as a sentinel rather than
// expanding the preference to the workflow's current nodes. That keeps the
// user's workflow-wide choice valid when a later workflow revision adds or
// reorders approval checkpoints.
const workflowApprovalPreferenceAllSteps = "*"

func applyApprovalPreferences(ctxDB *gorm.DB, userID, workflowID string, projection graphengine.Projection) graphengine.Projection {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(workflowID) == "" {
		return projection
	}
	var rows []orm.WorkflowApprovalPreference
	if err := ctxDB.Where("user_id = ? AND workflow_id = ?", userID, workflowID).Find(&rows).Error; err != nil {
		// Keep projections available during rolling upgrades before the migration
		// has reached every local/dev database.
		return projection
	}
	for stepID, node := range projection.Nodes {
		node.RequiresApproval = displaystate.ApprovalRequired(node.RequiresApproval, stepID, rows)
		// Expose the effective mode to every controller, not the package default.
		// Otherwise agents can still hand off while the UI hides approval controls.
		node.Mode = "auto"
		if node.RequiresApproval {
			node.Mode = "human"
		}
		projection.Nodes[stepID] = node
	}
	return projection
}

func projectWithApprovalPreferences(db *gorm.DB, userID, workflowID string, graph *graphengine.CompiledStateGraph, snapshot graphengine.RuntimeSnapshot) graphengine.Projection {
	return applyApprovalPreferences(db, userID, workflowID, graphengine.Project(graph, snapshot))
}

func workflowApprovalPreferenceTarget(stepID, scope string) (string, error) {
	stepID = strings.TrimSpace(stepID)
	if stepID == "" || (scope != "step" && scope != "following") {
		return "", controlstore.Reject("INVALID_COMMAND", "step_id and scope (step|following) are required")
	}
	if scope == "following" {
		stepID = workflowApprovalPreferenceAllSteps
	}
	return stepID, nil
}

func saveWorkflowApprovalPreference(db *gorm.DB, userID, workflowID, stepID, scope string) (string, error) {
	stepID, err := workflowApprovalPreferenceTarget(stepID, scope)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	err = db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "workflow_id"}, {Name: "step_id"}},
		DoUpdates: clause.Assignments(map[string]any{"approval_required": false, "updated_at": now}),
	}).Create(&orm.WorkflowApprovalPreference{UserID: userID, WorkflowID: workflowID, StepID: stepID,
		ApprovalRequired: false, CreatedAt: now, UpdatedAt: now}).Error
	return stepID, err
}

// SetWorkflowApprovalPreference persists a user-level exception to a package's
// default approval modes. "step" affects this checkpoint in future sessions;
// "following" means that, from this choice onward, this user never needs to
// approve any checkpoint in future runs of this workflow (in any chat).
func SetWorkflowApprovalPreference(w http.ResponseWriter, r *http.Request) {
	userID := common.UserID(r)
	var session orm.WorkflowSession
	if err := store.DB().Where("id = ? AND dismissed = false", common.PathVar(r, "session_id")).First(&session).Error; err != nil {
		common.ReplyErr(w, "session not found", http.StatusNotFound)
		return
	}
	if userID == "" || session.CreateUserID != userID {
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
		return
	}
	if !IsWorkflowUserControlRequest(r) {
		common.ReplyErr(w, "use the authenticated workflow page to change approval preferences", http.StatusForbidden)
		return
	}
	var req approvalPreferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid approval preference", http.StatusBadRequest)
		return
	}
	req.StepID = strings.TrimSpace(req.StepID)
	if _, err := workflowApprovalPreferenceTarget(req.StepID, req.Scope); err != nil {
		common.ReplyErr(w, "step_id and scope (step|following) are required", http.StatusUnprocessableEntity)
		return
	}
	// The current UI only offers opt-out actions. Reject opt-in writes so a
	// malformed client cannot silently make package-auto steps require approval.
	if req.ApprovalRequired {
		common.ReplyErr(w, "approval_required must be false", http.StatusUnprocessableEntity)
		return
	}
	graph, err := loadSessionGraph(r.Context(), store.DB(), &session)
	if err != nil {
		common.ReplyErr(w, "load workflow graph failed: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if _, ok := graph.Nodes[req.StepID]; !ok {
		common.ReplyErr(w, "workflow step not found", http.StatusNotFound)
		return
	}
	stepID, err := saveWorkflowApprovalPreference(store.DB().WithContext(r.Context()), userID, session.WorkflowID, req.StepID, req.Scope)
	if err != nil {
		common.ReplyErr(w, "save approval preference failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"workflow_id": session.WorkflowID, "scope": req.Scope, "step_ids": []string{stepID},
		"workflow_wide": req.Scope == "following", "approval_required": false,
	})
}
