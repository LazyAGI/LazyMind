package workflowconnector

import (
	"testing"
	"time"
)

func TestConnectionEvidenceReadiness(t *testing.T) {
	now := time.Now()
	heartbeat, since := now.UnixMilli(), now.Add(-10*time.Second)
	for _, tc := range []struct {
		name     string
		revision string
		poll     *ConnectionObservation
		want     string
	}{
		{"legacy writer", "", nil, ConnectionPending},
		{"previous pairing", "past", &ConnectionObservation{CheckedAt: heartbeat}, ConnectionPending},
		{"before startup", "current", &ConnectionObservation{CheckedAt: since.Add(-time.Second).UnixMilli()}, ConnectionPending},
		{"future", "current", &ConnectionObservation{CheckedAt: heartbeat + 1}, ConnectionPending},
		{"unrecognized issue", "current", &ConnectionObservation{CheckedAt: heartbeat, Issue: "other"}, ConnectionPending},
		{"expired authorization", "current", &ConnectionObservation{CheckedAt: heartbeat, Issue: "lazymind_auth_required"}, "lazymind_auth_required"},
		{"temporary outage", "current", &ConnectionObservation{CheckedAt: heartbeat, Issue: "lazymind_unavailable"}, "lazymind_unavailable"},
		{"successful actions poll", "current", &ConnectionObservation{CheckedAt: heartbeat}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence := ConnectionEvidence{PairingRevision: tc.revision, Connection: tc.poll}
			if got := evidence.Issue("current", heartbeat, since); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	evidence := ConnectionEvidence{Connection: &ConnectionObservation{CheckedAt: heartbeat}}
	if got := evidence.Issue("", heartbeat, since); got != "" {
		t.Fatalf("upgraded worker could not verify a legacy pairing: %s", got)
	}
	evidence.Connection.CheckedAt = now.Add(-time.Minute - time.Second).UnixMilli()
	if got := evidence.Issue("", heartbeat, time.Time{}); got != ConnectionPending {
		t.Fatalf("old poll became current via a new heartbeat: %s", got)
	}
}
