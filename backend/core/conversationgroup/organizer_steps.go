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
	steps := []organizerStep{{ID: "preparation"}, {ID: "organization"}, {ID: "review"}, {ID: "application"}}
	var prep organizerPreparation
	var cp incrementalCheckpoint
	_ = json.Unmarshal(run.PreparationJSON, &prep)
	_ = json.Unmarshal(run.CheckpointJSON, &cp)
	complete := run.Status == "succeeded" || run.Status == "confirmed" || run.Status == "undone"
	current := 1
	if len(run.PreparationJSON) > 0 && !prep.Sealed || run.Stage == "snapshot" || run.Stage == "preparing" {
		current = 0
	}
	if run.Stage == "final" || run.Stage == "finalizing" || cp.Stage == "final" || (prep.Sealed && cp.Cursor >= int(run.ProgressTotal)) {
		current = 2
	}
	if run.Status == "applying" || run.Stage == "applying" || run.ErrorCode == "apply_failed" {
		current = 3
	}
	steps[0].Total = prep.BatchTotal
	if steps[0].Total == 0 && prep.Total > 0 {
		steps[0].Total = (prep.Total + titlePreparationBatchSize - 1) / titlePreparationBatchSize
	}
	steps[0].Completed = prep.BatchCurrent
	steps[0].Current = min(prep.BatchCurrent+1, steps[0].Total)
	if prep.Sealed && prep.Total == 0 {
		steps[0].Detail = "reused"
	}
	remaining := max(0, int(run.ProgressTotal)-cp.Cursor)
	steps[1].Total = int(cp.Version) + (remaining+49)/50
	steps[1].Completed = int(cp.Version)
	steps[1].Current = min(int(cp.Version)+1, steps[1].Total)
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
		} else if i == 0 && run.Stage == "snapshot" {
			steps[i].Detail = "snapshot"
		} else if i == 1 && cp.Pending != nil {
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
