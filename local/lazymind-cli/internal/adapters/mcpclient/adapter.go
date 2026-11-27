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
	"lazymind/agentconnector/internal/connectorlauncher"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/mcpbridge"
	"lazymind/agentconnector/internal/workbuddyhost"
	"lazymind/agentconnector/internal/workflowconnector"
	"lazymind/agentconnector/internal/workflowhost"
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
	actualSelf   string // Installation executable; self is the stable external binding.
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	launcher, err := connectorlauncher.Ensure(ctx, home, resolvedSelf)
	if err != nil {
		return nil, fmt.Errorf("prepare LazyMind connector launcher: %w", err)
	}
	return &Adapter{kind: kind, self: launcher, actualSelf: resolvedSelf, bridge: bridge, home: home, hostID: hostID}, nil
}

func (a *Adapter) diagnosticExecutable() string {
	if a.actualSelf != "" {
		return a.actualSelf
	}
	return a.self
}

var workBuddyStartupHealth = workbuddyhost.StartupHealth
var workBuddyAuthentication = workbuddyadapter.AuthenticationStatus

func (a *Adapter) Status(ctx context.Context) (status agentintegration.Status) {
	repairErr := a.Reconcile(ctx)
	status = a.status(ctx)
	details := a.inspectConfiguration(&status)
	if repairErr != nil && (status.Issue == nil || status.Issue.Code == workflowconnector.ConnectionPending) {
		status = agentintegration.FailError(status, repairErr)
		status.Issue = &agentintegration.Issue{Code: "configuration_failed"}
	}
	if a.kind == DeepSeekHarness {
		a.inspectDSHPendingDisconnect(ctx, &status)
		a.inspectDSHConnection(ctx, &status)
	}
	defer func() { agentintegration.RecordInspection(a.home, a.diagnosticExecutable(), details, &status) }()
	if a.kind == WorkBuddy {
		status.CanConnect = status.CanConnect && status.Enabled != nil && len(status.Requirements) > 0
		if status.Enabled != nil && *status.Enabled && status.State == agentintegration.Enabled {
			store, err := credentials.NewStore(a.home, "")
			if err == nil {
				account, err := store.AccountScope()
				if err == nil {
					config, _, err := workbuddyhost.Load(a.home, account)
					if err == nil {
						agentintegration.SetRuntimeIssue(&status, workBuddyStartupHealth(ctx, a.home, config.CDPPort))
					}
				}
			}
		}
	}
	return status
}

func (a *Adapter) Connect(ctx context.Context) (result agentintegration.Status) {
	operation := agentintegration.NewOperation(a.home, string(a.kind), "connect", a.diagnosticExecutable())
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
				result.CanDisconnect = state.owned
			}
		}
	}()
	// Bound the complete setup, including Core probe and runtime discovery.
	var workBuddyRegistered bool
	if a.kind == WorkBuddy {
		bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		ctx = bounded
		unlock, err := lockWorkBuddyOperation(ctx)
		if err != nil {
			return workBuddyFailure(agentintegration.Status{Agent: "workbuddy", DisplayName: "WorkBuddy"}, workBuddyLockError(err))
		}
		defer unlock()
		workBuddyRegistered, err = a.verifyWorkBuddyRegistration()
		if err != nil {
			return workBuddyRegistrationFailure(err)
		}
	}
	if a.actualSelf != "" && !workBuddyRegistered {
		if _, err := connectorlauncher.Publish(ctx, a.home, a.actualSelf); err != nil {
			return agentintegration.FailError(agentintegration.Status{Agent: string(a.kind)}, err)
		}
	}
	if a.desktopDSHMode() {
		snapshot := agentintegration.Status{Agent: string(a.kind)}
		operation.Before = a.inspectConfiguration(&snapshot)
		return a.connectDSHDesktop(ctx)
	}
	var status agentintegration.Status
	if workBuddyRegistered {
		// Status can relocate an existing launcher. Reconnect keeps it intact
		// until the new WorkBuddy installation has succeeded.
		status = a.status(ctx)
	} else {
		status = a.Status(ctx)
	}
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
		if !workBuddyRegistered {
			// Reserve a free registration before setup touches shared WorkBuddy
			// state so this deployment can clean up a partial installation.
			operation.Enter("configuration")
			if err := writeManagedConfig(a.kind, configPath(a.kind), a.self, a.home, a.hostID, false); err != nil {
				return workBuddyRegistrationFailure(err)
			}
		}
		operation.Enter("pairing")
		if err := workflowconnector.Ensure(ctx, a.home, a.diagnosticExecutable(), "workbuddy"); err != nil {
			return workBuddyFailure(status, &workbuddyhost.SetupError{Code: "runtime_unavailable", Err: err})
		}
		if err := workbuddyhost.Install(ctx, a.home, userPath(".workbuddy"), account); err != nil {
			return workBuddyFailure(status, err)
		}
		if workBuddyRegistered {
			operation.Enter("configuration")
			if a.actualSelf != "" {
				if _, err := connectorlauncher.Publish(ctx, a.home, a.actualSelf); err != nil {
					return workBuddyFailure(status, err)
				}
			}
			if err := a.Reconcile(ctx); err != nil {
				return workBuddyFailure(status, err)
			}
		}
	}
	if a.kind == DeepSeekHarness {
		source, err := workflowconnector.DSHRelease(ctx, a.home, a.diagnosticExecutable())
		if err != nil {
			return dshFailure(status, err)
		}
		if err := a.changeDSHProfile(ctx, &source); err != nil {
			return dshFailure(status, err)
		}
		return a.status(ctx)
	}
	if a.kind != WorkBuddy || workBuddyRegistered {
		operation.Enter("configuration")
		if err := writeManagedConfig(a.kind, configPath(a.kind), a.self, a.home, a.hostID, false); err != nil {
			if a.kind == WorkBuddy {
				return workBuddyRegistrationFailure(err)
			}
			return agentintegration.FailError(status, err)
		}
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
	operation := agentintegration.NewOperation(a.home, string(a.kind), "disconnect", a.diagnosticExecutable())
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
		unlock, err := lockWorkBuddyOperation(ctx)
		if err != nil {
			return workBuddyFailure(agentintegration.Status{Agent: "workbuddy", DisplayName: "WorkBuddy"}, workBuddyLockError(err))
		}
		defer unlock()
		if _, err := a.verifyWorkBuddyRegistration(); err != nil {
			return workBuddyRegistrationFailure(err)
		}
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
		if err := a.changeDSHProfile(ctx, nil); err != nil {
			return agentintegration.FailError(status, err)
		}
		return a.status(ctx)
	}
	if a.kind == WorkBuddy {
		// Keep ownership until the local pairing and startup state are cleaned up.
		if err := workbuddyhost.DisableLocal(a.home); err != nil {
			return agentintegration.FailError(status, err)
		}
	}
	if err := removeManagedConfig(a.kind, configPath(a.kind), a.home, a.hostID); err != nil {
		if a.kind == WorkBuddy {
			return workBuddyRegistrationFailure(err)
		}
		return agentintegration.FailError(status, err)
	}
	return a.status(ctx)
}

func (a *Adapter) workBuddyConnectionStatus(status *agentintegration.Status) bool {
	host, statusErr := workbuddyhost.ReadLocalStatus(a.home)
	controlIssue := "host_unavailable"
	if statusErr == nil {
		controlIssue = workbuddyhost.ControlIssue(host)
		status.ConnectorRuntime = host.Runtime
		if workbuddyhost.VersionIssue(host.Version) != "version_unreadable" {
			status.Version = host.Version
		}
		status.Connection.PresentationStatus = host.Presentation.Status
		status.Connection.PageStatus = host.Presentation.PageStatus
	}
	status.Connection.ControlReady = controlIssue == ""
	agentintegration.SetRuntimeIssue(status, controlIssue)
	if status.Version == "" {
		status.Version = workbuddyhost.CurrentVersion()
	}

	connectionIssue := "pairing_required"
	store, err := credentials.NewStore(a.home, "")
	if err == nil {
		account, err := store.AccountScope()
		if err != nil {
			connectionIssue = "lazymind_auth_required"
		} else if _, pair, err := workbuddyhost.Load(a.home, account); err == nil {
			if controlIssue == "" || controlIssue == "host_unavailable" {
				switch {
				case !status.Connection.MCPTrusted:
					status.Issue = &agentintegration.Issue{Code: "mcp_trust_required"}
				case !status.Connection.MCPEnabled:
					status.Issue = &agentintegration.Issue{Code: "mcp_enable_required"}
				}
			}
			connectionIssue = workflowconnector.ConnectionPending
			if host.ConnectorID == pair.ConnectorID {
				connectionIssue = host.ConnectionEvidence.Issue(pair.RuntimeRevision, host.CheckedAt, time.Time{})
			}
			agentintegration.SetLazyMindIssue(status, connectionIssue)
			if status.Issue != nil {
				status.Message = workBuddyIssueMessage(status.Issue.Code)
			}
			return true
		} else if code := workflowhost.PairingIssue(err); code != "" {
			connectionIssue = code
		}
	}
	status.Issue = &agentintegration.Issue{Code: connectionIssue}
	status.Message = workBuddyIssueMessage(connectionIssue)
	agentintegration.SetLazyMindIssue(status, connectionIssue)
	return false
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
			status.Message = "Reconnect to finish installing the LazyMind Workflow connector."
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
		state.current = a.workBuddyConnectionStatus(&status)
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
		application := agentexec.WorkBuddyDesktopApplication()
		application.StatePaths = []string{userPath(".workbuddy")}
		return desktopRequirements(application, "workbuddy_desktop", "WorkBuddy")
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
