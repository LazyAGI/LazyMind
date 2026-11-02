// Package runstate owns run-status transitions shared by every controller.
// Hosts authenticate and deliver commands; they do not define run-state rules.
package runstate

import "lazymind/core/workflow/graphengine"

const (
	Active    = "active"
	Waiting   = "waiting"
	Completed = "completed"
	Failed    = "failed"
)

// AfterControl preserves the native control contract: accepting a continuation
// includes controller preparation before a new attempt exists. Rewind leaves
// terminal admission to the existing graph transition. Approval never releases
// another pending review, including a final review on a completed graph.
func AfterControl(current, kind string, approvalPending, completed bool) string {
	if kind == "rewind" {
		return current
	}
	if approvalPending {
		return Waiting
	}
	if completed {
		return Completed
	}
	return Active
}

// AfterProjection reconciles execution facts without ending an admitted
// continuation between successful steps. This is the same policy for native
// and external controllers, independent of the executor chosen for each step.
func AfterProjection(current string, projection graphengine.Projection, preparing bool) string {
	if current == "pausing" {
		return "pausing"
	}
	if projection.Completed {
		return Completed
	}
	status := Waiting
	for _, id := range projection.Current {
		switch projection.Nodes[id].Execution {
		case "pending", "queued", "claimed", "running":
			status = Active
		case "failed":
			if status != Active {
				status = Failed
			}
		}
	}
	if current == Active && len(projection.Current) == 0 && len(projection.Ready) > 0 && preparing {
		return Active
	}
	return status
}
