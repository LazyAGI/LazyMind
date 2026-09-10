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

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/mcpbridge"
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
	dshRunner func(context.Context, string, ...string) error
	kind      Kind
	self      string
	bridge    *mcpbridge.Bridge
	home      string
	hostID    string
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

func (a *Adapter) Status(context.Context) agentintegration.Status {
	return a.status()
}

func (a *Adapter) Connect(ctx context.Context) agentintegration.Status {
	status := a.status()
	if status.State != agentintegration.Ready {
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
	return a.status()
}

func (a *Adapter) Disconnect(context.Context) agentintegration.Status {
	status := a.status()
	if status.State == agentintegration.Conflict || status.State == agentintegration.Failed {
		return status
	}
	if a.kind == DeepSeekHarness {
		if err := a.disconnectDSHWorkflow(); err != nil {
			return agentintegration.Fail(status, err.Error())
		}
	}
	if err := removeManagedConfig(a.kind, configPath(a.kind)); err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	return a.status()
}

func (a *Adapter) status() agentintegration.Status {
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
	if a.kind == DeepSeekHarness && state.configured && state.owned && state.current {
		if !a.dshWorkflowConfigured() {
			state.current = false
			status.Message = "MCP is configured; reconnect to install or update the Workflow panel and host adapter."
		} else {
			status.Message = "MCP and the Workflow bundle are configured. Restart DSH to activate updated plugins."
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
	// Surface proxy auth/start failures in the DSH plugin panel instead of
	// silently omitting mcp__lazymind__* tools.
	return strings.Join(append(lines, "        failOnStartupError: true"), "\n") + "\n"
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
