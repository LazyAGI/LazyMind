package mcpclient

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

// Inspect only the selected host. A running Web profile cannot make Desktop
// appear connected, and disk configuration alone is never an online signal.
func (a *Adapter) inspectDSHConnection(ctx context.Context, status *agentintegration.Status) {
	if status.State != agentintegration.Enabled || status.Issue != nil {
		return
	}
	status.Connection = &agentintegration.ConnectionStatus{MCPConfigured: true,
		RuntimeState: "unavailable", PresentationStatus: "host-offline"}
	target := a
	desktop := a.desktopDSHMode()
	if desktop {
		target = a.forDSHProfile("desktop", "")
	}
	document, err := readYAMLDocument(target.dshConfigPath())
	if err != nil {
		dshConnectionIssue(status, "configuration_unreadable")
		return
	}
	entry := findDSHEntry(document)
	insert := mappingValue(entry, "insert")
	mcpEnabled := entry != nil && scalarValue(entry, "disabled") != "true" && insert != nil && len(insert.Content) == 1 && scalarValue(insert.Content[0], "disabled") != "true"
	var reference string
	for _, row := range yamlSequence(document).Content {
		if scalarValue(row, "id") == dshWorkflowRow {
			reference = scalarValue(mappingValue(row, "config"), "connectorConfig")
			status.Connection.MCPEnabled = mcpEnabled && scalarValue(row, "disabled") != "true"
			break
		}
	}
	config, err := connectorconfig.Load(reference)
	if err != nil || !config.Controlled || config.Home != a.home || config.Provider != string(DeepSeekHarness) || config.Profile != filepath.Dir(target.dshConfigPath()) {
		dshConnectionIssue(status, "configuration_unreadable")
		return
	}
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		dshConnectionIssue(status, "lazymind_unavailable")
		return
	}
	account, err := store.AccountScope()
	if err != nil {
		code := "lazymind_unavailable"
		if credentials.IsAuthenticationRequired(err) {
			code = "lazymind_auth_required"
		}
		dshConnectionIssue(status, code)
		return
	}
	id := strings.TrimSuffix(filepath.Base(config.PairingFile), ".json")
	pair, err := workflowhost.Load(a.home, id, account)
	if err != nil {
		code := workBuddyPairingIssue(err)
		if code == "" {
			code = "pairing_invalid"
		}
		dshConnectionIssue(status, code)
		return
	}
	if pair.Provider != string(DeepSeekHarness) || pair.Profile != config.Profile {
		dshConnectionIssue(status, "pairing_invalid")
		return
	}
	// DSH has no separate trust dialog. This field records the valid scoped
	// pairing, so consumers still require both native enablement and live control.
	status.Connection.MCPTrusted = true
	if !status.Connection.MCPEnabled {
		dshConnectionIssue(status, "mcp_enable_required")
		return
	}
	if live, err := workflowhost.ReadRuntimeStatus(a.home, pair); err == nil {
		if live.Issue != "" {
			dshConnectionIssue(status, live.Issue)
			return
		}
		status.Connection.ControlReady = true
		if !live.MCPReady {
			dshConnectionIssue(status, "mcp_unavailable")
			status.Connection.ControlReady = true
			return
		}
		status.Connection.RuntimeState = "connected"
		status.Connection.PresentationStatus = "available"
		status.Message = "DeepSeek Harness is connected to LazyMind."
		return
	}
	// Process inspection currently establishes absence only on macOS. On other
	// platforms, lack of a dispatcher poll is unavailable rather than evidence
	// that the application has exited.
	if desktop && (runtime.GOOS == "darwin" || a.dshRunning != nil) {
		cli, err := agentexec.FindDSHDesktopCLI()
		if err != nil {
			dshConnectionIssue(status, "process_inspection_failed")
			return
		}
		inspect := a.dshRunning
		if inspect == nil {
			inspect = agentexec.DSHDesktopRunning
		}
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		running, err := inspect(bounded, cli)
		if err != nil {
			dshConnectionIssue(status, "process_inspection_failed")
			return
		}
		if !running {
			status.Connection.RuntimeState = "waiting"
			status.Message = "Start DeepSeek Harness and enable the LazyMind plugin to connect."
			return
		}
	}
	dshConnectionIssue(status, "host_unavailable")
}

func dshConnectionIssue(status *agentintegration.Status, code string) {
	status.Connection.RuntimeState = "unavailable"
	status.Connection.ControlReady = false
	status.Issue = &agentintegration.Issue{Code: code}
	status.Message = "The DeepSeek Harness connection is unavailable. Check the installation guide and retry."
}
