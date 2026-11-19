package workflowhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"

	"lazymind/agentconnector/internal/localfile"
)

// RuntimeStatus is evidence from an authenticated DSH dispatcher poll. It says
// nothing about an individual Workflow's binding or execution eligibility.
type RuntimeStatus struct {
	ConnectorID string `json:"connector_id"`
	Revision    string `json:"revision"`
	CheckedAt   int64  `json:"checked_at"`
	Issue       string `json:"issue,omitempty"`
	MCPReady    bool   `json:"mcp_ready"`
}

func runtimeRevision(pair Pairing) string {
	sum := sha256.Sum256([]byte(pair.Token + "\x00" + pair.RuntimeRevision))
	return hex.EncodeToString(sum[:])
}

// RecordRuntimeStatus is called only after local pairing authentication. Normal
// one-second polling is coalesced to one write per three seconds; failures and
// recovery are recorded immediately. No token, account or response data is saved.
func RecordRuntimeStatus(home string, pair Pairing, issue string, mcpReady bool) error {
	if pair.Provider != DSHProvider || !pair.Enabled {
		return nil
	}
	if issue != "" && issue != "lazymind_auth_required" && issue != "lazymind_unavailable" {
		return errors.New("invalid dispatcher runtime issue")
	}
	path, err := pairingPath(home, pair.ConnectorID)
	if err != nil {
		return err
	}
	path += ".runtime-status.json"
	now := time.Now().UnixMilli()
	value := RuntimeStatus{ConnectorID: pair.ConnectorID, Revision: runtimeRevision(pair), CheckedAt: now, Issue: issue, MCPReady: mcpReady}
	var previous RuntimeStatus
	if body, err := os.ReadFile(path); err == nil && json.Unmarshal(body, &previous) == nil &&
		previous.ConnectorID == value.ConnectorID && previous.Revision == value.Revision && previous.Issue == issue && previous.MCPReady == mcpReady &&
		now >= previous.CheckedAt && now-previous.CheckedAt < 3000 {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return localfile.Write(path, body, 0600)
}

func ReadRuntimeStatus(home string, pair Pairing) (RuntimeStatus, error) {
	path, err := pairingPath(home, pair.ConnectorID)
	if err != nil {
		return RuntimeStatus{}, err
	}
	body, err := os.ReadFile(path + ".runtime-status.json")
	if err != nil {
		return RuntimeStatus{}, err
	}
	var value RuntimeStatus
	if json.Unmarshal(body, &value) != nil || !pair.Enabled || pair.Provider != DSHProvider ||
		value.ConnectorID != pair.ConnectorID || value.Revision != runtimeRevision(pair) {
		return RuntimeStatus{}, errors.New("invalid dispatcher runtime status")
	}
	age := time.Now().UnixMilli() - value.CheckedAt
	if age < 0 || age > 12000 {
		return RuntimeStatus{}, errors.New("dispatcher runtime status expired")
	}
	if value.Issue != "" && value.Issue != "lazymind_auth_required" && value.Issue != "lazymind_unavailable" {
		return RuntimeStatus{}, errors.New("invalid dispatcher runtime issue")
	}
	return value, nil
}
