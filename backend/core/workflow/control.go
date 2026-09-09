package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
)

type WorkflowControlCommand struct {
	CommandID       string `json:"command_id"`
	Kind            string `json:"kind"`
	StateVersion    int64  `json:"expected_state_version"`
	ReviewID        string `json:"review_id,omitempty"`
	ReviewVersion   int64  `json:"review_version,omitempty"`
	ManifestHash    string `json:"manifest_hash,omitempty"`
	StepID          string `json:"step_id,omitempty"`
	PreferenceScope string `json:"preference_scope,omitempty"`
	Objective       string `json:"objective,omitempty"`
	Instruction     string `json:"runtime_instruction,omitempty"`
}

type WorkflowControlReceipt struct {
	CommandID    string `json:"command_id"`
	Kind         string `json:"kind"`
	ReviewID     string `json:"review_id,omitempty"`
	ExecutionID  string `json:"execution_id,omitempty"`
	ActionID     string `json:"action_id,omitempty"`
	ResumeReason string `json:"resume_reason,omitempty"`
}

type WorkflowControlResult struct {
	Receipt WorkflowControlReceipt `json:"receipt"`
	Control *controlstore.Snapshot `json:"control"`
}

type WorkflowControlService struct{ DB *gorm.DB }

func controlOwner(session orm.WorkflowSession, owner string) error {
	if owner == "" || session.CreateUserID != owner {
		return controlstore.Reject("PERMISSION_DENIED", "workflow belongs to another owner")
	}
	if !controlstore.Controlled(session) {
		return controlstore.Reject("CONTROL_PROTOCOL_REQUIRED", "this run uses the legacy workflow protocol")
	}
	return nil
}

func (s WorkflowControlService) Execute(ctx context.Context, owner, sessionID string, command WorkflowControlCommand) (WorkflowControlResult, error) {
	var result WorkflowControlResult
	if command.CommandID == "" || len(command.CommandID) > 255 {
		return result, controlstore.Reject("INVALID_COMMAND", "command_id is required and must be at most 255 characters")
	}
	body, err := json.Marshal(command)
	if err != nil {
		return result, err
	}
	digest := controlstore.Hash(body)
	err = controlstore.Transaction(ctx, s.DB, sessionID, func(tx *gorm.DB, session *orm.WorkflowSession) error {
		if err := controlOwner(*session, owner); err != nil {
			return err
		}
		var saved orm.WorkflowCommand
		if err := tx.Where("command_id = ?", command.CommandID).First(&saved).Error; err == nil {
			if saved.OwnerUserID != owner || saved.SessionID != sessionID || saved.RequestHash != digest {
				return controlstore.Reject("COMMAND_CONFLICT", "command_id was already used for another operation")
			}
			if err := json.Unmarshal(saved.ResponseJSON, &result.Receipt); err != nil {
				return err
			}
			result.Control, err = controlstore.Read(tx, *session)
			return err
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		result.Receipt = WorkflowControlReceipt{CommandID: command.CommandID, Kind: command.Kind}
		if session.Dismissed {
			return controlstore.Reject("SESSION_STOPPED", "workflow has been dismissed")
		}
		if command.Kind != "stop" && command.Kind != "confirm" && command.Kind != "confirm_and_continue" && session.StateVersion != command.StateVersion {
			return controlstore.Reject("STATE_VERSION_CONFLICT", "refresh the workflow before applying this operation")
		}
		switch command.Kind {
		case "begin":
			if command.StepID == "" {
				return controlstore.Reject("INVALID_COMMAND", "step_id is required")
			}
			transition := transitionCommandRequest{CommandID: command.CommandID, Operation: "advance", RetryOrigin: "automatic",
				TargetStepID: command.StepID, ExpectedStateVersion: session.StateVersion, HandOff: true,
				Objective: command.Objective, RuntimeInstruction: command.Instruction}
			response, updated, tasks, err := applyWorkflowTransition(ctx, tx, session.ID, transition)
			if err != nil {
				return err
			}
			if !response.Accepted || len(tasks) != 1 {
				return controlstore.Reject("BEGIN_REJECTED", "step did not create one execution")
			}
			*session = updated
			result.Receipt.ExecutionID = tasks[0]
			if err := tx.Model(&orm.WorkflowHostAction{}).Where("session_id = ? AND kind = 'continue' AND execution_id = '' AND status IN ? AND consumed_at IS NULL", session.ID, []string{"accepted", "dispatching", "unknown"}).
				Update("consumed_at", time.Now().UTC()).Error; err != nil {
				return err
			}
			if err := tx.Model(&orm.WorkflowHostAction{}).Where("session_id = ? AND kind = 'continue' AND execution_id = '' AND status = 'pending' AND consumed_at IS NULL", session.ID).
				Updates(map[string]any{"consumed_at": time.Now().UTC(), "status": "superseded"}).Error; err != nil {
				return err
			}

		case "confirm", "confirm_and_continue":
			if err := confirmWorkflowReview(ctx, tx, session, owner, command); err != nil {
				return err
			}
			result.Receipt.ReviewID = command.ReviewID
			if command.Kind == "confirm_and_continue" {
				state, err := controlstore.Read(tx, *session)
				if err != nil {
					return err
				}
				if state.Admission.CanBegin && state.ActiveExecutions == 0 && state.Binding.Bound {
					result.Receipt.ActionID, err = controlstore.EnqueueHostAction(tx, *session, command.CommandID, "continue", "")
					if err != nil {
						return err
					}
				} else {
					result.Receipt.ResumeReason = state.Continuation
				}
			}
		case "continue":
			if err := controlstore.GuardBegin(tx, *session); err != nil {
				return err
			}
			if err := ensureNoActiveAttempts(tx, session.ID); err != nil {
				return err
			}
			result.Receipt.ActionID, err = controlstore.EnqueueHostAction(tx, *session, command.CommandID, "continue", "")
			if err != nil {
				return err
			}
		case "retry", "rewind":
			if session.Status == "stopped" {
				return controlstore.Reject("SESSION_STOPPED", "resume before retrying")
			}
			if command.StepID == "" {
				return controlstore.Reject("INVALID_COMMAND", "step_id is required for recovery")
			}
			if err := ensureNoActiveAttempts(tx, session.ID); err != nil {
				return err
			}
			// Invalidate exactly the checkpoints replaced by the recovery transition.
			transition := transitionCommandRequest{CommandID: command.CommandID, Operation: command.Kind,
				RetryOrigin: "user", TargetStepID: command.StepID, ExpectedStateVersion: session.StateVersion,
				HandOff: true, controlAuthorized: true}
			response, updated, tasks, err := applyWorkflowTransition(ctx, tx, session.ID, transition)
			if err != nil {
				return err
			}
			if !response.Accepted || len(tasks) != 1 {
				return controlstore.Reject("RECOVERY_REJECTED", "recovery did not create one execution")
			}
			*session = updated
			if err := tx.Model(&orm.WorkflowReviewCheckpoint{}).Where("session_id = ? AND status = ? AND attempt_id IN (?)", session.ID, "pending",
				tx.Model(&orm.WorkflowSessionStep{}).Select("id").Where("session_id = ? AND validity <> ?", session.ID, "effective")).
				Updates(map[string]any{"status": "superseded", "decision_command_id": command.CommandID, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
			result.Receipt.ExecutionID = tasks[0]
			result.Receipt.ActionID, err = controlstore.EnqueueHostAction(tx, *session, command.CommandID, "continue", tasks[0])
			if err != nil {
				return err
			}
		case "stop", "resume":
			result.Receipt.ActionID, _, err = controlstore.ApplyLifecycle(tx, session, command.CommandID, command.Kind == "stop")
			if err != nil {
				return err
			}
		default:
			return controlstore.Reject("INVALID_COMMAND", "unsupported workflow control operation")
		}
		if err := controlstore.BumpEvent(tx, session, "control.changed", session.ID, command.CommandID, result.Receipt); err != nil {
			return err
		}
		receipt, err := json.Marshal(result.Receipt)
		if err != nil {
			return err
		}
		if err := tx.Create(&orm.WorkflowCommand{CommandID: command.CommandID, OwnerUserID: owner, SessionID: sessionID,
			ContractVersion: controlpolicy.Protocol, RequestHash: digest, HTTPStatus: 200, ResponseJSON: receipt, CreatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
		result.Control, err = controlstore.Read(tx, *session)
		return err
	})
	return result, err
}

func ensureNoActiveAttempts(tx *gorm.DB, sessionID string) error {
	var count int64
	if err := tx.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND validity = 'effective' AND status IN ?", sessionID,
		[]string{"pending", "queued", "claimed", "running"}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return controlstore.Reject("EXECUTION_ACTIVE", "authorized executions are still running")
	}
	return nil
}

func confirmWorkflowReview(ctx context.Context, tx *gorm.DB, session *orm.WorkflowSession, owner string, command WorkflowControlCommand) error {
	if session.Status == "stopped" {
		return controlstore.Reject("SESSION_STOPPED", "resume before confirming")
	}
	var review orm.WorkflowReviewCheckpoint
	if err := tx.Where("id = ? AND session_id = ?", command.ReviewID, session.ID).First(&review).Error; err != nil {
		return err
	}
	if review.Status != "pending" || review.Version != command.ReviewVersion || review.ManifestHash != command.ManifestHash {
		return controlstore.Reject("REVIEW_VERSION_CONFLICT", "the review or its content changed; refresh before confirming")
	}
	var slots []string
	if err := json.Unmarshal([]byte(review.SlotsJSON), &slots); err != nil {
		return err
	}
	manifest, hash, err := controlstore.BuildManifest(tx, session.ID, slots)
	if err != nil {
		return err
	}
	if hash != command.ManifestHash {
		return controlstore.Reject("REVIEW_VERSION_CONFLICT", "the displayed content is no longer current")
	}
	graph, err := loadSessionGraph(ctx, tx, session)
	if err != nil {
		return err
	}
	var sealed controlstore.Manifest
	if err := json.Unmarshal([]byte(manifest), &sealed); err != nil {
		return err
	}
	for _, slot := range graph.Nodes[review.StepID].RequiredOutputs {
		found := false
		for _, item := range sealed.Items {
			if item.Slot == slot {
				found = true
				break
			}
		}
		if !found {
			return controlstore.Reject("REVIEW_OUTPUT_MISSING", "required output "+slot+" must be present before confirmation")
		}
	}

	now := time.Now().UTC()
	if err := tx.Model(&review).Updates(map[string]any{"status": "accepted", "manifest_json": manifest,
		"accepted_by": owner, "accepted_at": now, "decision_command_id": command.CommandID, "updated_at": now}).Error; err != nil {
		return err
	}
	if command.PreferenceScope != "" {
		if _, err := saveWorkflowApprovalPreference(tx, owner, session.WorkflowID, review.StepID, command.PreferenceScope); err != nil {
			return err
		}
	}
	if err := freezeRouteDecision(ctx, tx, session.ID, review.StepID, review.AttemptID); err != nil {
		return err
	}
	return tx.Where("id = ?", session.ID).First(session).Error
}

type WorkflowControlHandler struct{ Service WorkflowControlService }

func writeWorkflowControlError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "WORKFLOW_CONTROL_FAILED"
	var problem *controlstore.Error
	var transition *transitionRejection
	if errors.As(err, &problem) {
		code, status = problem.Code, http.StatusConflict
		if code == "PERMISSION_DENIED" || code == "HOST_AUTH_REQUIRED" {
			status = http.StatusForbidden
		}
		if code == "INVALID_COMMAND" {
			status = http.StatusUnprocessableEntity
		}
	} else if errors.As(err, &transition) {
		code, status = transition.response.Error.Code, transition.status
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		code, status = "WORKFLOW_NOT_FOUND", http.StatusNotFound
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"code": code, "message": err.Error()}})
}

// IsWorkflowUserControlRequest classifies browser requests; authentication and
// workflow ownership must still be checked by the caller.
func IsWorkflowUserControlRequest(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && origin.Host != "" && (origin.Scheme == "http" || origin.Scheme == "https") && r.Header.Get("X-LazyMind-Invocation-Id") == ""
}

func (h WorkflowControlHandler) Command(w http.ResponseWriter, r *http.Request) {
	// MCP invocations may execute steps; they never impersonate an interactive decision.
	if !IsWorkflowUserControlRequest(r) {
		writeWorkflowControlError(w, controlstore.Reject("PERMISSION_DENIED", "use the authenticated workflow page for user control actions"))
		return
	}
	h.decodeCommand(w, r, "")
}

func (h WorkflowControlHandler) decodeCommand(w http.ResponseWriter, r *http.Request, kind string) {
	var command WorkflowControlCommand
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		writeWorkflowControlError(w, controlstore.Reject("INVALID_COMMAND", "invalid workflow control command"))
		return
	}
	if kind != "" {
		command.Kind = kind
	}
	h.executeCommand(w, r, command)
}

func (h WorkflowControlHandler) executeCommand(w http.ResponseWriter, r *http.Request, command WorkflowControlCommand) {
	result, err := h.Service.Execute(r.Context(), strings.TrimSpace(r.Header.Get("X-User-Id")), common.PathVar(r, "session_id"), command)
	if err != nil {
		writeWorkflowControlError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

// Begin is the typed execution entry point. It creates a grant, never a user review decision.
func (h WorkflowControlHandler) Begin(w http.ResponseWriter, r *http.Request) {
	h.decodeCommand(w, r, "begin")
}

// StopExecution only removes execution authority. Resuming remains an interactive action.
func (h WorkflowControlHandler) StopExecution(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CommandID string `json:"command_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input); err != nil {
		writeWorkflowControlError(w, controlstore.Reject("INVALID_COMMAND", "command_id is required"))
		return
	}
	h.executeCommand(w, r, WorkflowControlCommand{Kind: "stop", CommandID: input.CommandID})
}

func (h WorkflowControlHandler) Read(w http.ResponseWriter, r *http.Request) {
	var result map[string]any
	err := controlstore.Transaction(r.Context(), h.Service.DB, common.PathVar(r, "session_id"), func(tx *gorm.DB, session *orm.WorkflowSession) error {
		if err := controlOwner(*session, strings.TrimSpace(r.Header.Get("X-User-Id"))); err != nil {
			return err
		}
		if r.URL.Query().Get("view") == "control" {
			state, err := controlstore.Read(tx, *session)
			result = map[string]any{"control": state}
			return err
		}
		projection, err := projectSession(r.Context(), tx, session)
		if err != nil {
			return err
		}
		dto := toSessionDTO(session)
		revisions, err := LoadDisplaySlots(r.Context(), tx, session.ID)
		if err != nil {
			return err
		}
		for i := range revisions {
			dto.Slots = append(dto.Slots, toSlotDTO(&revisions[i]))
		}
		enrichSlots(r.Context(), tx, session.ID, dto.Slots)
		steps, err := ListSteps(r.Context(), tx, session.ID)
		if err != nil {
			return err
		}
		for i := range steps {
			dto.Steps = append(dto.Steps, toStepDTO(&steps[i]))
		}
		result = map[string]any{"control": projection.Control, "session": dto, "projection": projection.Projection}
		return nil
	})
	if err != nil {
		writeWorkflowControlError(w, err)
		return
	}
	common.ReplyOK(w, result)
}
