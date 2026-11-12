package codexhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/localfile"
)

const connectionGuide = "LazyMind will show a connection recovery window. Save current work before choosing Restart and connect. LazyMind will reopen the original conversation and hand off the saved operation automatically. END this turn and do not repeat the blocked Workflow request while waiting; follow the recovery handoff after restart."

// ConnectionIssue describes host availability, never Workflow execution failure.
type ConnectionIssue struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	CanRestart bool   `json:"can_restart"`
	cause      error
}

func (e *ConnectionIssue) Error() string { return e.Code + ": " + e.Message }
func (e *ConnectionIssue) Unwrap() error { return e.cause }

func connectionIssue(code, message string, restart bool, cause ...error) error {
	return &ConnectionIssue{Code: code, Message: message, CanRestart: restart, cause: errors.Join(cause...)}
}

var controlProbe = probeControl

// CheckConnection is an execution admission check, not a startup action. It never
// launches or restarts an application and does not trust an old heartbeat file.
func CheckConnection(ctx context.Context, home string) error {
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	switch health := StartupHealth(bounded, home); health {
	case "":
	case "connection_required", "waiting_for_app":
		return connectionIssue("CODEX_CONNECTION_REQUIRED", "Codex session control is not connected. "+connectionGuide, true)
	case "startup_unavailable":
		return connectionIssue("CODEX_CONFIGURATION_UNAVAILABLE", "Codex or its LazyMind connection configuration is unavailable.", false,
			setupError(health, errors.New("Codex startup configuration is unavailable")))
	default:
		return connectionIssue("CODEX_CDP_UNAVAILABLE", "Codex session control could not be verified; check for a port conflict or unavailable application.", false,
			setupError(health, errors.New("Codex startup health check failed")))
	}
	c, err := readConfig(home)
	if err != nil {
		return err
	}
	ready, err := controlProbe(bounded, home, c)
	if err != nil {
		return connectionIssue("CODEX_CONTROL_UNAVAILABLE", "Unable to verify Codex session control; check the connection diagnostics.", false, err)
	}
	if !ready {
		return connectionIssue("CODEX_CONTROL_UNAVAILABLE", "Codex is reachable but its session-control interfaces are not ready or compatible; wait for Codex to finish loading and recheck.", false,
			setupError("capability_unavailable", errors.New("Codex session-control interfaces are not ready")))
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
	if err := ctx.Err(); err != nil {
		return false, err
	}
	bundle, err := codexplugin.DispatcherBundle()
	if err != nil {
		return false, setupError("runtime_unavailable", err)
	}
	sum := sha256.Sum256(bundle)
	script := filepath.Join(home, "codex-workflow", "probe-"+hex.EncodeToString(sum[:8])+".mjs")
	if err := localfile.Write(script, bundle, 0600); err != nil {
		return false, setupError("configuration_failed", err)
	}
	cmd := backgroundCommand(ctx, c.Node, append([]string{script, "--cdp-port", strconv.Itoa(c.Port)}, args...)...)
	cmd.WaitDelay = time.Second
	// No ambient Node preloads may execute inside the trusted read-only probe.
	cmd.Env = agentexec.SafeEnvironment()
	body, err := cmd.Output()
	if err != nil {
		code := "runtime_unavailable"
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = "cdp_unavailable"
		}
		return false, setupError(code, fmt.Errorf("session-control probe failed: %w", errors.Join(ctx.Err(), err)))
	}
	control := false
	for _, arg := range args {
		control = control || arg == "--probe-only"
	}
	return decodeProbeResult(body, control)
}

func decodeProbeResult(body []byte, control bool) (bool, error) {
	var result struct {
		Ready *bool  `json:"ready"`
		Issue string `json:"issue"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, setupError("cdp_unavailable", fmt.Errorf("invalid session-control probe response: %w", err))
	}
	if result.Ready == nil {
		return false, setupError("cdp_unavailable", errors.New("session-control probe response has no readiness value"))
	}
	if control {
		// Only our stable categories cross the setup boundary. Unknown output is
		// invalid evidence, not a renderer message to display or optimistic success.
		switch result.Issue {
		case "cdp_unavailable", "control_window_unavailable", "capability_unavailable":
			return false, setupError(result.Issue, errors.New("Codex session-control probe is not ready"))
		case "":
			if !*result.Ready {
				return false, setupError("capability_unavailable", errors.New("Codex session-control interfaces are not ready"))
			}
		default:
			return false, setupError("cdp_unavailable", errors.New("session-control probe returned an unknown issue"))
		}
	}
	return *result.Ready, nil
}
