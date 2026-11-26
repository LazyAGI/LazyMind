package workflowhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workflowconnector"
)

// RuntimeStatus contains separate local host and Core observations. Earlier
// DSH builds have one combined observation and omit the runtime identity.
type RuntimeStatus struct {
	ConnectorID         string              `json:"connector_id"`
	Revision            string              `json:"revision"`
	CheckedAt           int64               `json:"checked_at"`
	ConnectionCheckedAt int64               `json:"connection_checked_at,omitempty"`
	Issue               string              `json:"issue,omitempty"`
	MCPReady            bool                `json:"mcp_ready"`
	Identity            *DSHRuntimeIdentity `json:"identity,omitempty"`
	DSHLoad
}

// DSHRuntimeIdentity scopes both observations to the same running dispatcher.
type DSHRuntimeIdentity struct {
	Dispatcher      string `json:"dispatcher"`
	Process         string `json:"process"`
	ConfigRevision  string `json:"config_revision"`
	PairingRevision string `json:"pairing_revision"`
}

// DSHLoad describes the evidence reported by the running plugin.
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

func (load DSHLoad) sanitize() DSHLoad {
	if !workflowconnector.ValidDSHBuild(load.ClientBuild) {
		load.ClientBuild = ""
	}
	switch load.Panel {
	case "", "waiting-session", "bound", "unbound", "unavailable", "access-denied":
	default:
		load.Panel = ""
	}
	return load
}

func (identity DSHRuntimeIdentity) configurationMatches(home string, pair Pairing, path string) bool {
	config, err := connectorconfig.ForProvider(home, DSHProvider, pair.Profile)
	return err == nil && config != nil && config.Controlled && config.PairingFile == path && config.Revision() == identity.ConfigRevision
}

// Current verifies the current configuration and native lock even before the
// first heartbeat. A prior process or an earlier pairing cannot renew evidence.
func (identity DSHRuntimeIdentity) Current(home string, pair Pairing) bool {
	if !pair.Enabled || pair.Provider != DSHProvider || identity.PairingRevision == "" || identity.PairingRevision != pair.RuntimeRevision ||
		!dshInstancePattern.MatchString(identity.Dispatcher) || !dshInstancePattern.MatchString(identity.Process) {
		return false
	}
	path, err := pairingPath(home, pair.ConnectorID)
	if err != nil || !identity.configurationMatches(home, pair, path) {
		return false
	}
	body, err := os.ReadFile(path + ".runtime.lock")
	var lock struct {
		PID             int    `json:"pid"`
		InstanceID      string `json:"instanceId"`
		ProcessInstance string `json:"processInstance"`
	}
	return err == nil && json.Unmarshal(body, &lock) == nil && lock.InstanceID == identity.Dispatcher &&
		lock.ProcessInstance == identity.Process && lock.PID > 0 && !DSHProcessExited(lock.PID)
}

// ConnectionIssue applies the shared host rules with Core's own observation
// timestamp and the current process start boundary.
func (r RuntimeStatus) ConnectionIssue(pair Pairing) string {
	at, ceiling := r.CheckedAt, r.CheckedAt
	var since time.Time
	if r.Identity != nil {
		at, ceiling = r.ConnectionCheckedAt, time.Now().UnixMilli()
		since = time.UnixMilli(r.StartedAt)
	}
	evidence := workflowconnector.ConnectionEvidence{
		PairingRevision: r.Revision,
		Connection:      &workflowconnector.ConnectionObservation{CheckedAt: at, Issue: r.Issue},
	}
	return evidence.Issue(runtimeRevision(pair), ceiling, since)
}

func runtimeRevision(pair Pairing) string {
	sum := sha256.Sum256([]byte(pair.Token + "\x00" + pair.RuntimeRevision))
	return hex.EncodeToString(sum[:])
}

func validRuntimeIssue(issue string) bool {
	return issue == "" || issue == "lazymind_auth_required" || issue == "lazymind_unavailable"
}

// RecordRuntimeStatus preserves the combined action-poll report used by older
// plugins until their profiles reconnect.
func RecordRuntimeStatus(home string, pair Pairing, issue string, mcpReady bool, loaded ...DSHLoad) error {
	if !validRuntimeIssue(issue) {
		return errors.New("invalid dispatcher runtime issue")
	}
	return updateRuntimeStatus(context.Background(), home, pair, nil, func(value *RuntimeStatus, now int64) bool {
		var load DSHLoad
		if len(loaded) > 0 && loaded[0].valid(now) {
			load = loaded[0].sanitize()
		}
		if recentRuntimeWrite(value.CheckedAt, now) && value.Issue == issue && value.MCPReady == mcpReady && value.DSHLoad == load {
			return false
		}
		value.CheckedAt, value.Issue, value.MCPReady, value.DSHLoad = now, issue, mcpReady, load
		return true
	})
}

func RecordRuntimeHeartbeat(ctx context.Context, home string, pair Pairing, identity DSHRuntimeIdentity, mcpReady bool, load DSHLoad) error {
	now := time.Now().UnixMilli()
	if !load.valid(now) || load.Instance != identity.Process {
		return &PairingError{Code: "HOST_PAIRING_INVALID"}
	}
	load = load.sanitize()
	return updateRuntimeStatus(ctx, home, pair, &identity, func(value *RuntimeStatus, now int64) bool {
		if recentRuntimeWrite(value.CheckedAt, now) && value.MCPReady == mcpReady && value.DSHLoad == load {
			return false
		}
		value.CheckedAt, value.MCPReady, value.DSHLoad = now, mcpReady, load
		return true
	})
}

func RecordRuntimeConnection(ctx context.Context, home string, pair Pairing, identity DSHRuntimeIdentity, issue string) error {
	if !validRuntimeIssue(issue) {
		return errors.New("invalid dispatcher runtime issue")
	}
	return updateRuntimeStatus(ctx, home, pair, &identity, func(value *RuntimeStatus, now int64) bool {
		if recentRuntimeWrite(value.ConnectionCheckedAt, now) && value.Issue == issue {
			return false
		}
		value.ConnectionCheckedAt, value.Issue = now, issue
		return true
	})
}

func recentRuntimeWrite(at, now int64) bool {
	return now >= at && now-at < 3000
}

func updateRuntimeStatus(ctx context.Context, home string, pair Pairing, identity *DSHRuntimeIdentity, update func(*RuntimeStatus, int64) bool) error {
	if pair.Provider != DSHProvider || !pair.Enabled {
		if identity != nil {
			return &PairingError{Code: "HOST_PAIRING_INVALID"}
		}
		return nil
	}
	path, err := pairingPath(home, pair.ConnectorID)
	if err != nil {
		return err
	}
	unlock, err := localfile.LockContext(ctx, path+".lock")
	if err != nil {
		return err
	}
	defer unlock()
	current, err := Verify(home, pair.ConnectorID, pair.Token, pair.AccountScope)
	if err == nil && (current.RuntimeRevision != pair.RuntimeRevision || identity != nil && !identity.Current(home, current)) {
		err = &PairingError{Code: "HOST_PAIRING_INVALID"}
	}
	if err != nil {
		// Earlier action polls cannot be re-authenticated by their caller after
		// the request started. Ignore their late result after pairing changed.
		if identity == nil {
			return nil
		}
		return err
	}
	statusPath := path + ".runtime-status.json"
	value, _ := readRuntimeStatus(statusPath, pair)
	if identity == nil && value.Identity != nil && value.Identity.Current(home, pair) {
		// A stale legacy poll must leave the active split observations intact.
		return nil
	}
	if !sameRuntimeIdentity(value.Identity, identity) || value.ConnectorID == "" {
		value = RuntimeStatus{ConnectorID: pair.ConnectorID, Revision: runtimeRevision(pair), Identity: identity}
	}
	if !update(&value, time.Now().UnixMilli()) {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return localfile.Write(statusPath, body, 0600)
}

func sameRuntimeIdentity(a, b *DSHRuntimeIdentity) bool {
	return a == b || a != nil && b != nil && *a == *b
}

func (r RuntimeStatus) Alive(home string, pair Pairing) bool {
	age := time.Now().UnixMilli() - r.CheckedAt
	if age < 0 || age > 12000 {
		return false
	}
	if r.Identity == nil {
		return true
	}
	current, err := Verify(home, pair.ConnectorID, pair.Token, pair.AccountScope)
	return err == nil && r.Revision == runtimeRevision(current) && r.Identity.Current(home, current)
}

func ReadRuntimeStatus(home string, pair Pairing) (RuntimeStatus, error) {
	value, err := ReadLastRuntimeStatus(home, pair)
	if err != nil {
		return RuntimeStatus{}, err
	}
	if !value.Alive(home, pair) {
		return RuntimeStatus{}, errors.New("dispatcher runtime status expired")
	}
	return value, nil
}

// ReadLastRuntimeStatus provides installation history for a still-current
// pairing and configuration. Call ReadRuntimeStatus for liveness.
func ReadLastRuntimeStatus(home string, pair Pairing) (RuntimeStatus, error) {
	path, err := pairingPath(home, pair.ConnectorID)
	if err != nil {
		return RuntimeStatus{}, err
	}
	value, err := readRuntimeStatus(path+".runtime-status.json", pair)
	if err != nil {
		return RuntimeStatus{}, err
	}
	if value.Identity != nil && (value.Identity.PairingRevision != pair.RuntimeRevision ||
		value.Identity.Process != value.Instance || !value.Identity.configurationMatches(home, pair, path)) {
		return RuntimeStatus{}, errors.New("dispatcher configuration or process has changed")
	}
	if value.DSHLoad != (DSHLoad{}) && !value.DSHLoad.valid(value.CheckedAt) {
		return RuntimeStatus{}, errors.New("invalid dispatcher load evidence")
	}
	return value, nil
}

func readRuntimeStatus(path string, pair Pairing) (RuntimeStatus, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return RuntimeStatus{}, err
	}
	var value RuntimeStatus
	if json.Unmarshal(body, &value) != nil || !pair.Enabled || pair.Provider != DSHProvider ||
		value.ConnectorID != pair.ConnectorID || value.Revision != runtimeRevision(pair) || !validRuntimeIssue(value.Issue) {
		return RuntimeStatus{}, errors.New("invalid dispatcher runtime status")
	}
	return value, nil
}
