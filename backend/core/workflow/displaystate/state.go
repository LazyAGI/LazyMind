// Package displaystate owns the user-visible state shared by all Workflow surfaces.
// Internal waiting is a scheduling state, not an approval or a user pause.
package displaystate

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

const legacyUserStopSummary = "stopped by user"

type View struct {
	InputStepID    string `json:"input_step_id,omitempty"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	ApprovalStepID string `json:"approval_step_id"`
	// The native Chat turn retains stop authority after handing off execution.
	ChatHistoryID string `json:"chat_history_id,omitempty"`
}

// ChatHistoryID identifies the native turn whose Chat pause owns this workflow.
// Resume binds a new turn without changing the immutable trigger history.
func ChatHistoryID(session orm.WorkflowSession) (string, error) {
	var binding struct {
		HistoryID string `json:"chat_history_id"`
	}
	if session.ControlBindingJSON != "" {
		if err := json.Unmarshal([]byte(session.ControlBindingJSON), &binding); err != nil {
			return "", err
		}
	}
	if binding.HistoryID != "" {
		return binding.HistoryID, nil
	}
	return session.TriggerHistoryID, nil
}

// ApprovalRequired identifies a live review, not every internal waiting state.
func (view View) ApprovalRequired() bool {
	return view.Status == "waiting" && view.Reason == "approval_required" && view.ApprovalStepID != ""
}

// Read uses durable execution facts so a reload and a live event agree. Callers
// must propagate read errors; unavailable data is not an execution failure.
func Read(db *gorm.DB, session orm.WorkflowSession) (View, error) {
	view, err := readState(db, session)
	if err != nil {
		return View{}, err
	}
	if session.ControllerHost == "lazymind" {
		view.ChatHistoryID, err = ChatHistoryID(session)
	}
	return view, err
}

func readState(db *gorm.DB, session orm.WorkflowSession) (View, error) {
	var steps []orm.WorkflowSessionStep
	if err := db.Where("session_id = ?", session.ID).
		Order("created_at DESC, attempt DESC, id DESC").Find(&steps).Error; err != nil {
		return View{}, err
	}
	latest := map[string]orm.WorkflowSessionStep{}
	for _, step := range steps {
		if step.Validity == "stale" {
			continue
		}
		if prev, exists := latest[step.StepID]; !exists || step.Attempt > prev.Attempt {
			latest[step.StepID] = step
		}
	}
	current := make([]orm.WorkflowSessionStep, 0, len(latest))
	for _, step := range latest {
		current = append(current, step)
	}
	sort.Slice(current, func(i, j int) bool {
		if current[i].CreatedAt.Equal(current[j].CreatedAt) {
			return current[i].ID > current[j].ID
		}
		return current[i].CreatedAt.After(current[j].CreatedAt)
	})
	running := false
	for _, step := range current {
		switch step.Status {
		case "pending", "queued", "claimed", "running":
			running = true
		}
	}
	stoppedAt, err := UserStoppedAt(db, session.LastStoppedAt, current)
	if err != nil {
		return View{}, err
	}
	if session.Status == "pausing" {
		return View{Status: "running", Reason: "pausing"}, nil
	}
	if running {
		return View{Status: "running", Reason: "executing"}, nil
	}
	paused := session.Status == "stopped" || (session.Status == "waiting" && !stoppedAt.IsZero())
	for _, step := range current {
		if step.CreatedAt.After(stoppedAt) && session.Status != "stopped" {
			paused = false
		}
	}
	// Pausing suspends a checkpoint; it never accepts or discards its review.
	approval, err := pendingApproval(db, session, current)
	if err != nil {
		return View{}, err
	}
	if paused {
		return View{Status: "paused", Reason: "user_stopped", ApprovalStepID: approval}, nil
	}
	if approval != "" {
		return View{Status: "waiting", Reason: "approval_required", ApprovalStepID: approval}, nil
	}
	if session.Status == "active" || session.Status == "waiting" {
		pending, err := pendingInput(db, session)
		if err != nil {
			return View{}, err
		}
		if pending != "" {
			return View{Status: "waiting_input", Reason: "user_input_required", InputStepID: pending}, nil
		}
	}
	if session.Status == "completed" {
		return View{Status: "completed", Reason: "completed"}, nil
	}
	// Only an admitted driver or explicit user resume can enter active. Historical
	// stop markers must not hide that preparation before its new attempt exists.
	if session.Status == "active" {
		return preparingState(db, session, current)
	}
	if session.Status == "failed" {
		return View{Status: "failed", Reason: "execution_failed"}, nil
	}
	changed, err := inputsChanged(db, session, steps, current)
	if err != nil {
		return View{}, err
	}
	if changed {
		return View{Status: "waiting", Reason: "inputs_changed"}, nil
	}
	if len(current) == 0 {
		return preparingState(db, session, current)
	}
	step := current[0]
	if step.Status != "succeeded" {
		return View{Status: "failed", Reason: step.Status}, nil
	}
	if session.Status == "waiting" && session.WorkflowMode == "dynamic" && session.ControllerHost == "lazymind" && session.ControlProtocol == "" {
		for _, step := range current {
			if step.Status != "succeeded" {
				return View{Status: "failed", Reason: step.Status}, nil
			}
		}
		var task struct{ Params json.RawMessage }
		err := db.Model(&orm.SubAgentTask{}).Select("params").Where("id = ? AND conversation_id = ?", step.TaskID, session.ConversationID).Take(&task).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return View{}, err
		}
		if err == nil {
			var request struct {
				HandOff      bool   `json:"hand_off"`
				WorkflowMode string `json:"workflow_mode"`
				SessionID    string `json:"session_id"`
				StepID       string `json:"step_id"`
			}
			if err := json.Unmarshal(task.Params, &request); err != nil {
				return View{}, err
			}
			if request.HandOff && request.WorkflowMode == "dynamic" && request.SessionID == session.ID && request.StepID == step.StepID {
				return View{Status: "waiting", Reason: "next_step_required"}, nil
			}
		}
	}
	return View{Status: "failed", Reason: "no_advance"}, nil
}

// UserStoppedAt reads current and historical user-stop evidence for both the
// display projection and Task Center's conversation lifecycle checks.
func UserStoppedAt(db *gorm.DB, lastStoppedAt *time.Time, current []orm.WorkflowSessionStep) (time.Time, error) {
	stoppedAt := time.Time{}
	if lastStoppedAt != nil {
		stoppedAt = *lastStoppedAt
	}
	for _, step := range current {
		if step.TerminalCode == "WORKFLOW_STOPPED" && step.UpdatedAt.After(stoppedAt) {
			stoppedAt = step.UpdatedAt
		}
	}
	legacy, err := legacyUserStoppedAt(db, current)
	if legacy.After(stoppedAt) {
		stoppedAt = legacy
	}
	return stoppedAt, err
}

// Older native executions recorded a user stop on the linked subtask before
// WorkflowSessionStep.TerminalCode and WorkflowSession.LastStoppedAt existed.
// Recover only that exact durable stop reason; other interruptions never
// establish a user pause.
func legacyUserStoppedAt(db *gorm.DB, current []orm.WorkflowSessionStep) (time.Time, error) {
	stepByTask := map[string]orm.WorkflowSessionStep{}
	ids := make([]string, 0)
	for _, step := range current {
		if step.Status != "interrupted" || step.TerminalCode != "" || step.TaskID == "" {
			continue
		}
		stepByTask[step.TaskID] = step
		ids = append(ids, step.TaskID)
	}
	if len(ids) == 0 {
		return time.Time{}, nil
	}
	var tasks []struct{ ID string }
	if err := db.Model(&orm.SubAgentTask{}).Select("id").
		Where("id IN ? AND status = ? AND summary = ?", ids, "interrupted", legacyUserStopSummary).
		Find(&tasks).Error; err != nil {
		return time.Time{}, err
	}
	stoppedAt := time.Time{}
	for _, task := range tasks {
		if step := stepByTask[task.ID]; step.UpdatedAt.After(stoppedAt) {
			stoppedAt = step.UpdatedAt
		}
	}
	return stoppedAt, nil
}

func preparingState(db *gorm.DB, session orm.WorkflowSession, current []orm.WorkflowSessionStep) (View, error) {
	preparing := View{Status: "running", Reason: "preparing"}
	if session.ControllerHost != "external-agent" || !strings.HasPrefix(session.OriginRef, "run_") {
		return preparing, nil
	}
	var origin orm.ExternalChatRun
	err := db.Where("id = ? AND actor_user_id = ? AND conversation_id = ?", session.OriginRef, session.CreateUserID, session.ConversationID).Take(&origin).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return preparing, nil
	}
	if err != nil {
		return View{}, err
	}
	runs := db.Model(&orm.ExternalChatRun{}).Where("actor_user_id = ? AND conversation_id = ? AND created_at >= ?", session.CreateUserID, session.ConversationID, origin.CreatedAt).Session(&gorm.Session{})
	var active int64
	if err := runs.Where("status IN ?", []string{"pending", "running"}).Count(&active).Error; err != nil {
		return View{}, err
	}
	if active > 0 {
		return preparing, nil
	}
	var latest orm.ExternalChatRun
	if err := runs.Order("created_at DESC, id DESC").Take(&latest).Error; err != nil {
		return View{}, err
	}
	// A completed Chat reply does not prove the workflow started. Preserve real
	// execution/review states above, and distinguish failure from normal handoff.
	switch latest.Status {
	case "stopped":
		return View{Status: "paused", Reason: "user_stopped"}, nil
	case "completed", "failed":
		if len(current) == 0 {
			return View{Status: "failed", Reason: "execution_not_started"}, nil
		}
		for _, step := range current {
			if step.Status != "succeeded" {
				return View{Status: "failed", Reason: step.Status}, nil
			}
		}
		if latest.Status == "failed" {
			return View{Status: "failed", Reason: "external_driver_failed"}, nil
		}
		return View{Status: "waiting", Reason: "next_step_required"}, nil
	default:
		return preparing, nil
	}
}

// Native and external displays resolve the same pinned question artifact while
// retaining the producing step used to highlight the input checkpoint.
func pendingInput(db *gorm.DB, session orm.WorkflowSession) (string, error) {
	request, err := PendingInput(db, session)
	if err != nil || request == nil {
		return "", err
	}
	return request.StepID, nil
}

func pendingApproval(db *gorm.DB, session orm.WorkflowSession, current []orm.WorkflowSessionStep) (string, error) {
	hasSuccess := false
	for _, step := range current {
		hasSuccess = hasSuccess || step.Status == "succeeded"
	}
	if !hasSuccess {
		return "", nil
	}
	if session.ControllerHost == "external-agent" && session.ControlProtocol == "workflow.control.v1" {
		var reviews []orm.WorkflowReviewCheckpoint
		if err := db.Where("session_id = ? AND status = ?", session.ID, "pending").Find(&reviews).Error; err != nil {
			return "", err
		}
		for _, step := range current {
			for _, review := range reviews {
				if step.Status == "succeeded" && review.AttemptID == step.ID && review.Status == "pending" {
					return step.StepID, nil
				}
			}
		}
	} else if session.WorkflowRevisionID != "" {
		var revision orm.WorkflowRevision
		if err := db.Where("id = ?", session.WorkflowRevisionID).First(&revision).Error; err != nil {
			return "", err
		}
		var graph graphengine.CompiledStateGraph
		if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
			return "", err
		}
		var preferences []orm.WorkflowApprovalPreference
		if err := db.Where("user_id = ? AND workflow_id = ?", session.CreateUserID, session.WorkflowID).Find(&preferences).Error; err != nil {
			return "", err
		}
		for _, step := range current {
			if step.Status != "succeeded" || !ApprovalRequired(graph.Nodes[step.StepID].Mode == "human", step.StepID, preferences) {
				continue
			}
			var acceptedCount int64
			if err := db.Model(&orm.WorkflowReviewCheckpoint{}).Where("session_id = ? AND attempt_id = ? AND status = ?", session.ID, step.ID, "accepted").Count(&acceptedCount).Error; err != nil {
				return "", err
			}
			accepted := acceptedCount > 0
			// Review only checkpoints whose successor has not started. Historical
			// successful steps must not reopen approval behind a later execution.
			for _, edge := range graph.ControlEdges {
				if edge.From != step.StepID {
					continue
				}
				for _, successor := range current {
					if successor.StepID == edge.To && !successor.CreatedAt.Before(step.CreatedAt) {
						accepted = true
					}
				}
			}
			if !accepted {
				return step.StepID, nil
			}
		}
	}
	return "", nil
}

// ApprovalRequired is shared by the graph projection and display projection.
func ApprovalRequired(required bool, stepID string, preferences []orm.WorkflowApprovalPreference) bool {
	for _, pref := range preferences {
		if pref.StepID == "*" && !pref.ApprovalRequired {
			required = false
		}
	}
	for _, pref := range preferences {
		if pref.StepID == stepID {
			required = pref.ApprovalRequired
		}
	}
	return required
}

// Enrich is shared by native and hosted event writers. The field is replaced
// atomically (including an empty approval ID), never merged with a stale review.
func Enrich(db *gorm.DB, session orm.WorkflowSession, payload json.RawMessage) (json.RawMessage, error) {
	view, err := Read(db, session)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, err
	}
	body["display"] = view
	return json.Marshal(body)
}
