package codexhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/localfile"
)

const connectionGuide = "LazyMind will show a connection recovery window. Save current work before choosing Restart and connect. LazyMind will reopen the original conversation and hand off the saved operation automatically. END this turn and do not repeat the blocked Workflow request while waiting; follow the recovery handoff after restart."

// ConnectionIssue describes host availability, never Workflow execution failure.
type ConnectionIssue struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	CanRestart bool   `json:"can_restart"`
}

func (e *ConnectionIssue) Error() string { return e.Code + ": " + e.Message }

func connectionIssue(code, message string, restart bool) error {
	return &ConnectionIssue{Code: code, Message: message, CanRestart: restart}
}

var controlProbe = probeControl

// CheckConnection is an execution admission check, not a startup action. It never
// launches or restarts an application and does not trust an old heartbeat file.
func CheckConnection(ctx context.Context, home string) error {
	if !integrationstate.Allowed(home, "codex") {
		return connectionIssue("CODEX_INTEGRATION_DISABLED", "enable the Codex connection in LazyMind before executing a Workflow", false)
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch StartupHealth(bounded, home) {
	case "":
	case "connection_required", "waiting_for_app":
		return connectionIssue("CODEX_CONNECTION_REQUIRED", "Codex session control is not connected. "+connectionGuide, true)
	case "startup_unavailable":
		return connectionIssue("CODEX_CONFIGURATION_UNAVAILABLE", "Codex or its LazyMind connection configuration is unavailable.", false)
	default:
		return connectionIssue("CODEX_CDP_UNAVAILABLE", "Codex session control could not be verified; check for a port conflict or unavailable application.", false)
	}
	c, err := readConfig(home)
	if err != nil {
		return err
	}
	ready, err := controlProbe(bounded, home, c)
	if err != nil {
		return connectionIssue("CODEX_CONTROL_UNAVAILABLE", "Unable to verify Codex session control: "+err.Error(), false)
	}
	if !ready {
		return connectionIssue("CODEX_CONTROL_UNAVAILABLE", "Codex is reachable but its session-control interfaces are not ready or compatible; wait for Codex to finish loading and recheck.", false)
	}
	// Revalidate the endpoint owner after the awaited probe. A previous process's
	// ready result cannot authorize a request against a foreign application.
	application, appErr := startupApplication()
	if appErr != nil || !startupListenerMatches(bounded, application, c.Port) {
		return connectionIssue("CODEX_CDP_UNAVAILABLE", "The Codex connection changed during verification; recheck the current application and debugging port.", false)
	}
	return nil
}

func probeControl(ctx context.Context, home string, c Config) (bool, error) {
	return runProbe(ctx, home, c, "--probe-only")
}

func runProbe(ctx context.Context, home string, c Config, args ...string) (bool, error) {
	bundle, err := codexplugin.DispatcherBundle()
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(bundle)
	script := filepath.Join(home, "codex-workflow", "probe-"+hex.EncodeToString(sum[:8])+".mjs")
	if err := localfile.Write(script, bundle, 0600); err != nil {
		return false, err
	}
	cmd := backgroundCommand(ctx, c.Node, append([]string{script, "--cdp-port", strconv.Itoa(c.Port)}, args...)...)
	cmd.WaitDelay = time.Second
	// No ambient Node preloads may execute inside the trusted read-only probe.
	cmd.Env = agentexec.SafeEnvironment()
	body, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("session-control probe failed: %w", err)
	}
	var result struct {
		Ready bool `json:"ready"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, errors.New("invalid session-control probe response")
	}
	return result.Ready, nil
}
