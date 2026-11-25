package workflowhost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"time"

	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workflowconnector"
)

// RuntimeStatus is evidence from an authenticated DSH dispatcher poll. It says
// nothing about an individual Workflow's binding or execution eligibility.
type RuntimeStatus struct {
	ConnectorID string `json:"connector_id"`
	Revision    string `json:"revision"`
	CheckedAt   int64  `json:"checked_at"`
	Issue       string `json:"issue,omitempty"`
	MCPReady    bool   `json:"mcp_ready"`
	DSHLoad
}

// DSHLoad describes the module and process that sent the authenticated poll.
type DSHLoad struct {
	Build       string `json:"build,omitempty"`
	Instance    string `json:"process_instance,omitempty"`
	StartedAt   int64  `json:"process_started_at,omitempty"`
	Panel       string `json:"panel,omitempty"`
	ClientBuild string `json:"client_build,omitempty"`
}

var dshInstancePattern = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

func (load DSHLoad) valid(checkedAt int64) bool {
	return workflowconnector.ValidDSHBuild(load.Build) && dshInstancePattern.MatchString(load.Instance) &&
		load.StartedAt > 0 && load.StartedAt <= checkedAt
}

// ConnectionIssue applies the same recent, current-pairing observation rules as
// the Codex and WorkBuddy workers, after DSH's shorter liveness check.
func (r RuntimeStatus) ConnectionIssue(pair Pairing) string {
	evidence := workflowconnector.ConnectionEvidence{
		PairingRevision: r.Revision,
		Connection:      &workflowconnector.ConnectionObservation{CheckedAt: r.CheckedAt, Issue: r.Issue},
	}
	return evidence.Issue(runtimeRevision(pair), r.CheckedAt, time.Time{})
}

func runtimeRevision(pair Pairing) string {
	sum := sha256.Sum256([]byte(pair.Token + "\x00" + pair.RuntimeRevision))
	return hex.EncodeToString(sum[:])
}

// RecordRuntimeStatus is called only after local pairing authentication. Normal
// one-second polling is coalesced to one write per three seconds; failures and
// recovery are recorded immediately. No token, account or response data is saved.
func RecordRuntimeStatus(home string, pair Pairing, issue string, mcpReady bool, loaded ...DSHLoad) error {
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
	if len(loaded) > 0 && loaded[0].valid(now) {
		value.DSHLoad = loaded[0]
		if !workflowconnector.ValidDSHBuild(value.ClientBuild) {
			value.ClientBuild = ""
		}
		switch value.Panel {
		case "", "waiting-session", "bound", "unbound", "unavailable", "access-denied":
		default:
			value.Panel = ""
		}
	}
	var previous RuntimeStatus
	if body, err := os.ReadFile(path); err == nil && json.Unmarshal(body, &previous) == nil &&
		previous.ConnectorID == value.ConnectorID && previous.Revision == value.Revision && previous.Issue == issue && previous.MCPReady == mcpReady && previous.DSHLoad == value.DSHLoad &&
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
	value, err := ReadLastRuntimeStatus(home, pair)
	if err != nil {
		return RuntimeStatus{}, err
	}
	age := time.Now().UnixMilli() - value.CheckedAt
	if age < 0 || age > 12000 {
		return RuntimeStatus{}, errors.New("dispatcher runtime status expired")
	}
	return value, nil
}

// ReadLastRuntimeStatus provides installation history for a still-current
// pairing. Call ReadRuntimeStatus when using the evidence for liveness.
func ReadLastRuntimeStatus(home string, pair Pairing) (RuntimeStatus, error) {
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
	if value.DSHLoad != (DSHLoad{}) && !value.DSHLoad.valid(value.CheckedAt) {
		return RuntimeStatus{}, errors.New("invalid dispatcher load evidence")
	}
	if value.Issue != "" && value.Issue != "lazymind_auth_required" && value.Issue != "lazymind_unavailable" {
		return RuntimeStatus{}, errors.New("invalid dispatcher runtime issue")
	}
	return value, nil
}
