// Package displaystate owns the user-visible state shared by all Workflow surfaces.
// Internal waiting is a scheduling state, not an approval or a user pause.
package displaystate

import (
	"encoding/json"
	"sort"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

type View struct {
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
	if err := db.Where("session_id = ? AND validity <> ?", session.ID, "stale").
		Order("created_at DESC, attempt DESC, id DESC").Find(&steps).Error; err != nil {
		return View{}, err
	}
	latest := map[string]orm.WorkflowSessionStep{}
	for _, step := range steps {
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
	stoppedAt := time.Time{}
	if session.LastStoppedAt != nil {
		stoppedAt = *session.LastStoppedAt
	}
	for _, step := range current {
		switch step.Status {
		case "pending", "queued", "claimed", "running":
			running = true
		}
		if step.TerminalCode == "WORKFLOW_STOPPED" && step.UpdatedAt.After(stoppedAt) {
			stoppedAt = step.UpdatedAt
		}
	}
	if session.Status == "pausing" {
		return View{Status: "running", Reason: "pausing"}, nil
	}
	if running {
		return View{Status: "running", Reason: "executing"}, nil
	}
	// A completed graph is not a completed workflow until its final review is
	// accepted. A pause must not implicitly approve a successful human step.
	approval, err := pendingApproval(db, session, current)
	if err != nil {
		return View{}, err
	}
	if approval != "" {
		return View{Status: "waiting", Reason: "approval_required", ApprovalStepID: approval}, nil
	}
	if session.Status == "completed" {
		return View{Status: "completed", Reason: "completed"}, nil
	}
	// Only an admitted driver or explicit user resume can enter active. Historical
	// stop markers must not hide that preparation before its new attempt exists.
	if session.Status == "active" {
		return View{Status: "running", Reason: "preparing"}, nil
	}
	if session.Status == "failed" {
		return View{Status: "failed", Reason: "execution_failed"}, nil
	}
	paused := session.Status == "stopped" || !stoppedAt.IsZero()
	for _, step := range current {
		if step.CreatedAt.After(stoppedAt) && session.Status != "stopped" {
			paused = false
		}
	}
	if paused {
		return View{Status: "paused", Reason: "user_stopped"}, nil
	}
	if len(current) == 0 {
		return View{Status: "running", Reason: "preparing"}, nil
	}
	step := current[0]
	if step.Status != "succeeded" {
		return View{Status: "failed", Reason: step.Status}, nil
	}
	return View{Status: "failed", Reason: "no_advance"}, nil
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
