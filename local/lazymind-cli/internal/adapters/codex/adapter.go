package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/mcpbridge"
	"lazymind/agentconnector/internal/workflowhost"
)

const (
	serverName         = "lazymind"
	commandTimeout     = 5 * time.Second
	interactiveTimeout = 2 * time.Minute
)

var prepareCodexDesktop = codexhost.PrepareDesktopForInstallation
var waitCodexReady = codexhost.WaitReady
var cleanupCodexPresentation = codexhost.CleanupPresentation

type Adapter struct {
	binary         string
	configured     string
	discoveryError error
	self           string
	bridge         *mcpbridge.Bridge
	home           string
	hostID         string
}

type mcpConfig struct {
	Enabled   bool
	Transport transport
}

type transport struct {
	Command string
	Args    []string
	Env     map[string]string
}

type tomlMCPConfig struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	Enabled *bool             `toml:"enabled"`
}

func New(binary, self string, bridge *mcpbridge.Bridge) (*Adapter, error) {
	if bridge == nil {
		return nil, errors.New("MCP bridge is required")
	}
	resolvedBinary, discoveryError := findBinary(binary)
	if strings.TrimSpace(self) == "" {
		var err error
		self, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve LazyMind executable: %w", err)
		}
	}
	resolvedSelf, err := agentexec.ResolveExecutable(self)
	if err != nil {
		return nil, fmt.Errorf("resolve LazyMind executable: %w", err)
	}
	home, err := agentexec.LazyMindHome()
	if err != nil {
		return nil, err
	}
	hostID, err := agentexec.PersistentHostID()
	if err != nil {
		return nil, fmt.Errorf("resolve LazyMind Agent Host identity: %w", err)
	}
	return &Adapter{
		binary: resolvedBinary, configured: binary, discoveryError: discoveryError,
		self: resolvedSelf, bridge: bridge, home: home, hostID: hostID,
	}, nil
}

var startupHealth = codexhost.StartupHealth

func (a *Adapter) Status(ctx context.Context) (status agentintegration.Status) {
	status = a.status(ctx)
	details := a.inspectConfiguration(&status)
	defer func() { agentintegration.RecordInspection(a.home, a.self, details, &status) }()
	if status.Enabled != nil && *status.Enabled && status.State == agentintegration.Enabled {
		agentintegration.SetRuntimeIssue(&status, startupHealth(ctx, a.home))
	}
	return status
}

func (a *Adapter) status(context.Context) agentintegration.Status {
	desktop, err := codexDesktopState()
	if err != nil {
		return agentintegration.Fail(agentintegration.Status{Agent: "codex", DisplayName: "Codex"}, err.Error())
	}
	requirements := []agentintegration.Requirement{
		{ID: "codex_desktop", Description: "Install the ChatGPT desktop app with Codex.", Satisfied: desktop.Installed},
		{ID: "codex_desktop_initialized", Description: "Open Codex in the ChatGPT desktop app at least once.", Satisfied: desktop.Initialized},
		{ID: "codex_cli", Description: "Locate the Codex CLI bundled with the ChatGPT desktop app.", Satisfied: a.discoveryError == nil},
	}
	status := agentintegration.Status{
		Agent: "codex", DisplayName: "Codex", Requirements: requirements,
	}
	config, exists, err := a.getConfig()
	if err != nil {
		return agentintegration.FailError(status, err)
	}
	switch {
	case exists && !a.isOwned(config):
		status.State = agentintegration.Conflict
		status.Message = "Codex already has an MCP server named `lazymind` that is not managed by this LazyMind installation."
	case !exists && a.pluginEnabled():
		status.State = agentintegration.Enabled
		status.Message = "LazyMind Workflow plugin is installed. Open a new Codex task to load its tools."

	case agentintegration.MissingRequirement(requirements):
		status.State = agentintegration.RequirementsMissing
	default:
		status.State = agentintegration.Ready
		if exists {
			status.Message = "Reconnect to upgrade the existing MCP entry to the LazyMind Workflow plugin."
		}
	}
	if status.State == agentintegration.Enabled {
		store, err := credentials.NewStore(a.home, "")
		ready := false
		presentation := "unavailable"
		issue := "host_unavailable"
		if err == nil {
			scope, e := store.AccountScope()
			if e == nil {
				if current, statusErr := codexhost.ReadStatus(a.home, scope); statusErr == nil {
					issue = codexhost.StatusIssue(current)
					ready = issue == ""
					presentation = current.Presentation.Status
				}
			}
		}
		status.Connection = &agentintegration.ConnectionStatus{MCPConfigured: true, MCPEnabled: true, MCPTrusted: true, ControlReady: ready, PresentationStatus: presentation}
		if !ready {
			status.Issue = &agentintegration.Issue{Code: issue}
			status.Message = "Codex control is disconnected. Open its installation guide in LazyMind; save current work before confirming a connection or restart."
		}
	}
	return status
}

func (a *Adapter) Connect(ctx context.Context) (result agentintegration.Status) {
	operation := agentintegration.NewOperation(a.home, "codex", "connect", a.self)
	operation.Enter("configuration")
	defer func() {
		if result.State == agentintegration.Failed {
			result.RetryAction = "connect"
		}
		operation.After = a.inspectConfiguration(&result)
		if result.State == agentintegration.Failed {
			result.RetryAction = "connect"
			if result.Issue == nil {
				result.Issue = &agentintegration.Issue{Code: "configuration_failed"}
			}
		}
		operation.Finish(&result)
	}()
	profile, err := codexHome()
	if err != nil {
		return agentintegration.FailError(agentintegration.Status{Agent: "codex", DisplayName: "Codex"}, err)
	}
	if err = os.MkdirAll(profile, 0700); err != nil {
		return agentintegration.FailError(agentintegration.Status{Agent: "codex", DisplayName: "Codex"}, err)
	}
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	unlock, err := localfile.LockContext(bounded, filepath.Join(profile, ".lazymind-integration.lock"))
	cancel()
	if err != nil {
		return agentintegration.FailError(agentintegration.Status{Agent: "codex", DisplayName: "Codex"}, err)
	}
	defer unlock()

	if err := CleanupLegacyControl(); err != nil {
		return agentintegration.Fail(a.Status(ctx), "clean up legacy Codex control configuration: "+err.Error())
	}
	status := a.Status(ctx)
	operation.Before = a.inspectConfiguration(&status)
	if status.Enabled == nil || agentintegration.MissingRequirement(status.Requirements) {
		return status
	}
	status.Issue = nil

	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		return agentintegration.FailError(status, err)
	}
	account, err := store.AccountScope()
	if err != nil {
		return agentintegration.FailError(status, err)
	}
	// Saved registration determines the switch state. Reusing the installation
	// also requires an enabled pairing for the currently signed-in account.
	if status.State == agentintegration.Enabled {
		if record, e := codexplugin.Read(); e == nil && record.BuildID == codexplugin.BuildID() && workflowhost.Configured(a.home, record.ConnectorID, account) {
			if e = codexhost.Configure(a.home, record.Node, record.PairingFile); e != nil {
				return agentintegration.Fail(status, e.Error())
			}
			return a.connectDesktop(ctx, account, status, operation)
		}
	}
	// Installation is local and uses the saved account scope. A temporarily
	// offline Core must not prevent installing the plugin; MCP checks service
	// connectivity and authentication when Codex loads it.
	if err := a.prepareRuntime(ctx); err != nil {
		return agentintegration.FailError(status, err)
	}
	record, err := codexplugin.Install(ctx, a.home, a.self, a.binary, profile, a.hostID)
	if err != nil {
		return agentintegration.Fail(status, "install LazyMind Workflow plugin: "+err.Error())
	}
	if err := codexhost.Configure(a.home, record.Node, record.PairingFile); err != nil {
		return agentintegration.FailError(status, err)
	}
	// Finish configuration before restarting so Codex loads the replacement only.
	if _, exists, err := a.getConfig(); err != nil {
		return agentintegration.FailError(status, err)
	} else if exists {
		if _, err := a.run(ctx, "mcp", "remove", serverName); err != nil {
			_, _ = a.run(ctx, "plugin", "remove", codexplugin.Name+"@"+record.Marketplace, "--json")
			return agentintegration.Fail(status, "remove legacy MCP entry: "+err.Error())
		}
	}
	return a.connectDesktop(ctx, account, status, operation)
}

func (a *Adapter) connectDesktop(ctx context.Context, account string, status agentintegration.Status, operation *agentintegration.Operation) agentintegration.Status {
	setup, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	operation.Enter("restart")
	if err := prepareCodexDesktop(setup, a.home); err != nil {
		return codexConnectionFailure(status, err)
	}
	since := time.Now()
	current := a.Status(setup)
	// A user-disabled plugin cannot produce a heartbeat until the enable step.
	// Preserve that explicit choice and let the guide explain how to enable it.
	if current.Connection != nil && current.Connection.MCPConfigured && !current.Connection.MCPEnabled {
		return current
	}
	operation.Enter("control")
	if err := waitCodexReady(setup, a.home, account, since); err != nil {
		return codexConnectionFailure(current, err)
	}
	operation.Enter("verify")
	return a.Status(setup)
}

// Refresh updates an already enabled, owned plugin without performing desktop
// lifecycle control. Connect is the explicit install/restart flow; it must not
// be reused as a harmless refresh while a user is working in Codex.
func (a *Adapter) Refresh(ctx context.Context) (result agentintegration.Status) {
	defer func() {
		details := a.inspectConfiguration(&result)
		agentintegration.RecordInspection(a.home, a.self, details, &result)
	}()
	status := a.status(ctx)
	profile, err := codexHome()
	if err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	record, err := codexplugin.Read()
	if err != nil || record.Home != a.home || record.Profile != profile {
		return agentintegration.Fail(status, "Refresh requires an existing LazyMind Workflow plugin owned by this installation and Codex profile.")
	}
	if enabled, err := codexplugin.Enabled(profile, record.Marketplace); err != nil || !enabled {
		return agentintegration.Fail(status, "Refresh requires an enabled LazyMind Workflow plugin; use the installation guide to connect it first.")
	}
	if config, exists, err := a.getConfig(); err != nil {
		return agentintegration.Fail(status, err.Error())
	} else if exists && !a.isOwned(config) {
		return agentintegration.Fail(status, "Refresh cannot replace a lazymind MCP entry owned by another installation.")
	}
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	account, err := store.AccountScope()
	if err != nil || !workflowhost.Configured(a.home, record.ConnectorID, account) {
		return agentintegration.Fail(status, "Refresh requires the existing pairing for the signed-in LazyMind account.")
	}
	if err := a.prepareRuntime(ctx); err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	record, err = codexplugin.Install(ctx, a.home, a.self, a.binary, profile, a.hostID)
	if err != nil {
		return agentintegration.Fail(status, "refresh LazyMind Workflow plugin: "+err.Error())
	}
	if err := codexhost.Configure(a.home, record.Node, record.PairingFile); err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	// An earlier Connect may have installed the plugin but stopped before legacy
	// migration when the desktop declined to restart. Remove only our own old
	// entry after successful refresh; this CLI edit does not control the desktop.
	if config, exists, err := a.getConfig(); err != nil {
		return agentintegration.Fail(status, err.Error())
	} else if exists {
		if !a.isOwned(config) {
			return agentintegration.Fail(status, "Plugin refreshed, but the lazymind MCP entry changed ownership; it was preserved.")
		}
		if _, err := a.run(ctx, "mcp", "remove", serverName); err != nil {
			return agentintegration.Fail(status, "Plugin refreshed, but its legacy MCP entry could not be removed: "+err.Error())
		}
	}
	status = a.status(ctx)
	if status.State == agentintegration.Enabled {
		status.Message = "LazyMind Workflow plugin files were refreshed without restarting Codex. Open a new task to load the updated skills and MCP tools; existing tasks may keep their current tool processes."
	}
	return status
}

func (a *Adapter) Disconnect(ctx context.Context) (result agentintegration.Status) {
	operation := agentintegration.NewOperation(a.home, "codex", "disconnect", a.self)
	operation.Enter("disconnect")
	defer func() {
		if result.State == agentintegration.Failed {
			result.RetryAction = "disconnect"
		}
		operation.After = a.inspectConfiguration(&result)
		if result.State == agentintegration.Failed {
			result.RetryAction = "disconnect"
			if result.Issue == nil {
				result.Issue = &agentintegration.Issue{Code: "configuration_failed"}
			}
		}
		operation.Finish(&result)
	}()
	profile, err := codexHome()
	if err != nil {
		return agentintegration.FailError(agentintegration.Status{Agent: "codex", DisplayName: "Codex"}, err)
	}
	if err = os.MkdirAll(profile, 0700); err != nil {
		return agentintegration.FailError(agentintegration.Status{Agent: "codex", DisplayName: "Codex"}, err)
	}
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	unlock, err := localfile.LockContext(bounded, filepath.Join(profile, ".lazymind-integration.lock"))
	cancel()
	if err != nil {
		return agentintegration.FailError(agentintegration.Status{Agent: "codex", DisplayName: "Codex"}, err)
	}
	defer unlock()

	if err := CleanupLegacyControl(); err != nil {
		return agentintegration.Fail(a.Status(ctx), "clean up legacy Codex control configuration: "+err.Error())
	}
	status := a.Status(ctx)
	operation.Before = a.inspectConfiguration(&status)
	if status.Enabled == nil {
		return status
	}
	status.Issue = nil
	status.State = agentintegration.Ready
	if record, err := codexplugin.Read(); err == nil {
		installed, _, err := codexplugin.Registration(profile, record.Marketplace)
		if err != nil {
			return agentintegration.FailError(status, err)
		}
		if record.Home == a.home {
			if err := workflowhost.Disable(a.home, record.ConnectorID); err != nil && !errors.Is(err, os.ErrNotExist) {
				return agentintegration.FailError(status, err)
			}
		}
		if installed {
			if _, err := a.run(ctx, "plugin", "remove", codexplugin.Name+"@"+record.Marketplace, "--json"); err != nil {
				return agentintegration.Fail(status, "remove Codex Workflow plugin: "+err.Error())
			}
		}
		if err := cleanupCodexPresentation(a.home); err != nil {
			return agentintegration.FailError(status, err)
		}

	}

	if _, exists, err := a.getConfig(); err != nil {
		return agentintegration.FailError(status, err)
	} else if exists {
		if _, err := a.run(ctx, "mcp", "remove", serverName); err != nil {
			return agentintegration.Fail(status, "remove Codex MCP configuration: "+err.Error())
		}
	}
	return a.Status(ctx)
}

func (a *Adapter) Login(ctx context.Context) agentintegration.Status {
	status := a.Status(ctx)
	if err := a.prepareRuntime(ctx); err != nil {
		return agentintegration.FailError(status, err)
	}
	loginCtx, cancel := context.WithTimeout(ctx, interactiveTimeout)
	defer cancel()
	if _, err := agentexec.Run(loginCtx, a.binary, "login"); err != nil {
		return agentintegration.Fail(status, "Codex login failed: "+err.Error())
	}
	if ready, reason := availability(a.binary); !ready {
		return agentintegration.Fail(status, reason)
	}
	return a.Status(ctx)
}

func codexDesktopState() (agentexec.DesktopApplicationState, error) {
	home, err := codexHome()
	if err != nil {
		return agentexec.DesktopApplicationState{}, err
	}
	spec := codexDesktopApplication()
	spec.StatePaths = []string{home}
	return agentexec.InspectDesktopApplication(spec)
}

func codexDesktopApplication() agentexec.DesktopApplication {
	return agentexec.DesktopApplication{
		BindingTarget:   agentexec.CodexDesktop,
		PackageNames:    []string{"OpenAI.Codex"},
		ExecutableNames: []string{"ChatGPT.exe", "Codex.exe"},
		Protocols:       []string{"chatgpt", "codex"},
		DisplayNames:    []string{"ChatGPT", "Codex"},
	}
}

func (a *Adapter) getConfig() (mcpConfig, bool, error) {
	home, err := codexHome()
	if err != nil {
		return mcpConfig{}, false, err
	}
	body, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return mcpConfig{}, false, nil
	}
	if err != nil {
		return mcpConfig{}, false, fmt.Errorf("read Codex MCP configuration: %w", err)
	}
	var document struct {
		MCPServers map[string]tomlMCPConfig `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(body, &document); err != nil {
		return mcpConfig{}, false, fmt.Errorf("decode Codex MCP configuration: %w", err)
	}
	configured, exists := document.MCPServers[serverName]
	if !exists {
		return mcpConfig{}, false, nil
	}
	enabled := configured.Enabled == nil || *configured.Enabled
	return mcpConfig{
		Enabled:   enabled,
		Transport: transport{Command: configured.Command, Args: configured.Args, Env: configured.Env},
	}, true, nil
}

func (a *Adapter) run(ctx context.Context, arguments ...string) (string, error) {
	if err := a.prepareRuntime(ctx); err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return agentexec.Run(runCtx, a.binary, arguments...)
}

func (a *Adapter) isOwned(config mcpConfig) bool {
	if reference := config.Transport.Env[connectorconfig.EnvironmentKey]; reference != "" {
		record, err := connectorconfig.Load(reference)
		if err != nil || record.Home != a.home || record.HostID != a.hostID || record.Provider != "codex" {
			return false
		}
	}
	return agentexec.SameExecutable(config.Transport.Command, a.self) &&
		len(config.Transport.Args) == 2 &&
		config.Transport.Args[0] == "mcp" && config.Transport.Args[1] == "proxy"
}

func codexHome() (string, error) {
	if configured := strings.TrimSpace(connectorconfig.Value("CODEX_HOME")); configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func (a *Adapter) pluginEnabled() bool {
	record, err := codexplugin.Read()
	if err != nil || record.Home != a.home || record.BuildID != codexplugin.BuildID() || !agentexec.SameExecutable(record.Self, a.self) {
		return false
	}
	profile, err := codexHome()
	if err != nil || record.Profile != profile {
		return false
	}
	enabled, err := codexplugin.Enabled(profile, record.Marketplace)
	if err != nil || !enabled {
		return false
	}
	return true
}
