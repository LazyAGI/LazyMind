package editor

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
	"strings"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/mcpbridge"
)

type Kind string

const (
	Cursor             Kind = "cursor"
	WorkBuddy          Kind = "workbuddy"
	SetupCursorInstall      = "cursor_install_url"
	SetupConfigFile         = "config_file"
	serverName              = "lazymind"
)

type Adapter struct {
	definition     definition
	binary         string
	discoveryError error
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
	ServiceReady   bool        `json:"service_ready"`
	Ready          bool        `json:"ready"`
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
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

type cursorMCPFile struct {
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
	resolvedBinary, discoveryError := agentexec.Find(binary, def.environment, def.names, def.candidates)
	if discoveryError != nil {
		if strings.TrimSpace(binary) != "" || strings.TrimSpace(os.Getenv(def.environment)) != "" {
			discoveryError = fmt.Errorf("resolve configured %s CLI: %w", def.displayName, discoveryError)
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
		definition: def, binary: resolvedBinary, discoveryError: discoveryError,
		self: resolvedSelf, bridge: bridge, home: home,
	}, nil
}

func (a *Adapter) Status(ctx context.Context) Status {
	status := Status{
		Agent: a.definition.agent, DisplayName: a.definition.displayName,
		Mode: "manual", Installed: a.discoveryError == nil,
	}
	var problems []string
	if a.discoveryError != nil {
		problems = append(problems, a.discoveryError.Error())
	} else {
		version, err := agentexec.Run(ctx, a.binary, "--version")
		if err != nil {
			problems = append(problems, fmt.Sprintf("read %s version: %v", a.definition.displayName, err))
		} else {
			status.Version = firstLine(version)
		}
		guide, err := a.setupGuide()
		if err != nil {
			problems = append(problems, fmt.Sprintf("build %s setup instructions: %v", a.definition.displayName, err))
		} else {
			status.Setup = &guide
		}
	}
	probe, err := a.bridge.Probe(ctx)
	if err != nil {
		problems = append(problems, "LazyMind MCP preflight failed: "+err.Error())
	} else {
		status.ServiceReady = true
		status.Endpoint = probe.Endpoint
		status.Tools = probe.Tools
	}
	status.Ready = status.Installed && status.ServiceReady && status.Setup != nil
	status.ReadinessError = strings.Join(problems, "; ")
	return status
}

func (a *Adapter) setupGuide() (SetupGuide, error) {
	environment := map[string]string(nil)
	if a.home != "" {
		environment = map[string]string{"LAZYMIND_HOME": a.home}
	}
	stdio := stdioMCPDefinition{
		Type: "stdio", Command: a.self, Args: []string{"mcp", "proxy"}, Env: environment,
	}
	configuration, err := json.MarshalIndent(cursorMCPFile{
		MCPServers: map[string]stdioMCPDefinition{serverName: stdio},
	}, "", "  ")
	if err != nil {
		return SetupGuide{}, err
	}
	userHome, _ := os.UserHomeDir()
	if a.definition.agent == string(Cursor) {
		body, err := json.Marshal(stdio)
		if err != nil {
			return SetupGuide{}, err
		}
		query := url.Values{
			"name":   []string{serverName},
			"config": []string{base64.StdEncoding.EncodeToString(body)},
		}
		configPath := filepath.Join("~", ".cursor", "mcp.json")
		if userHome != "" {
			configPath = filepath.Join(userHome, ".cursor", "mcp.json")
		}
		return SetupGuide{
			Method:     SetupCursorInstall,
			URL:        "https://cursor.com/en/install-mcp?" + query.Encode(),
			ConfigPath: configPath, Configuration: string(configuration),
		}, nil
	}

	configPath := filepath.Join("~", ".codebuddy", "mcp.json")
	if userHome != "" {
		configPath = filepath.Join(userHome, ".codebuddy", "mcp.json")
	}
	return SetupGuide{
		Method: SetupConfigFile, ConfigPath: configPath, Configuration: string(configuration),
	}, nil
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
	default:
		return definition{}, fmt.Errorf("unsupported editor Agent %q", kind)
	}
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

func appCandidates(home, application, binary string) []string {
	result := []string{filepath.Join("/Applications", application, "Contents", "Resources", "app", "bin", binary)}
	if home != "" {
		result = append(result, filepath.Join(home, "Applications", application, "Contents", "Resources", "app", "bin", binary))
	}
	return result
}

func firstLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
