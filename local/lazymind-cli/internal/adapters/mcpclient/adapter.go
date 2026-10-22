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

func (a *Adapter) Status(ctx context.Context) agentintegration.Status {
	return a.status(ctx)
}

func (a *Adapter) Connect(ctx context.Context) (result agentintegration.Status) {
	defer func() {
		if a.kind == WorkBuddy && result.State == agentintegration.Failed {
			sanitizeWorkBuddyFailure(&result)
			// Installation may have written the owned entry before startup failed.
			if state, err := readManagedConfig(a.kind, configPath(a.kind), a.self, a.home, a.hostID); err == nil {
				result.CanDisconnect = state.configured && state.owned
			}
		}
	}()
	if a.desktopDSHMode() {
		return a.connectDSHDesktop(ctx)
	}
	// Bound the complete setup, including Core probe and runtime discovery.
	if a.kind == WorkBuddy {
		bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		ctx = bounded
	}
	status := a.status(ctx)
	if a.kind == WorkBuddy {
		status.Issue = nil
	}
	if status.State != agentintegration.Ready && !(a.kind == WorkBuddy && status.State == agentintegration.Enabled) {
		return status
	}
	if _, err := a.bridge.Probe(ctx); err != nil {
		return agentintegration.Fail(status, "LazyMind MCP is unavailable: "+err.Error())
	}
	if a.kind == Cursor {
		installURL, err := a.cursorInstallURL()
		if err != nil {
			return agentintegration.Fail(status, err.Error())
		}
		status.State = agentintegration.ActionRequired
		status.Action = &agentintegration.Action{
			Kind: "open_url", URL: installURL,
		}
		status.Message = "Approve the LazyMind MCP installation in Cursor, then check again."
		return status
	}
	if a.kind == WorkBuddy {
		if err := workbuddyhost.Preflight(); err != nil {
			return workBuddyFailure(status, err)
		}
		store, err := credentials.NewStore(a.home, "")
		if err != nil {
			return agentintegration.Fail(status, err.Error())
		}
		account, err := store.AccountScope()
		if err != nil {
			return agentintegration.Fail(status, err.Error())
		}
		if err := workbuddyhost.Install(ctx, a.home, userPath(".workbuddy"), account); err != nil {
			return agentintegration.Fail(status, err.Error())
		}
	}
	controlled := false
	if a.kind == DeepSeekHarness {
		var err error
		controlled, err = a.installDSHWorkflow(ctx)
		if err != nil {
			return agentintegration.Fail(status, err.Error())
		}
	}
	if err := writeManagedConfig(a.kind, configPath(a.kind), a.self, a.home, a.hostID, controlled); err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	if a.kind == WorkBuddy {
		setup, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		store, err := credentials.NewStore(a.home, "")
		if err != nil {
			return agentintegration.Fail(status, err.Error())
		}
		account, err := store.AccountScope()
		if err != nil {
			return agentintegration.Fail(status, err.Error())
		}
		config, _, err := workbuddyhost.Load(a.home, account)
		if err != nil {
			return agentintegration.Fail(status, err.Error())
		}
		// Reconnect also reloads the newly written MCP configuration.
		if err := workbuddyhost.PrepareDesktop(setup, config.CDPPort, true); err != nil {
			return agentintegration.Fail(status, err.Error())
		}
		if err := workbuddyhost.WaitReady(setup, a.home, account, time.Now()); err != nil {
			return workBuddyFailure(status, err)
		}
	}
	return a.status(ctx)
}

func (a *Adapter) Disconnect(ctx context.Context) agentintegration.Status {
	if a.desktopDSHMode() {
		return a.disconnectDSHDesktop(ctx)
	}
	status := a.status(ctx)
	if status.State == agentintegration.Conflict || status.State == agentintegration.Failed && !(a.kind == WorkBuddy && status.CanDisconnect) {
		return status
	}
	if a.kind == DeepSeekHarness {
		if err := a.disconnectDSHWorkflow(); err != nil {
			return agentintegration.Fail(status, err.Error())
		}
	}
	if a.kind == WorkBuddy {
		store, err := credentials.NewStore(a.home, "")
		if err != nil {
			return agentintegration.Fail(status, err.Error())
		}
		account, err := store.AccountScope()
		if err != nil {
			return agentintegration.Fail(status, err.Error())
		}
		if err := workbuddyhost.Disable(a.home, account); err != nil {
			return agentintegration.Fail(status, err.Error())
		}
	}
	if err := removeManagedConfig(a.kind, configPath(a.kind)); err != nil {
		return agentintegration.Fail(status, err.Error())
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
		return agentintegration.Fail(status, err.Error())
	}
	status.Requirements = requirements
	state, err := readManagedConfig(a.kind, configPath(a.kind), a.self, a.home, a.hostID)
	if err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	if a.kind == WorkBuddy {
		status.CanDisconnect = state.configured && state.owned
	}
	if a.kind == DeepSeekHarness && state.configured && state.owned && state.current {
		if !a.dshWorkflowConfigured() {
			state.current = false
			status.Message = "MCP is configured; reconnect to install or update the Workflow panel and host adapter."
		} else {
			status.Message = "MCP and the Workflow bundle are configured. Restart DSH to activate updated plugins."
		}
	}
	if a.kind == WorkBuddy && state.configured && state.owned && state.current {
		state.current = false
		status.Connection = &agentintegration.ConnectionStatus{MCPConfigured: true, PresentationStatus: "host-offline"}
		status.Message = "Enable the connection to start WorkBuddy and connect Workflow session control."
		store, err := credentials.NewStore(a.home, "")
		if err == nil {
			account, scopeErr := store.AccountScope()
			config, pair, configErr := workbuddyhost.Load(a.home, account)
			if scopeErr != nil || configErr != nil {
				state.current = false
				status.Message = "Reconnect WorkBuddy to configure the independent Workflow connector."
			} else {
				status.Connection.MCPTrusted, status.Connection.MCPEnabled = workbuddySetup(configPath(a.kind))
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
