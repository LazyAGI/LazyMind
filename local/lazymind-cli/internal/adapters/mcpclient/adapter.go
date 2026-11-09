package mcpclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	workbuddyadapter "lazymind/agentconnector/internal/adapters/workbuddy"
	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/mcpbridge"
	"lazymind/agentconnector/internal/workbuddyhost"
)

type Kind string

const (
	Cursor          Kind = "cursor"
	WorkBuddy       Kind = "workbuddy"
	Raccoon         Kind = "raccoon"
	TRAEWork        Kind = "traework"
	DeepSeekHarness Kind = "deepseek-harness"
	serverName           = "lazymind"
)

type Adapter struct {
	dshTarget    string // Set only on a profile-scoped copy of the adapter.
	dshCLI       string
	dshMCPReady  bool
	dshInspector func(context.Context, string) (dshRuntimeInspection, error)
	dshRunner    func(context.Context, string, ...string) error
	dshRunning   func(context.Context, string) (bool, error)
	kind         Kind
	self         string
	bridge       interface {
		Probe(context.Context) (mcpbridge.ProbeResult, error)
	}
	home   string
	hostID string
}

type stdioMCPDefinition struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

func New(kind Kind, self string, bridge *mcpbridge.Bridge) (*Adapter, error) {
	if bridge == nil {
		return nil, fmt.Errorf("MCP bridge is required")
	}
	if _, err := displayName(kind); err != nil {
		return nil, err
	}
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
	return &Adapter{kind: kind, self: resolvedSelf, bridge: bridge, home: home, hostID: hostID}, nil
}

var workBuddyStartupHealth = workbuddyhost.StartupHealth
var workBuddyAuthentication = workbuddyadapter.AuthenticationStatus

func (a *Adapter) Status(ctx context.Context) (status agentintegration.Status) {
	status = a.status(ctx)
	details := a.inspectConfiguration(&status)
	if a.kind == DeepSeekHarness {
		a.inspectDSHConnection(ctx, &status)
	}
	defer func() { agentintegration.RecordInspection(a.home, a.self, details, &status) }()
	if a.kind == WorkBuddy {
		status.CanConnect = status.Enabled != nil && !agentintegration.MissingRequirement(status.Requirements) && len(status.Requirements) > 0
		if status.Enabled != nil && *status.Enabled && status.State == agentintegration.Enabled {
			store, err := credentials.NewStore(a.home, "")
			if err == nil {
				account, err := store.AccountScope()
				if err == nil {
					config, _, err := workbuddyhost.Load(a.home, account)
					if err == nil {
						issue, message := status.Issue, status.Message
						agentintegration.SetRuntimeIssue(&status, workBuddyStartupHealth(ctx, a.home, config.CDPPort))
						// Native trust and enablement are prerequisites, independent of runtime health.
						if issue != nil && (issue.Code == "mcp_trust_required" || issue.Code == "mcp_enable_required") {
							status.Issue, status.Message = issue, message
						}
					}
				}
			}
		}
	}
	return status
}

func (a *Adapter) Connect(ctx context.Context) (result agentintegration.Status) {
	operation := agentintegration.NewOperation(a.home, string(a.kind), "connect", a.self)
	operation.Enter("inspect")
	defer func() {
		if result.State == agentintegration.Failed {
			result.RetryAction = "connect"
		}
		operation.After = a.inspectConfiguration(&result)
		if a.kind == DeepSeekHarness {
			a.inspectDSHConnection(ctx, &result)
		}
		if result.State == agentintegration.Failed {
			result.RetryAction = "connect"
			if result.Issue == nil {
				result.Issue = &agentintegration.Issue{Code: "configuration_failed"}
			}
		}
		operation.Finish(&result)
	}()
	defer func() {
		if a.kind == WorkBuddy && result.State == agentintegration.Failed {
			if result.Issue == nil {
				code := map[string]string{"inspect": "configuration_failed", "probe": "lazymind_unavailable", "pairing": "pairing_required", "configuration": "configuration_failed", "restart": "launch_failed", "control": "host_unavailable"}[operation.Stage]
				if code == "" {
					code = "operation_failed"
				}
				result.Issue = &agentintegration.Issue{Code: code}
			}
			sanitizeWorkBuddyFailure(&result)
			// Installation may have written the owned entry before startup failed.
			if state, err := readManagedConfig(a.kind, configPath(a.kind), a.self, a.home, a.hostID); err == nil {
				result.CanDisconnect = state.configured
			}
		}
	}()
	if a.desktopDSHMode() {
		snapshot := agentintegration.Status{Agent: string(a.kind)}
		operation.Before = a.inspectConfiguration(&snapshot)
		return a.connectDSHDesktop(ctx)
	}
	// Bound the complete setup, including Core probe and runtime discovery.
	if a.kind == WorkBuddy {
		bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		ctx = bounded
		unlock, err := lockWorkBuddyOperation(ctx, a.home)
		if err != nil {
			return workBuddyFailure(agentintegration.Status{Agent: "workbuddy", DisplayName: "WorkBuddy"}, workBuddyLockError(err))
		}
		defer unlock()
	}
	status := a.Status(ctx)
	operation.Before = a.inspectConfiguration(&status)
	if status.Enabled == nil || agentintegration.MissingRequirement(status.Requirements) {
		return status
	}
	status.Issue = nil
	status.State = agentintegration.Ready
	operation.Enter("probe")
	if _, err := a.bridge.Probe(ctx); err != nil {
		if a.kind == WorkBuddy {
			return workBuddyFailure(status, err)
		}
		return agentintegration.Fail(status, "LazyMind MCP is unavailable: "+err.Error())
	}
	if a.kind == Cursor {
		installURL, err := a.cursorInstallURL()
		if err != nil {
			return agentintegration.FailError(status, err)
		}
		status.State = agentintegration.ActionRequired
		status.Action = &agentintegration.Action{
			Kind: "open_url", URL: installURL,
		}
		status.Message = "Approve the LazyMind MCP installation in Cursor, then check again."
		return status
	}
	if a.kind == WorkBuddy {
		operation.Enter("compatibility")
		if err := workbuddyhost.Preflight(); err != nil {
			return workBuddyFailure(status, err)
		}
		operation.Enter("login")
		auth := workBuddyAuthentication()
		if auth.LoginRequired {
			status.State = agentintegration.ActionRequired
			status.Action = &agentintegration.Action{Kind: "login"}
			status.Issue = &agentintegration.Issue{Code: "workbuddy_login_required"}
			status.Message = "Sign in to WorkBuddy to continue the connection."
			return status
		}
		if !auth.Ready {
			return workBuddyFailure(status, &workbuddyhost.SetupError{Code: "workbuddy_auth_unreadable", Err: errors.New(auth.Message)})
		}
		operation.Enter("pairing")
		store, err := credentials.NewStore(a.home, "")
		if err != nil {
			return workBuddyFailure(status, err)
		}
		account, err := store.AccountScope()
		if err != nil {
			return workBuddyFailure(status, &workbuddyhost.SetupError{Code: "lazymind_auth_required", Err: err})
		}
		if err := workbuddyhost.Install(ctx, a.home, userPath(".workbuddy"), account); err != nil {
			return workBuddyFailure(status, err)
		}
	}
	var unlockPatch func()
	defer func() {
		if unlockPatch != nil {
			unlockPatch()
		}
	}()
	controlled := false
	if a.kind == DeepSeekHarness {
		var err error
		controlled, err = a.installDSHWorkflow(ctx, func() error {
			if err := os.MkdirAll(filepath.Dir(a.dshConfigPath()), 0700); err != nil {
				return err
			}
			unlockPatch, err = lockDSHPatch(ctx, a.dshConfigPath())
			return err
		})
		if err != nil {
			return agentintegration.FailError(status, err)
		}
	}
	operation.Enter("configuration")
	if err := writeManagedConfig(a.kind, configPath(a.kind), a.self, a.home, a.hostID, controlled); err != nil {
		if a.kind == WorkBuddy {
			return workBuddyFailure(status, err)
		}
		return agentintegration.FailError(status, err)
	}
	if a.kind == WorkBuddy {
		setup, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		store, err := credentials.NewStore(a.home, "")
		if err != nil {
			return workBuddyFailure(status, err)
		}
		account, err := store.AccountScope()
		if err != nil {
			return workBuddyFailure(status, err)
		}
		config, _, err := workbuddyhost.Load(a.home, account)
		if err != nil {
			return workBuddyFailure(status, err)
		}
		// Reconnect also reloads the newly written MCP configuration.
		operation.Enter("restart")
		if err := workbuddyhost.PrepareDesktop(setup, config.CDPPort, true); err != nil {
			return workBuddyFailure(status, err)
		}
		operation.Enter("control")
		if err := workbuddyhost.WaitReady(setup, a.home, account, time.Now()); err != nil {
			return workBuddyFailure(status, err)
		}
	}
	operation.Enter("verify")
	return a.status(ctx)
}

func (a *Adapter) Disconnect(ctx context.Context) (result agentintegration.Status) {
	operation := agentintegration.NewOperation(a.home, string(a.kind), "disconnect", a.self)
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
	if a.kind == WorkBuddy {
		unlock, err := lockWorkBuddyOperation(ctx, a.home)
		if err != nil {
			return workBuddyFailure(agentintegration.Status{Agent: "workbuddy", DisplayName: "WorkBuddy"}, workBuddyLockError(err))
		}
		defer unlock()
	}
	if a.desktopDSHMode() {
		snapshot := agentintegration.Status{Agent: string(a.kind)}
		operation.Before = a.inspectConfiguration(&snapshot)
		return a.disconnectDSHDesktop(ctx)
	}
	status := a.Status(ctx)
	operation.Before = a.inspectConfiguration(&status)
	if status.Enabled == nil {
		return status
	}
	status.Issue = nil
	status.State = agentintegration.Ready
	if a.kind == DeepSeekHarness {
		if err := os.MkdirAll(filepath.Dir(a.dshConfigPath()), 0700); err != nil {
			return agentintegration.FailError(status, err)
		}
		unlock, err := lockDSHPatch(ctx, a.dshConfigPath())
		if err != nil {
			return agentintegration.FailError(status, err)
		}
		defer unlock()
	}
	if err := removeManagedConfig(a.kind, configPath(a.kind)); err != nil {
		return agentintegration.FailError(status, err)
	}
	if a.kind == DeepSeekHarness {
		if err := a.disconnectDSHWorkflow(); err != nil {
			return agentintegration.FailError(status, err)
		}
	}
	if a.kind == WorkBuddy {
		if err := workbuddyhost.DisableLocal(a.home); err != nil {
			return agentintegration.FailError(status, err)
		}
	}

	return a.status(ctx)
}

func (a *Adapter) status(ctx context.Context) agentintegration.Status {
	if a.desktopDSHMode() {
		return a.dshDesktopStatus(ctx)
	}
	name, _ := displayName(a.kind)
	status := agentintegration.Status{
		Agent: string(a.kind), DisplayName: name,
	}
	requirements, err := requirements(a.kind)
	if err != nil {
		return agentintegration.FailError(status, err)
	}
	status.Requirements = requirements
	state, err := readManagedConfig(a.kind, configPath(a.kind), a.self, a.home, a.hostID)
	if err != nil {
		return agentintegration.FailError(status, err)
	}
	if a.kind == WorkBuddy {
		status.CanDisconnect = state.configured
	}
	if a.kind == DeepSeekHarness && state.configured && state.owned && state.current {
		if !a.dshWorkflowInstalled() {
			state.current = false
			status.Message = "MCP is configured; reconnect to install or update the Workflow panel and host adapter."
			if a.dshWorkflowNeedsUpdate() {
				status.Issue = &agentintegration.Issue{Code: "plugin_update_required"}
				status.Message = "The installed LazyMind plugin needs an update. Reconnect to update its components."
			}
		} else {
			status.Message = "MCP and the Workflow bundle are configured. Restart DSH to activate updated plugins."
		}
	}
	if a.kind == WorkBuddy && state.configured && state.owned && state.current {
		state.current = false
		status.Connection = &agentintegration.ConnectionStatus{MCPConfigured: true, PresentationStatus: "host-offline"}
		status.Connection.MCPTrusted, status.Connection.MCPEnabled, err = workbuddySetup(configPath(a.kind))
		if err != nil {
			status.Issue = &agentintegration.Issue{Code: "configuration_unreadable"}
			return agentintegration.Fail(status, "Unable to read WorkBuddy trust configuration.")
		}
		status.Issue = &agentintegration.Issue{Code: "pairing_required"}
		status.Message = "Enable the connection to start WorkBuddy and connect Workflow session control."
		store, err := credentials.NewStore(a.home, "")
		if err == nil {
			account, scopeErr := store.AccountScope()
			config, pair, configErr := workbuddyhost.Load(a.home, account)
			if scopeErr != nil || configErr != nil {
				state.current = false
				if scopeErr != nil {
					status.Issue.Code = "lazymind_auth_required"
				} else if code := workBuddyPairingIssue(configErr); code != "" {
					status.Issue.Code = code
				}
				status.Message = workBuddyIssueMessage(status.Issue.Code)
			} else {
				status.Issue = nil
				// A temporary outage must not turn the user's enabled switch off
				// or prevent disconnecting. Health is reported independently.
				state.current = true
				status.Message = ""
				if host, err := workbuddyhost.ReadStatus(config, pair.ConnectorID); err == nil {
					issue := workbuddyhost.StatusIssue(host)
					status.Connection.ControlReady = issue == "" && host.Ready && host.Cancellation == "session"
					if issue == "" && host.Presentation.Status == "capability_unavailable" {
						issue = "capability_unavailable"
					}
					if issue != "" {
						status.Issue = &agentintegration.Issue{Code: issue}
						status.Message = workBuddyIssueMessage(issue)
					}
					status.Connection.PresentationStatus = host.Presentation.Status
					status.Connection.PageStatus = host.Presentation.PageStatus
				} else {
					status.Issue = &agentintegration.Issue{Code: "host_unavailable"}
					status.Message = workBuddyIssueMessage("host_unavailable")
				}
				// Persisted trust and enable choices are independent of CDP.
				// Healthy control alone must never advertise a usable MCP connection.
				if !status.Connection.MCPTrusted && (status.Issue == nil || status.Issue.Code == "host_unavailable") {
					status.Issue = &agentintegration.Issue{Code: "mcp_trust_required"}
					status.Message = workBuddyIssueMessage("mcp_trust_required")
				} else if !status.Connection.MCPEnabled && (status.Issue == nil || status.Issue.Code == "host_unavailable") {
					code := "mcp_enable_required"
					status.Issue = &agentintegration.Issue{Code: code}
					status.Message = workBuddyIssueMessage(code)
				}
			}
		}
	}
	switch {
	case state.configured && !state.owned:
		status.State = agentintegration.Conflict
		status.Message = "An MCP server named `lazymind` already exists and is not managed by this LazyMind installation."
	case state.configured && state.current:
		status.State = agentintegration.Enabled
	case agentintegration.MissingRequirement(requirements):
		status.State = agentintegration.RequirementsMissing
	default:
		status.State = agentintegration.Ready
		if state.configured && status.Message == "" {
			status.Message = "The existing LazyMind MCP entry needs to be enabled again."
		}
	}
	return status
}

func displayName(kind Kind) (string, error) {
	switch kind {
	case Cursor:
		return "Cursor", nil
	case WorkBuddy:
		return "WorkBuddy", nil
	case Raccoon:
		return "Raccoon", nil
	case TRAEWork:
		return "TRAE Work", nil
	case DeepSeekHarness:
		return "DeepSeek Harness", nil
	default:
		return "", fmt.Errorf("unsupported MCP client %q", kind)
	}
}

func requirements(kind Kind) ([]agentintegration.Requirement, error) {
	switch kind {
	case Cursor:
		return desktopRequirements(agentexec.DesktopApplication{
			BindingTarget: agentexec.CursorDesktop, ExecutableNames: []string{"Cursor.exe"},
			Protocols: []string{"cursor"}, DisplayNames: []string{"Cursor"},
			StatePaths: []string{userPath(".cursor")},
		}, "cursor_desktop", "Cursor Desktop")
	case WorkBuddy:
		return desktopRequirements(agentexec.DesktopApplication{
			BindingTarget: agentexec.WorkBuddyDesktop, ExecutableNames: []string{"WorkBuddy.exe", "WorkBuddy CN.exe"},
			DisplayNames: []string{"WorkBuddy", "WorkBuddy CN"}, StatePaths: []string{userPath(".workbuddy")},
		}, "workbuddy_desktop", "WorkBuddy")
	case Raccoon:
		return desktopRequirements(agentexec.DesktopApplication{
			BindingTarget:   agentexec.RaccoonDesktop,
			ExecutableNames: []string{"Raccoon.exe", "OfficeRaccoon.exe", "办公小浣熊.exe", "商汤小浣熊.exe"},
			DisplayNames:    []string{"Raccoon", "办公小浣熊", "商汤小浣熊"},
			StatePaths:      []string{userPath(".box-agent", "config")},
		}, "raccoon_desktop", "Raccoon Desktop")
	case TRAEWork:
		return desktopRequirements(agentexec.DesktopApplication{
			BindingTarget:   agentexec.TRAEWorkDesktop,
			ExecutableNames: []string{"TRAE.exe", "TRAE SOLO CN.exe", "TraeWork.exe", "Trae CN.exe"},
			DisplayNames:    []string{"TRAE", "TRAE Work", "TRAE SOLO CN", "TRAE CN"},
			StatePaths:      []string{filepath.Dir(traeWorkConfigPath())},
		}, "trae_work_desktop", "TRAE Work")
	case DeepSeekHarness:
		if name := dshProfileName(); name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\") {
			return nil, errors.New("invalid DSH profile name")
		}
		installed, initialized := dshPresence()
		return []agentintegration.Requirement{
			{ID: "dsh_web", Description: "Install DeepSeek Harness Web.", Satisfied: installed},
			{ID: "dsh_web_initialized", Description: "Open DeepSeek Harness Web at least once.", Satisfied: initialized},
		}, nil
	default:
		return nil, nil
	}
}

func desktopRequirements(
	spec agentexec.DesktopApplication,
	id, displayName string,
) ([]agentintegration.Requirement, error) {
	state, err := agentexec.InspectDesktopApplication(spec)
	if err != nil {
		return nil, err
	}
	return []agentintegration.Requirement{
		{ID: id, Description: "Install " + displayName + ".", Satisfied: state.Installed},
		{ID: id + "_initialized", Description: "Open " + displayName + " at least once.", Satisfied: state.Initialized},
	}, nil
}

func configPath(kind Kind) string {
	switch kind {
	case Cursor:
		return userPath(".cursor", "mcp.json")
	case WorkBuddy:
		return userPath(".workbuddy", "mcp.json")
	case Raccoon:
		return userPath(".box-agent", "config", "mcp.json")
	case TRAEWork:
		return traeWorkConfigPath()
	case DeepSeekHarness:
		return filepath.Join(dshHome(), "profiles", dshProfileName(), "cordis.patch.yml")
	default:
		return ""
	}
}

func (a *Adapter) cursorInstallURL() (string, error) {
	if _, err := connectorconfig.Save(connectorconfig.Config{Home: a.home, Provider: string(Cursor), HostID: a.hostID}); err != nil {
		return "", err
	}
	body, err := json.Marshal(managedStdio(a.self, a.home, a.hostID, Cursor))
	if err != nil {
		return "", err
	}
	query := url.Values{
		"name":   []string{serverName},
		"config": []string{base64.StdEncoding.EncodeToString(body)},
	}
	return "cursor://anysphere.cursor-deeplink/mcp/install?" + query.Encode(), nil
}

func dshProfilePatch(self string, environment map[string]string) string {
	lines := []string{
		"- insert:",
		"    - id: mcp-lazymind",
		"      name: '@deepseek-ai/dsh-mcp-client'",
		"      config:",
		"        serverName: lazymind",
		"        transport: stdio",
		"        command: " + strconv.Quote(self),
		"        args: ['mcp', 'proxy']",
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		lines = append(lines, "        env:")
		for _, key := range keys {
			lines = append(lines, "          "+key+": "+strconv.Quote(environment[key]))
		}
	}
	// Keep the DSH harness up when LazyMind is offline. DSH's default is also
	// false: a failed first MCP connect logs, omits mcp__lazymind__* tools, and
	// reconnects later. true would abort the whole plugin tree at boot.
	return strings.Join(append(lines, "        failOnStartupError: false"), "\n") + "\n"
}

func dshHome() string {
	if home := strings.TrimSpace(os.Getenv("DSH_HOME")); home != "" {
		if absolute, err := filepath.Abs(home); err == nil {
			return filepath.Clean(absolute)
		}
	}
	home := strings.TrimSpace(os.Getenv("LAZYMIND_HOST_HOME"))
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".dsh")
}

func dshProfileDir() string {
	return filepath.Dir(configPath(DeepSeekHarness))
}

func dshPresence() (installed, initialized bool) {
	home := dshHome()
	profile := dshProfileDir()
	initialized = pathExists(profile)
	installed = initialized || pathExists(home)
	return installed, initialized
}

func userPath(parts ...string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(append([]string{home}, parts...)...)
}

func traeWorkConfigPath() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "TRAE SOLO CN", "User", "mcp.json")
	case "windows":
		if roaming := strings.TrimSpace(os.Getenv("APPDATA")); roaming != "" {
			return filepath.Join(roaming, "TRAE SOLO CN", "User", "mcp.json")
		}
		return filepath.Join(home, "AppData", "Roaming", "TRAE SOLO CN", "User", "mcp.json")
	default:
		return filepath.Join(home, ".config", "TRAE SOLO CN", "User", "mcp.json")
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
