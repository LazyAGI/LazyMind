package mcpclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	kind   Kind
	self   string
	bridge *mcpbridge.Bridge
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
	if err := writeManagedConfig(a.kind, configPath(a.kind), a.self, a.home, a.hostID); err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	return a.status()
}

func (a *Adapter) Disconnect(context.Context) agentintegration.Status {
	status := a.status()
	if status.State == agentintegration.Conflict || status.State == agentintegration.Failed {
		return status
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
		if state.configured {
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
			BindingTarget: agentexec.WorkBuddyDesktop, ExecutableNames: []string{"WorkBuddy.exe"},
			DisplayNames: []string{"WorkBuddy"}, StatePaths: []string{userPath(".workbuddy")},
		}, "workbuddy_desktop", "WorkBuddy")
	case Raccoon:
		return desktopRequirements(agentexec.DesktopApplication{
			BindingTarget:   agentexec.RaccoonDesktop,
			ExecutableNames: []string{"Raccoon.exe", "OfficeRaccoon.exe", "办公小浣熊.exe"},
			DisplayNames:    []string{"Raccoon", "办公小浣熊"},
			StatePaths:      []string{userPath(".box-agent", "config")},
		}, "raccoon_desktop", "Raccoon Desktop")
	case TRAEWork:
		return desktopRequirements(agentexec.DesktopApplication{
			BindingTarget:   agentexec.TRAEWorkDesktop,
			ExecutableNames: []string{"TRAE.exe", "TRAE SOLO CN.exe", "TraeWork.exe"},
			DisplayNames:    []string{"TRAE", "TRAE Work", "TRAE SOLO CN"},
			StatePaths:      []string{filepath.Dir(traeWorkConfigPath())},
		}, "trae_work_desktop", "TRAE Work")
	case DeepSeekHarness:
		profile := filepath.Join(dshHome(), "profiles", "web", "package.json")
		client := filepath.Join(dshHome(), "profiles", "node_modules", "@deepseek-ai", "dsh-mcp-client", "package.json")
		return []agentintegration.Requirement{
			{ID: "dsh_web_profile", Description: "Initialize the DeepSeek Harness web profile.", Satisfied: pathExists(profile)},
			{ID: "dsh_mcp_client", Description: "Install @deepseek-ai/dsh-mcp-client in the web profile.", Satisfied: pathExists(client)},
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
		return filepath.Join(dshHome(), "profiles", "web", "cordis.patch.yml")
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
	return strings.Join(append(lines, "        failOnStartupError: true"), "\n") + "\n"
}

func dshHome() string {
	if home := strings.TrimSpace(os.Getenv("DSH_HOME")); home != "" {
		if absolute, err := filepath.Abs(home); err == nil {
			return filepath.Clean(absolute)
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dsh")
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
