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
	"strconv"
	"strings"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/mcpbridge"
)

type Kind string

const (
	Cursor               Kind = "cursor"
	WorkBuddy            Kind = "workbuddy"
	TRAEWork             Kind = "traework"
	DeepSeekHarness      Kind = "deepseek-harness"
	SetupCursorInstall        = "cursor_install_url"
	SetupConfigFile           = "config_file"
	SetupTRAEConfigFile       = "trae_config_file"
	SetupDSHProfilePatch      = "dsh_profile_patch"
	serverName                = "lazymind"
)

type Adapter struct {
	kind           Kind
	definition     definition
	binary         string
	discoveryError error
	version        string
	self           string
	bridge         *mcpbridge.Bridge
	home           string
}

type SetupGuide struct {
	Method        string `json:"method"`
	URL           string `json:"url,omitempty"`
	ConfigPath    string `json:"config_path,omitempty"`
	Configuration string `json:"configuration,omitempty"`
}

type Status struct {
	Agent          string      `json:"agent"`
	DisplayName    string      `json:"display_name"`
	Mode           string      `json:"mode"`
	Installed      bool        `json:"installed"`
	Version        string      `json:"version,omitempty"`
	Configured     bool        `json:"configured"`
	Owned          bool        `json:"owned"`
	ServiceReady   bool        `json:"service_ready"`
	Ready          bool        `json:"ready"`
	Command        string      `json:"command,omitempty"`
	Arguments      []string    `json:"arguments,omitempty"`
	Endpoint       string      `json:"endpoint,omitempty"`
	Tools          []string    `json:"tools,omitempty"`
	Setup          *SetupGuide `json:"setup,omitempty"`
	ReadinessError string      `json:"readiness_error,omitempty"`
}

type definition struct {
	agent       string
	displayName string
	environment string
	names       []string
	candidates  []string
	notFound    string
}

type stdioMCPDefinition struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

type mcpFile struct {
	MCPServers map[string]stdioMCPDefinition `json:"mcpServers"`
}

func New(kind Kind, binary, self string, bridge *mcpbridge.Bridge) (*Adapter, error) {
	if bridge == nil {
		return nil, errors.New("MCP bridge is required")
	}
	def, err := definitionFor(kind)
	if err != nil {
		return nil, err
	}
	resolvedBinary, version, discoveryError := discover(kind, binary, def)
	if discoveryError != nil {
		if strings.TrimSpace(binary) != "" || strings.TrimSpace(os.Getenv(def.environment)) != "" {
			discoveryError = fmt.Errorf("resolve configured %s: %w", def.displayName, discoveryError)
		} else {
			discoveryError = errors.New(def.notFound)
		}
	}
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
	home := strings.TrimSpace(os.Getenv("LAZYMIND_HOME"))
	if home != "" {
		home, err = filepath.Abs(home)
		if err != nil {
			return nil, fmt.Errorf("resolve LAZYMIND_HOME: %w", err)
		}
		home = filepath.Clean(home)
	}
	return &Adapter{
		kind: kind, definition: def, binary: resolvedBinary, discoveryError: discoveryError,
		version: version, self: resolvedSelf, bridge: bridge, home: home,
	}, nil
}

func (a *Adapter) Status(ctx context.Context) Status {
	probe, probeErr := a.bridge.Probe(ctx)
	return a.StatusWithProbe(probe, probeErr)
}

func (a *Adapter) StatusWithProbe(probe mcpbridge.ProbeResult, probeErr error) Status {
	status := Status{
		Agent: a.definition.agent, DisplayName: a.definition.displayName,
		Mode: "managed", Installed: a.discoveryError == nil, Version: a.version,
	}
	var problems []string
	if a.discoveryError != nil {
		problems = append(problems, a.discoveryError.Error())
	} else {
		guide, err := a.setupGuide()
		if err != nil {
			problems = append(problems, fmt.Sprintf("build %s setup instructions: %v", a.definition.displayName, err))
		} else {
			status.Setup = &guide
			state, stateErr := readManagedConfig(a.kind, guide.ConfigPath, a.self, a.home)
			if stateErr != nil {
				problems = append(problems, fmt.Sprintf("read %s MCP configuration: %v", a.definition.displayName, stateErr))
			} else {
				status.Configured = state.configured
				status.Owned = state.owned
				status.Command = state.command
				status.Arguments = append([]string(nil), state.arguments...)
				if state.configured && !state.owned {
					problems = append(problems, "an MCP server named `lazymind` already exists and is not managed by this LazyMind installation")
				}
			}
		}
	}
	if probeErr != nil {
		problems = append(problems, "LazyMind MCP preflight failed: "+probeErr.Error())
	} else {
		status.ServiceReady = true
		status.Endpoint = probe.Endpoint
		status.Tools = probe.Tools
	}
	status.Ready = status.Installed && status.ServiceReady && status.Configured && status.Owned
	status.ReadinessError = strings.Join(problems, "; ")
	return status
}

func (a *Adapter) Connect(ctx context.Context) (Status, error) {
	if a.discoveryError != nil {
		return Status{}, a.discoveryError
	}
	guide, err := a.setupGuide()
	if err != nil {
		return Status{}, err
	}
	state, err := readManagedConfig(a.kind, guide.ConfigPath, a.self, a.home)
	if err != nil {
		return Status{}, fmt.Errorf("read %s MCP configuration: %w", a.definition.displayName, err)
	}
	if state.configured && !state.owned {
		return Status{}, errors.New("an MCP server named `lazymind` already exists and is not managed by this LazyMind installation")
	}
	if _, err := a.bridge.Probe(ctx); err != nil {
		return Status{}, fmt.Errorf("LazyMind MCP preflight failed: %w", err)
	}
	if !state.configured {
		if err := writeManagedConfig(a.kind, guide.ConfigPath, a.self, a.home); err != nil {
			return Status{}, fmt.Errorf("configure %s MCP: %w", a.definition.displayName, err)
		}
	}
	status := a.Status(ctx)
	if !status.Configured || !status.Owned {
		return Status{}, fmt.Errorf("%s did not persist the expected LazyMind MCP configuration", a.definition.displayName)
	}
	return status, nil
}

func (a *Adapter) Disconnect(ctx context.Context) (Status, error) {
	if a.discoveryError != nil {
		return a.Status(ctx), nil
	}
	guide, err := a.setupGuide()
	if err != nil {
		return Status{}, err
	}
	state, err := readManagedConfig(a.kind, guide.ConfigPath, a.self, a.home)
	if err != nil {
		return Status{}, fmt.Errorf("read %s MCP configuration: %w", a.definition.displayName, err)
	}
	if state.configured && !state.owned {
		return Status{}, errors.New("the MCP server named `lazymind` is not managed by this LazyMind installation and will not be removed")
	}
	if state.configured {
		if err := removeManagedConfig(a.kind, guide.ConfigPath); err != nil {
			return Status{}, fmt.Errorf("disconnect %s MCP: %w", a.definition.displayName, err)
		}
	}
	return a.Status(ctx), nil
}

func (a *Adapter) setupGuide() (SetupGuide, error) {
	environment := map[string]string(nil)
	if a.home != "" {
		environment = map[string]string{"LAZYMIND_HOME": a.home}
	}
	stdio := stdioMCPDefinition{
		Type: "stdio", Command: a.self, Args: []string{"mcp", "proxy"}, Env: environment,
	}
	switch a.kind {
	case Cursor:
		configuration, err := marshalMCPFile(stdio)
		if err != nil {
			return SetupGuide{}, err
		}
		body, err := json.Marshal(stdio)
		if err != nil {
			return SetupGuide{}, err
		}
		query := url.Values{
			"name":   []string{serverName},
			"config": []string{base64.StdEncoding.EncodeToString(body)},
		}
		return SetupGuide{
			Method:     SetupCursorInstall,
			URL:        "https://cursor.com/en/install-mcp?" + query.Encode(),
			ConfigPath: userPath(".cursor", "mcp.json"), Configuration: configuration,
		}, nil
	case WorkBuddy:
		configuration, err := marshalMCPFile(stdio)
		if err != nil {
			return SetupGuide{}, err
		}
		return SetupGuide{
			Method: SetupConfigFile, ConfigPath: userPath(".codebuddy", "mcp.json"),
			Configuration: configuration,
		}, nil
	case TRAEWork:
		configuration, err := marshalMCPFile(stdio)
		if err != nil {
			return SetupGuide{}, err
		}
		return SetupGuide{
			Method: SetupTRAEConfigFile, ConfigPath: traeWorkConfigPath(),
			Configuration: configuration,
		}, nil
	case DeepSeekHarness:
		return SetupGuide{
			Method: SetupDSHProfilePatch, ConfigPath: dshProfilePatchPath(),
			Configuration: dshProfilePatch(a.self, environment),
		}, nil
	default:
		return SetupGuide{}, fmt.Errorf("unsupported MCP client %q", a.kind)
	}
}

func marshalMCPFile(stdio stdioMCPDefinition) (string, error) {
	configuration, err := json.MarshalIndent(mcpFile{
		MCPServers: map[string]stdioMCPDefinition{serverName: stdio},
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(configuration), nil
}

func definitionFor(kind Kind) (definition, error) {
	home, _ := os.UserHomeDir()
	switch kind {
	case Cursor:
		return definition{
			agent: "cursor", displayName: "Cursor", environment: "LAZYMIND_CURSOR_BIN",
			names: executableNames("cursor"), candidates: cursorCandidates(home),
			notFound: "Cursor is not installed; install Cursor before configuring LazyMind MCP",
		}, nil
	case WorkBuddy:
		return definition{
			agent: "workbuddy", displayName: "WorkBuddy", environment: "LAZYMIND_WORKBUDDY_BIN",
			names: executableNames("buddycn", "codebuddy"), candidates: workBuddyCandidates(home),
			notFound: "WorkBuddy (CodeBuddy CN) is not installed; install WorkBuddy before configuring LazyMind MCP",
		}, nil
	case TRAEWork:
		return definition{
			agent: "traework", displayName: "TRAE Work", environment: "LAZYMIND_TRAE_WORK_BIN",
			names: executableNames("trae-solo-cn", "trae"), candidates: traeWorkCandidates(home),
			notFound: "TRAE Work is not installed; install TRAE Work before configuring LazyMind MCP",
		}, nil
	case DeepSeekHarness:
		return definition{
			agent: "deepseek-harness", displayName: "DeepSeek Harness", environment: "LAZYMIND_DSH_BIN",
			notFound: "DeepSeek Harness web profile is not installed; run npx @deepseek-ai/dsh web first",
		}, nil
	default:
		return definition{}, fmt.Errorf("unsupported MCP client %q", kind)
	}
}

func discover(kind Kind, configured string, def definition) (string, string, error) {
	if kind == DeepSeekHarness && strings.TrimSpace(configured) == "" && strings.TrimSpace(os.Getenv(def.environment)) == "" {
		return discoverDSHProfile()
	}
	binary, err := agentexec.Find(configured, def.environment, def.names, def.candidates)
	if err != nil {
		return "", "", err
	}
	return binary, "", nil
}

func discoverDSHProfile() (string, string, error) {
	profile := filepath.Join(dshHome(), "profiles", "web", "package.json")
	if _, err := os.Stat(profile); err != nil {
		return "", "", err
	}
	mcpClient := filepath.Join(dshHome(), "profiles", "node_modules", "@deepseek-ai", "dsh-mcp-client", "package.json")
	if _, err := os.Stat(mcpClient); err != nil {
		return "", "", fmt.Errorf("DeepSeek Harness MCP client is unavailable: %w", err)
	}
	packageFile := filepath.Join(dshHome(), "profiles", "node_modules", "@deepseek-ai", "dsh", "package.json")
	body, err := os.ReadFile(packageFile)
	if err != nil {
		return profile, "", nil
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return "", "", fmt.Errorf("read DeepSeek Harness version: %w", err)
	}
	return profile, strings.TrimSpace(manifest.Version), nil
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
	if home := environment["LAZYMIND_HOME"]; home != "" {
		lines = append(lines, "        env:", "          LAZYMIND_HOME: "+strconv.Quote(home))
	}
	lines = append(lines, "        failOnStartupError: true")
	return strings.Join(lines, "\n") + "\n"
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

func dshProfilePatchPath() string {
	return filepath.Join(dshHome(), "profiles", "web", "cordis.patch.yml")
}

func userPath(parts ...string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return filepath.Join(append([]string{"~"}, parts...)...)
	}
	return filepath.Join(append([]string{home}, parts...)...)
}

func executableNames(names ...string) []string {
	if runtime.GOOS != "windows" {
		return names
	}
	result := make([]string, 0, len(names)*2)
	for _, name := range names {
		result = append(result, name+".cmd", name+".exe")
	}
	return result
}

func cursorCandidates(home string) []string {
	switch runtime.GOOS {
	case "darwin":
		return appCandidates(home, "Cursor.app", "cursor")
	case "windows":
		root := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if root == "" {
			return nil
		}
		return []string{
			filepath.Join(root, "Programs", "cursor", "resources", "app", "bin", "cursor.cmd"),
			filepath.Join(root, "Programs", "Cursor", "resources", "app", "bin", "cursor.cmd"),
		}
	default:
		return nil
	}
}

func workBuddyCandidates(home string) []string {
	switch runtime.GOOS {
	case "darwin":
		return append(appCandidates(home, "CodeBuddy CN.app", "code"), appCandidates(home, "WorkBuddy.app", "code")...)
	case "windows":
		root := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if root == "" {
			return nil
		}
		return []string{
			filepath.Join(root, "Programs", "CodeBuddy CN", "bin", "buddycn.cmd"),
			filepath.Join(root, "Programs", "CodeBuddy CN", "resources", "app", "bin", "code.cmd"),
			filepath.Join(root, "Programs", "WorkBuddy", "resources", "app", "bin", "code.cmd"),
		}
	default:
		return nil
	}
}

func traeWorkCandidates(home string) []string {
	switch runtime.GOOS {
	case "darwin":
		return append(appCandidates(home, "TRAE SOLO CN.app", "trae-solo-cn"), appCandidates(home, "TRAE.app", "trae")...)
	case "windows":
		root := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if root == "" {
			return nil
		}
		return []string{
			filepath.Join(root, "Programs", "TRAE SOLO CN", "resources", "app", "bin", "trae-solo-cn.cmd"),
			filepath.Join(root, "Programs", "TRAE", "resources", "app", "bin", "trae.cmd"),
		}
	default:
		return nil
	}
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

func appCandidates(home, application, binary string) []string {
	result := []string{filepath.Join("/Applications", application, "Contents", "Resources", "app", "bin", binary)}
	if home != "" {
		result = append(result, filepath.Join(home, "Applications", application, "Contents", "Resources", "app", "bin", binary))
	}
	return result
}
