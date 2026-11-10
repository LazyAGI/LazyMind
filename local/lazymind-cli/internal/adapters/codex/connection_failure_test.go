package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/desktopsetup"
	"lazymind/agentconnector/internal/workflowhost"
)

func TestPluginReconnectWaitsForFreshControlAndPreservesDiagnostics(t *testing.T) {
	adapter, store, prepares := newPluginReconnectTestAdapter(t)
	account, err := store.AccountScope()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	waits := 0
	waitCodexReady = func(ctx context.Context, home, scope string, since time.Time) error {
		waits++
		deadline, ok := ctx.Deadline()
		if *prepares != 2 || home != adapter.home || scope != account || since.Before(started) || !ok || time.Until(deadline) > 60*time.Second {
			t.Fatalf("control wait must follow this restart with current account and bounded deadline: prepares=%d", *prepares)
		}
		return &codexhost.SetupError{Code: "capability_unavailable", Err: context.DeadlineExceeded}
	}
	status := adapter.Connect(context.Background())
	if waits != 1 || status.State != agentintegration.Failed || status.Enabled == nil || !*status.Enabled || status.Issue == nil ||
		status.Issue.Code != "capability_unavailable" || status.Issue.Stage != "control" || status.DiagnosticID == "" || status.RetryAction != "connect" {
		t.Fatalf("control failure lost setup state: %+v", status)
	}
	log, err := os.ReadFile(filepath.Join(adapter.home, "logs", "agent-setup.log"))
	if err != nil || !strings.Contains(string(log), `"stage":"control"`) || !strings.Contains(string(log), `"code":"capability_unavailable"`) {
		t.Fatalf("missing control-stage diagnostics: %v", err)
	}
}

func TestPluginReconnectDoesNotWaitForUserDisabledPlugin(t *testing.T) {
	adapter, _, prepares := newPluginReconnectTestAdapter(t)
	record, err := codexplugin.Read()
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(record.Profile, "config.toml")
	body, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(strings.ReplaceAll(string(body), "enabled = true", "enabled = false")), 0600); err != nil {
		t.Fatal(err)
	}
	waitCodexReady = func(context.Context, string, string, time.Time) error {
		t.Fatal("user must enable the plugin before its worker can start")
		return nil
	}
	status := adapter.Connect(context.Background())
	if *prepares != 2 || status.State != agentintegration.Enabled || status.Connection == nil || status.Connection.MCPEnabled || status.Issue == nil || status.Issue.Code != "mcp_enable_required" {
		t.Fatalf("lost enable guidance: %+v", status)
	}
	prepareCodexDesktop = func(context.Context, string) error { return desktopsetup.ErrQuitRequired }
	status = adapter.Connect(context.Background())
	if status.State != agentintegration.ActionRequired || status.Action == nil || status.Action.Kind != "quit_agent" || status.Issue == nil || status.Issue.Code != "quit_required" {
		t.Fatalf("disabled plugin masked a refused restart: %+v", status)
	}
}

func TestCodexConnectionFailureClassifiesWithoutRawOutput(t *testing.T) {
	for _, sample := range []struct {
		err  error
		code string
	}{
		{&codexhost.SetupError{Code: "control_port_conflict", Err: errors.New("secret=fixture")}, "control_port_conflict"},
		{&codexhost.SetupError{Code: "launch_failed", Err: context.Canceled}, "operation_cancelled"},
		{&workflowhost.PairingError{Code: "HOST_PAIRING_DISABLED"}, "pairing_disabled"},
		{os.ErrPermission, "configuration_failed"},
		{context.DeadlineExceeded, "startup_timeout"},
		{desktopsetup.ErrQuitRequired, "quit_required"},
	} {
		t.Run(sample.code, func(t *testing.T) {
			status := codexConnectionFailure(agentintegration.Status{Agent: "codex", DisplayName: "Codex"}, sample.err)
			if status.Issue == nil || status.Issue.Code != sample.code || strings.Contains(status.Message, "secret=") {
				t.Fatalf("unexpected failure: %+v", status)
			}
			if sample.code == "quit_required" && (status.State != agentintegration.ActionRequired || status.Action == nil || status.Action.Kind != "quit_agent") {
				t.Fatalf("lost normal-quit retry: %+v", status)
			}
		})
	}
}
