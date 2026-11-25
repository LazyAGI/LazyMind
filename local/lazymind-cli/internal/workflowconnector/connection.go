package workflowconnector

import "time"

const ConnectionPending = "lazymind_pending"

// ConnectionEvidence comes from the external connector's existing Workflow
// actions poll. Host capability and panel health are observed separately.
type ConnectionEvidence struct {
	PairingRevision string                 `json:"pairing_revision,omitempty"`
	Connection      *ConnectionObservation `json:"connection,omitempty"`
}

type ConnectionObservation struct {
	CheckedAt int64  `json:"checked_at"`
	Issue     string `json:"issue"`
}

// Issue checks that the observation belongs to this pairing and is recent
// enough for the normal poll, including its bounded retry and request timeout.
func (e ConnectionEvidence) Issue(revision string, heartbeat int64, since time.Time) string {
	if e.Connection == nil || e.PairingRevision != revision {
		return ConnectionPending
	}
	at := e.Connection.CheckedAt
	if at <= 0 || at > heartbeat || at < since.UnixMilli() || time.Since(time.UnixMilli(at)) > time.Minute {
		return ConnectionPending
	}
	switch e.Connection.Issue {
	case "", "lazymind_auth_required", "lazymind_unavailable", "pairing_required", "pairing_disabled",
		"pairing_account_mismatch", "pairing_credential_mismatch", "pairing_invalid", "pairing_unreadable":
		return e.Connection.Issue
	default:
		return ConnectionPending
	}
}

func ConnectionRetryable(issue string) bool {
	// During a rapid off/on cycle a disabled response may be followed by
	// recovery from a new worker. The pairing file is checked on every read.
	return issue == ConnectionPending || issue == "lazymind_unavailable" || issue == "pairing_disabled"
}
