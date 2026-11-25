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

func TestConnectionEvidenceAcrossAgentHosts(t *testing.T) {
	now := time.Now()
	for _, host := range []string{"deepseek-harness", "codex", "workbuddy"} {
		t.Run(host, func(t *testing.T) {
			revision := host + "-active"
			latest := now.UnixMilli()
			value := ConnectionEvidence{PairingRevision: revision, Connection: &ConnectionObservation{CheckedAt: latest}}
			if issue := value.Issue(host+"-previous", latest, now.Add(-time.Second)); issue != ConnectionPending {
				t.Fatalf("pairing mismatch: %q", issue)
			}
			value.Connection.CheckedAt = now.Add(-time.Minute - time.Second).UnixMilli()
			if issue := value.Issue(revision, latest, time.Time{}); issue != ConnectionPending {
				t.Fatalf("expired evidence: %q", issue)
			}
			value.Connection = &ConnectionObservation{CheckedAt: latest, Issue: "lazymind_auth_required"}
			if issue := value.Issue(revision, latest, time.Time{}); issue != "lazymind_auth_required" {
				t.Fatalf("expired authorization: %q", issue)
			}
			value.Connection.Issue = ""
			if issue := value.Issue(revision, latest, time.Time{}); issue != "" {
				t.Fatalf("authorization recovery: %q", issue)
			}
		})
	}
}
