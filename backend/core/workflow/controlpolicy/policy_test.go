package controlpolicy

import "testing"

func TestAdmissionSeparatesReviewExecutionAndLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name  string
		facts Facts
		state string
		begin bool
	}{
		{"automatic", Facts{Status: "active"}, "continue", true},
		{"human", Facts{Status: "waiting", PendingReviews: 1}, "awaiting_user", false},
		{"parallel drain", Facts{Status: "active", PendingReviews: 2, ActiveAttempts: 1}, "draining", false},
		{"last step review", Facts{Status: "completed", PendingReviews: 1}, "awaiting_user", false},
		{"stopped", Facts{Status: "stopped", PendingReviews: 1}, "stopped", false},
		{"unbound", Facts{Status: "active", BindingRequired: true}, "binding_required", false},
		{"bound", Facts{Status: "active", BindingRequired: true, Bound: true}, "continue", true},
		{"failed", Facts{Status: "failed"}, "failed", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state, admission := Decide(tt.facts)
			if state != tt.state || admission.CanBegin != tt.begin {
				t.Fatalf("got %s %+v", state, admission)
			}
		})
	}
}

func TestUnknownDeliveryCannotBeBlindlyRetried(t *testing.T) {
	if CanSettleDelivery("unknown", "pending") || CanSettleDelivery("accepted", "superseded") {
		t.Fatal("an ambiguous or accepted action must not be resent as a fresh action")
	}
	if !CanSettleDelivery("unknown", "accepted") || !CanSettleDelivery("dispatching", "unknown") {
		t.Fatal("delivery reconciliation is missing")
	}
}
