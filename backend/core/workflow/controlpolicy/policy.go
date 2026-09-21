// Package controlpolicy owns workflow admission independently of hosts and storage.
package controlpolicy

const Protocol = "workflow.control.v1"

type Facts struct {
	Status          string
	Dismissed       bool
	PendingReviews  int64
	ActiveAttempts  int64
	NativeAttempts  int64
	BindingRequired bool
	Bound           bool
}

type Admission struct {
	CanBegin bool   `json:"can_begin"`
	Reason   string `json:"reason,omitempty"`
}

func Decide(f Facts) (continuation string, admission Admission) {
	if f.Dismissed || f.Status == "stopped" {
		return "stopped", Admission{Reason: "session_stopped"}
	}
	if f.BindingRequired && !f.Bound {
		return "binding_required", Admission{Reason: "binding_required"}
	}
	if f.PendingReviews > 0 {
		if f.ActiveAttempts > 0 {
			return "draining", Admission{Reason: "review_pending"}
		}
		return "awaiting_user", Admission{Reason: "review_pending"}
	}
	if f.Status == "completed" {
		return "completed", Admission{Reason: "session_completed"}
	}
	if f.Status == "failed" {
		return "failed", Admission{Reason: "recovery_required"}
	}
	if f.NativeAttempts > 0 {
		return "awaiting_executor", Admission{Reason: "native_execution_active"}
	}
	return "continue", Admission{CanBegin: true}
}

// Delivery transitions never turn an ambiguous send into an automatic retry.
func CanSettleDelivery(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case "pending":
		return to == "dispatching" || to == "superseded"
	case "dispatching":
		return to == "accepted" || to == "failed" || to == "unknown"
	case "unknown":
		return to == "accepted"
	default:
		return false
	}
}
