package conversationgroup

import (
	"encoding/json"

	"lazymind/core/common/orm"
)

type organizerStep struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Current   int    `json:"current"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
}

// Derive display state from durable preparation/checkpoints even when terminal
// statuses replace Stage. No frontend timers or additional model stages are needed.
func organizerSteps(run orm.ConversationOrganizerRun) []organizerStep {
	// A visible run was created atomically with its frozen conversation scope.
	steps := []organizerStep{{ID: "snapshot", Detail: "snapshotLocked"}, {ID: "preparation"}, {ID: "organization"}, {ID: "review"}, {ID: "application"}}
	var prep organizerPreparation
	var cp incrementalCheckpoint
	_ = json.Unmarshal(run.PreparationJSON, &prep)
	_ = json.Unmarshal(run.CheckpointJSON, &cp)
	complete := run.Status == "succeeded" || run.Status == "confirmed" || run.Status == "undone"
	current := 2
	if len(run.PreparationJSON) > 0 && !prep.Sealed || run.Stage == "preparing" {
		current = 1
	}
	if run.Stage == "final" || run.Stage == "finalizing" || cp.Stage == "final" || (prep.Sealed && cp.Cursor >= int(run.ProgressTotal)) {
		current = 3
	}
	if run.Status == "applying" || run.Stage == "applying" || run.ErrorCode == "apply_failed" {
		current = 4
	}
	steps[1].Total = prep.BatchTotal
	if steps[1].Total == 0 && prep.Total > 0 {
		steps[1].Total = (prep.Total + titlePreparationBatchSize - 1) / titlePreparationBatchSize
	}
	steps[1].Completed = prep.BatchCurrent
	steps[1].Current = min(prep.BatchCurrent+1, steps[1].Total)
	if prep.Sealed && prep.Total == 0 {
		steps[1].Detail = "reused"
	}
	remaining := max(0, int(run.ProgressTotal)-cp.Cursor)
	steps[2].Total = int(cp.Version) + (remaining+49)/50
	steps[2].Completed = int(cp.Version)
	steps[2].Current = min(int(cp.Version)+1, steps[2].Total)
	for i := range steps {
		steps[i].Status = "pending"
		if complete || i < current {
			steps[i].Status = "completed"
			continue
		}
		if i != current {
			continue
		}
		steps[i].Status = "active"
		if run.Status == "failed" || run.Status == "canceled" {
			steps[i].Status = run.Status
		}
		if run.Stage == "canceling" {
			steps[i].Detail = "canceling"
		} else if i == 2 && cp.Pending != nil {
			for _, op := range cp.Pending.Operations {
				if op.Op == "update" || op.Op == "merge" {
					steps[i].Detail = "auditing"
					break
				}
			}
		}
	}
	return steps
}
