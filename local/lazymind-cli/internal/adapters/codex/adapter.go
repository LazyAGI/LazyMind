package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/codexcontrol"
	"lazymind/agentconnector/internal/mcpbridge"
)

const serverName = "lazymind"

type Adapter struct {
	binary         string
	discoveryError error
	self           string
	bridge         *mcpbridge.Bridge
	home           string
	hostID         string
	controlAddress string
	control        *codexcontrol.Controller
}

type Status struct {
	Agent             string   `json:"agent"`
	Installed         bool     `json:"installed"`
	Version           string   `json:"version,omitempty"`
	Configured        bool     `json:"configured"`
	Owned             bool     `json:"owned"`
	ServiceReady      bool     `json:"service_ready"`
	Ready             bool     `json:"ready"`
	Command           string   `json:"command,omitempty"`
	Arguments         []string `json:"arguments,omitempty"`
	Endpoint          string   `json:"endpoint,omitempty"`
	Tools             []string `json:"tools,omitempty"`
	ReadinessError    string   `json:"readiness_error,omitempty"`
	ControlConfigured bool     `json:"control_configured"`
	ControlConnected  bool     `json:"control_connected"`
	RestartRequired   bool     `json:"restart_required"`
}

type mcpConfig struct {
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Transport transport `json:"transport"`
}

type transport struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

func New(binary, self string, bridge *mcpbridge.Bridge) (*Adapter, error) {
	return NewWithControl(binary, self, bridge, codexcontrol.DefaultAddress, nil)
}

func NewWithControl(
	binary, self string,
	bridge *mcpbridge.Bridge,
	controlAddress string,
	control *codexcontrol.Controller,
) (*Adapter, error) {
	if bridge == nil {
		return nil, errors.New("MCP bridge is required")
	}
	resolvedBinary, discoveryError := findBinary(binary)
	var err error
	if strings.TrimSpace(self) == "" {
		self, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve LazyMind executable: %w", err)
		}
	}
	resolvedSelf, err := agentexec.ResolveExecutable(self)
	if err != nil {
		return nil, fmt.Errorf("resolve LazyMind executable: %w", err)
	}
	connectorHome, err := agentexec.LazyMindHome()
	if err != nil {
		return nil, err
	}
	hostID, err := agentexec.PersistentHostID()
	if err != nil {
		return nil, fmt.Errorf("resolve LazyMind Agent Host identity: %w", err)
	}
	return &Adapter{
		binary: resolvedBinary, discoveryError: discoveryError,
		self: resolvedSelf, bridge: bridge, home: connectorHome,
		hostID: hostID, controlAddress: strings.TrimSpace(controlAddress), control: control,
	}, nil
}

func findBinary(configured string) (string, error) {
	name := "codex"
	if runtime.GOOS == "windows" {
		name = "codex.exe"
	}
	home, _ := os.UserHomeDir()
	var candidates []string
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".codex", "packages", "standalone", "current", "bin", name),
		)
		releases, _ := filepath.Glob(filepath.Join(home, ".codex", "packages", "standalone", "releases", "*", "bin", name))
		sort.Sort(sort.Reverse(sort.StringSlice(releases)))
		candidates = append(candidates, releases...)
		candidates = append(candidates, filepath.Join(home, ".local", "bin", name))
	}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			"/opt/homebrew/bin/codex",
			"/usr/local/bin/codex",
			"/Applications/Codex.app/Contents/Resources/codex",
			"/Applications/ChatGPT.app/Contents/Resources/codex",
		)
		if home != "" {
			candidates = append(candidates, filepath.Join(home, "Applications", "ChatGPT.app", "Contents", "Resources", "codex"))
		}
	case "windows":
		if root := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); root != "" {
			candidates = append(candidates,
				filepath.Join(root, "Programs", "Codex", "resources", name),
				filepath.Join(root, "Programs", "ChatGPT", "resources", name),
			)
			matches, _ := filepath.Glob(filepath.Join(root, "Programs", "ChatGPT", "app-*", "resources", name))
			candidates = append(candidates, matches...)
		}
		if root := strings.TrimSpace(os.Getenv("ProgramFiles")); root != "" {
			candidates = append(candidates, filepath.Join(root, "ChatGPT", "resources", name))
		}
	}
	resolved, err := findBinaryFromSources(configured, []string{name}, candidates)
	if err != nil {
		if strings.TrimSpace(configured) != "" || strings.TrimSpace(os.Getenv("LAZYMIND_CODEX_BIN")) != "" {
			return "", fmt.Errorf("resolve configured Codex CLI: %w", err)
		}
		return "", errors.New("Codex CLI is not installed; install Codex CLI before connecting LazyMind")
	}
	return resolved, nil
}

func findBinaryFromSources(configured string, names, candidates []string) (string, error) {
	if strings.TrimSpace(configured) == "" {
		configured = strings.TrimSpace(os.Getenv("LAZYMIND_CODEX_BIN"))
	}
	if configured != "" {
		return agentexec.Find(configured, "", nil, nil)
	}
	completeDistributions := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if codexDistributionHasHost(candidate) {
			completeDistributions = append(completeDistributions, candidate)
		}
	}
	if resolved, err := agentexec.Find("", "", nil, completeDistributions); err == nil {
		return resolved, nil
	}
	return agentexec.Find("", "", names, candidates)
}

func codexDistributionHasHost(binary string) bool {
	host := "codex-code-mode-host"
	if runtime.GOOS == "windows" {
		host += ".exe"
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(binary), host))
	return err == nil && !info.IsDir()
}

// FindBinary is shared by the MCP installation adapter and the hosted Chat
// adapter so Codex discovery has one authoritative implementation.
func FindBinary(configured string) (string, error) { return findBinary(configured) }

func (a *Adapter) Connect(ctx context.Context) (Status, error) {
	if a.discoveryError != nil {
		return Status{}, a.discoveryError
	}
	config, exists, err := a.getConfig(ctx)
	if err != nil {
		return Status{}, err
	}
	if exists && !a.isOwned(config) {
		return Status{}, errors.New("Codex already has an MCP server named `lazymind` that is not managed by this LazyMind installation; remove or rename that entry in Codex before connecting")
	}
	probe, err := a.bridge.Probe(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("LazyMind MCP preflight failed: %w", err)
	}
	if exists && config.Enabled && a.hasCurrentEnvironment(config) {
		if _, err := codexcontrol.Configure(a.controlAddress); err != nil {
			return Status{}, fmt.Errorf("configure Codex native control: %w", err)
		}
		return a.populateStatus(ctx, Status{
			Agent: "codex", Installed: true, ServiceReady: true,
			Endpoint: probe.Endpoint, Tools: probe.Tools,
		})
	}

	if exists {
		if _, err := agentexec.Run(ctx, a.binary, "mcp", "remove", serverName); err != nil {
			return Status{}, fmt.Errorf("remove stale Codex MCP configuration: %w", err)
		}
	}
	if err := a.addConfig(ctx, a.self, []string{"mcp", "proxy"}, currentEnvironment(a.home, a.hostID)); err != nil {
		if exists {
			_ = a.addConfig(context.Background(), config.Transport.Command, config.Transport.Args, config.Transport.Env)
		}
		return Status{}, fmt.Errorf("add Codex MCP configuration: %w", err)
	}
	if _, err := codexcontrol.Configure(a.controlAddress); err != nil {
		_, _ = agentexec.Run(context.Background(), a.binary, "mcp", "remove", serverName)
		if exists {
			_ = a.addConfig(context.Background(), config.Transport.Command, config.Transport.Args, config.Transport.Env)
		}
		return Status{}, fmt.Errorf("configure Codex native control: %w", err)
	}
	configured, configuredExists, verifyErr := a.getConfig(ctx)
	if verifyErr != nil || !configuredExists || !configured.Enabled || !a.isOwned(configured) || !a.hasCurrentEnvironment(configured) {
		_, _ = agentexec.Run(context.Background(), a.binary, "mcp", "remove", serverName)
		if exists {
			_ = a.addConfig(context.Background(), config.Transport.Command, config.Transport.Args, config.Transport.Env)
		}
		if verifyErr != nil {
			return Status{}, fmt.Errorf("verify Codex MCP configuration: %w", verifyErr)
		}
		return Status{}, errors.New("Codex did not persist the expected LazyMind MCP configuration")
	}
	return a.populateStatus(ctx, Status{
		Agent: "codex", Installed: true, ServiceReady: true,
		Endpoint: probe.Endpoint, Tools: probe.Tools,
	})
}

func (a *Adapter) Status(ctx context.Context) (Status, error) {
	probe, probeErr := a.bridge.Probe(ctx)
	return a.StatusWithProbe(ctx, probe, probeErr)
}

func (a *Adapter) StatusWithProbe(ctx context.Context, probe mcpbridge.ProbeResult, probeErr error) (Status, error) {
	if a.discoveryError != nil {
		return Status{
			Agent: "codex", Installed: false, Ready: false,
			ReadinessError: a.discoveryError.Error(),
		}, nil
	}
	status := Status{Agent: "codex", Installed: true, ServiceReady: probeErr == nil}
	if probeErr == nil {
		status.Endpoint = probe.Endpoint
		status.Tools = probe.Tools
	} else {
		status.ReadinessError = probeErr.Error()
	}
	return a.populateStatus(ctx, status)
}

func (a *Adapter) Disconnect(ctx context.Context) (Status, error) {
	if a.discoveryError != nil {
		return a.Status(ctx)
	}
	config, exists, err := a.getConfig(ctx)
	if err != nil {
		return Status{}, err
	}
	if exists && !a.isOwned(config) {
		return Status{}, errors.New("the Codex MCP server named `lazymind` is not managed by this LazyMind installation and will not be removed")
	}
	if exists {
		if _, err := agentexec.Run(ctx, a.binary, "mcp", "remove", serverName); err != nil {
			return Status{}, fmt.Errorf("remove Codex MCP configuration: %w", err)
		}
	}
	if err := codexcontrol.RemoveConfig(a.controlAddress); err != nil {
		return Status{}, fmt.Errorf("remove Codex native control enrollment: %w", err)
	}
	return a.Status(ctx)
}

func (a *Adapter) populateStatus(ctx context.Context, status Status) (Status, error) {
	controlConfig, controlErr := codexcontrol.InspectConfig(a.controlAddress)
	if controlErr == nil {
		status.ControlConfigured = controlConfig.Configured
	}
	if a.control != nil {
		controlStatus := a.control.Status()
		status.ControlConnected = controlStatus.Ready
	}
	config, exists, err := a.getConfig(ctx)
	if err != nil {
		return Status{}, err
	}
	status.Configured = exists
	if !exists {
		status.Ready = false
		return status, nil
	}
	status.Owned = a.isOwned(config)
	status.Command = config.Transport.Command
	status.Arguments = append([]string(nil), config.Transport.Args...)
	switch {
	case !status.Owned:
		status.Ready = false
		status.ReadinessError = "Codex already has an MCP server named `lazymind` that is not managed by this LazyMind installation"
	case !config.Enabled:
		status.Ready = false
		status.ReadinessError = "Codex MCP configuration is disabled; reconnect from LazyMind Desktop settings"
	case !a.hasCurrentEnvironment(config):
		status.Ready = false
		status.ReadinessError = "Codex MCP configuration uses a different LazyMind credential directory; reconnect from LazyMind Desktop settings"
	case controlErr != nil:
		status.Ready = false
		status.ReadinessError = "Codex native control configuration is unavailable: " + controlErr.Error()
	case !status.ControlConfigured:
		status.Ready = false
		status.ReadinessError = "Codex native control is not configured; reconnect from LazyMind settings"
	case !status.ControlConnected:
		status.Ready = false
		status.RestartRequired = true
		status.ReadinessError = "Restart Codex once so it connects to LazyMind native control"
	default:
		status.Ready = status.ServiceReady
	}
	return status, nil
}

func (a *Adapter) getConfig(ctx context.Context) (mcpConfig, bool, error) {
	output, err := agentexec.Run(ctx, a.binary, "mcp", "list", "--json")
	if err != nil {
		return mcpConfig{}, false, fmt.Errorf("list Codex MCP configuration: %w", err)
	}
	var configs []mcpConfig
	if err := json.Unmarshal([]byte(output), &configs); err != nil {
		return mcpConfig{}, false, fmt.Errorf("decode Codex MCP configuration: %w", err)
	}
	for _, config := range configs {
		if config.Name == serverName {
			return config, true, nil
		}
	}
	return mcpConfig{}, false, nil
}

func (a *Adapter) addConfig(ctx context.Context, command string, args []string, environment map[string]string) error {
	arguments := []string{"mcp", "add", serverName}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		arguments = append(arguments, "--env", key+"="+environment[key])
	}
	arguments = append(arguments, "--", command)
	arguments = append(arguments, args...)
	_, err := agentexec.Run(ctx, a.binary, arguments...)
	return err
}

func (a *Adapter) isOwned(config mcpConfig) bool {
	return config.Transport.Type == "stdio" &&
		agentexec.SameExecutable(config.Transport.Command, a.self) &&
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
	if home == "" {
		return environment
	}
	environment["LAZYMIND_HOME"] = home
	return environment
}
