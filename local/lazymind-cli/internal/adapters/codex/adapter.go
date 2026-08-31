package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/mcpbridge"
)

const (
	serverName         = "lazymind"
	commandTimeout     = 5 * time.Second
	interactiveTimeout = 2 * time.Minute
)

type Adapter struct {
	binary         string
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
		binary: resolvedBinary, discoveryError: discoveryError,
		self: resolvedSelf, bridge: bridge, home: home, hostID: hostID,
	}, nil
}

func (a *Adapter) Status(context.Context) agentintegration.Status {
	requirements, err := desktopRequirements()
	status := agentintegration.Status{
		Agent: "codex", DisplayName: "Codex", Requirements: requirements,
	}
	if err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	config, exists, err := a.getConfig()
	if err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	switch {
	case exists && !a.isOwned(config):
		status.State = agentintegration.Conflict
		status.Message = "Codex already has an MCP server named `lazymind` that is not managed by this LazyMind installation."
	case exists && config.Enabled && a.hasCurrentEnvironment(config):
		status.State = agentintegration.Enabled
	case agentintegration.MissingRequirement(requirements):
		status.State = agentintegration.RequirementsMissing
	default:
		status.State = agentintegration.Ready
		if exists {
			status.Message = "The existing LazyMind MCP entry needs to be enabled again."
		}
	}
	return status
}

func (a *Adapter) Connect(ctx context.Context) agentintegration.Status {
	if err := CleanupLegacyControl(); err != nil {
		return agentintegration.Fail(a.Status(ctx), "clean up legacy Codex control configuration: "+err.Error())
	}
	status := a.Status(ctx)
	if status.State != agentintegration.Ready {
		return status
	}
	if a.discoveryError != nil {
		return agentintegration.Fail(status, "Codex Desktop runtime is unavailable: "+a.discoveryError.Error())
	}
	if _, err := a.bridge.Probe(ctx); err != nil {
		return agentintegration.Fail(status, "LazyMind MCP is unavailable: "+err.Error())
	}
	if _, exists, err := a.getConfig(); err != nil {
		return agentintegration.Fail(status, err.Error())
	} else if exists {
		if _, err := a.run(ctx, "mcp", "remove", serverName); err != nil {
			return agentintegration.Fail(status, "remove stale Codex MCP configuration: "+err.Error())
		}
	}
	if err := a.addConfig(ctx); err != nil {
		return agentintegration.Fail(status, "configure Codex MCP: "+err.Error())
	}
	return a.Status(ctx)
}

func (a *Adapter) Disconnect(ctx context.Context) agentintegration.Status {
	if err := CleanupLegacyControl(); err != nil {
		return agentintegration.Fail(a.Status(ctx), "clean up legacy Codex control configuration: "+err.Error())
	}
	status := a.Status(ctx)
	if status.State == agentintegration.Conflict || status.State == agentintegration.Failed || a.discoveryError != nil {
		return status
	}
	if _, exists, err := a.getConfig(); err != nil {
		return agentintegration.Fail(status, err.Error())
	} else if exists {
		if _, err := a.run(ctx, "mcp", "remove", serverName); err != nil {
			return agentintegration.Fail(status, "remove Codex MCP configuration: "+err.Error())
		}
	}
	return a.Status(ctx)
}

func (a *Adapter) Login(ctx context.Context) agentintegration.Status {
	status := a.Status(ctx)
	if a.discoveryError != nil {
		return status
	}
	loginCtx, cancel := context.WithTimeout(ctx, interactiveTimeout)
	defer cancel()
	if _, err := agentexec.Run(loginCtx, a.binary, "login"); err != nil {
		return agentintegration.Fail(status, "Codex login failed: "+err.Error())
	}
	return a.Status(ctx)
}

func desktopRequirements() ([]agentintegration.Requirement, error) {
	state, err := agentexec.InspectDesktopApplication(agentexec.DesktopApplication{
		BindingTarget:   agentexec.CodexDesktop,
		ExecutableNames: []string{"ChatGPT.exe", "Codex.exe"},
		DisplayNames:    []string{"ChatGPT", "Codex"},
	})
	if err != nil {
		return nil, err
	}
	return []agentintegration.Requirement{{
		ID: "codex_desktop", Description: "Install the Codex desktop app.", Satisfied: state.Installed,
	}}, nil
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

func (a *Adapter) addConfig(ctx context.Context) error {
	environment := currentEnvironment(a.home, a.hostID)
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	arguments := []string{"mcp", "add", serverName}
	for _, key := range keys {
		arguments = append(arguments, "--env", key+"="+environment[key])
	}
	arguments = append(arguments, "--", a.self, "mcp", "proxy")
	_, err := a.run(ctx, arguments...)
	return err
}

func (a *Adapter) run(ctx context.Context, arguments ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return agentexec.Run(runCtx, a.binary, arguments...)
}

func (a *Adapter) isOwned(config mcpConfig) bool {
	return agentexec.SameExecutable(config.Transport.Command, a.self) &&
		len(config.Transport.Args) == 2 &&
		config.Transport.Args[0] == "mcp" && config.Transport.Args[1] == "proxy"
}

func (a *Adapter) hasCurrentEnvironment(config mcpConfig) bool {
	configuredHome := filepath.Clean(strings.TrimSpace(config.Transport.Env["LAZYMIND_HOME"]))
	if configuredHome == "." {
		configuredHome = ""
	}
	return configuredHome == a.home && config.Transport.Env["LAZYMIND_AGENT_PROVIDER"] == "codex" &&
		config.Transport.Env["LAZYMIND_AGENT_HOST_ID"] == a.hostID
}

func currentEnvironment(home, hostID string) map[string]string {
	environment := map[string]string{
		"LAZYMIND_AGENT_PROVIDER": "codex", "LAZYMIND_AGENT_HOST_ID": hostID,
	}
	if home != "" {
		environment["LAZYMIND_HOME"] = home
	}
	return environment
}

func findBinary(configured string) (string, error) {
	name := "codex"
	home, _ := os.UserHomeDir()
	candidates := codexCandidates(home, name)
	resolved, err := agentexec.FindBoundExecutable(
		configured, "LAZYMIND_CODEX_BIN", agentexec.CodexCLI, []string{name}, candidates,
	)
	if err != nil {
		return "", errors.New("Codex CLI is not installed")
	}
	return resolved, nil
}

func codexCandidates(home, name string) []string {
	candidates := []string{}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".codex", "packages", "standalone", "current", "bin", name))
		releases, _ := filepath.Glob(filepath.Join(home, ".codex", "packages", "standalone", "releases", "*", "bin", name))
		sort.Sort(sort.Reverse(sort.StringSlice(releases)))
		candidates = append(candidates, releases...)
	}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			"/opt/homebrew/bin/codex", "/usr/local/bin/codex",
			"/Applications/ChatGPT.app/Contents/Resources/codex",
			"/Applications/Codex.app/Contents/Resources/codex",
		)
	case "windows":
		if root := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); root != "" {
			candidates = append(candidates,
				filepath.Join(root, "Programs", "ChatGPT", "resources", name),
				filepath.Join(root, "Programs", "Codex", "resources", name),
			)
		}
	}
	return candidates
}

func codexHome() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func FindBinary(configured string) (string, error) { return findBinary(configured) }
