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
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/codexruntime"
	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/mcpbridge"
	"lazymind/agentconnector/internal/workflowhost"
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
	sharedRuntime  func(context.Context, bool, func() error) error
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
		return agentintegration.Fail(status, err.Error())
	}
	switch {
	case exists && !a.isOwned(config):
		status.State = agentintegration.Conflict
		status.Message = "Codex already has an MCP server named `lazymind` that is not managed by this LazyMind installation."
	case !exists && a.pluginEnabled():
		status.State = agentintegration.Enabled
		status.Message = "LazyMind Workflow plugin is installed. Open a new Codex task to load its tools."
		if codexruntime.Supported() {
			profile, err := codexHome()
			if err != nil {
				return agentintegration.Fail(status, err.Error())
			}
			registration, err := codexruntime.Read(profile)
			if err != nil {
				return agentintegration.Fail(status, err.Error())
			}
			if registration == nil {
				status.State = agentintegration.Ready
				status.Message = "Workflow 插件已安装；点击连接以完成共享模式配置。"
			} else {
				state := codexruntime.ConnectionState(registration)
				status.CanConnect, status.CanDisconnect = true, true
				if state == "connected" {
					status.Message = "Codex 桌面已连接共享后台；会话控制通道已连接。"
				} else {
					status.State = agentintegration.ActionRequired
					status.Issue = &agentintegration.Issue{Code: "codex_shared_" + state}
					switch state {
					case "invalidated":
						status.Message = "Codex 共享配置已失效（应用升级或运行文件变化）；请关闭联动后重新配置。"
					case "failed":
						status.Message = "Codex 共享后台连接失败，会话控制不可用；请检查共享运行日志，或关闭联动恢复默认启动。"
					default:
						status.Message = "共享模式已配置，等待 Codex 重启并连接后台；当前尚不能控制会话。"
					}
				}
			}
		} else {
			status.Message = "Workflow 插件已安装；当前平台使用 queue 模式，尚不支持原生停止。"
		}

	case agentintegration.MissingRequirement(requirements):
		status.State = agentintegration.RequirementsMissing
	default:
		status.State = agentintegration.Ready
		if exists {
			status.Message = "Reconnect to upgrade the existing MCP entry to the LazyMind Workflow plugin."
		}
	}
	return status
}

func (a *Adapter) Connect(ctx context.Context) agentintegration.Status {
	if err := CleanupLegacyControl(); err != nil {
		return agentintegration.Fail(a.Status(ctx), "clean up legacy Codex control configuration: "+err.Error())
	}
	status := a.Status(ctx)
	if status.State != agentintegration.Ready && status.State != agentintegration.Enabled && !status.CanConnect {
		return status
	}
	// Installation is local and uses the saved account scope. A temporarily
	// offline Core must not prevent installing the plugin; MCP checks service
	// connectivity and authentication when Codex loads it.
	profile, err := codexHome()
	if err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	err = a.configureShared(ctx, true, func() error {
		if a.pluginEnabled() {
			if _, exists, err := a.getConfig(); err != nil {
				return err
			} else if !exists {
				return nil
			}
		}
		record, err := codexplugin.Install(ctx, a.home, a.self, a.binary, profile, a.hostID)
		if err != nil {
			return fmt.Errorf("install LazyMind Workflow plugin: %w", err)
		}
		if _, exists, err := a.getConfig(); err != nil {
			return err
		} else if exists {
			if _, err := a.run(ctx, "mcp", "remove", serverName); err != nil {
				_, _ = a.run(ctx, "plugin", "remove", codexplugin.Name+"@"+record.Marketplace, "--json")
				return fmt.Errorf("remove legacy MCP entry: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return agentintegration.Fail(a.Status(ctx), err.Error())
	}
	result := a.Status(ctx)
	if codexruntime.Supported() && result.State == agentintegration.Enabled {
		result.Message = "Codex 共享模式已配置。请正常退出并重开 Codex，然后新建 Workflow 使用继续/停止控制。"
	} else if !codexruntime.Supported() {
		result.Message = "Workflow 插件已安装，当前平台使用 queue 模式；共享后台与原生停止尚不支持当前平台。"
	}
	return result
}

// PrepareCLI installs the existing Workflow plugin for CLI execution without
// changing Desktop startup settings or migrating the user's basic MCP entry.
func (a *Adapter) PrepareCLI(ctx context.Context) error {
	if a.discoveryError != nil {
		return a.discoveryError
	}
	if a.pluginEnabled() {
		return nil
	}
	profile, err := codexHome()
	if err != nil {
		return err
	}
	_, err = codexplugin.Install(ctx, a.home, a.self, a.binary, profile, a.hostID)
	return err
}

func (a *Adapter) Disconnect(ctx context.Context) agentintegration.Status {
	if err := CleanupLegacyControl(); err != nil {
		return agentintegration.Fail(a.Status(ctx), "clean up legacy Codex control configuration: "+err.Error())
	}
	status := a.Status(ctx)
	if status.State == agentintegration.Conflict || status.State == agentintegration.Failed {
		return status
	}
	if err := a.configureShared(ctx, false, nil); err != nil {
		return agentintegration.Fail(status, err.Error())
	}
	if record, err := codexplugin.Read(); err == nil && record.Home == a.home {
		if _, err := a.run(ctx, "plugin", "remove", codexplugin.Name+"@"+record.Marketplace, "--json"); err != nil {
			return agentintegration.Fail(status, "remove Codex Workflow plugin: "+err.Error())
		}
		if err := workflowhost.Disable(a.home, record.ConnectorID); err != nil {
			return agentintegration.Fail(status, err.Error())
		}
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
		ExecutableNames: []string{"ChatGPT.exe", "Codex.exe"},
		Protocols:       []string{"chatgpt", "codex"},
		DisplayNames:    []string{"ChatGPT", "Codex"},
		PackageNames:    []string{"OpenAI.Codex"},
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
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return agentexec.Run(runCtx, a.binary, arguments...)
}

func (a *Adapter) isOwned(config mcpConfig) bool {
	return agentexec.SameExecutable(config.Transport.Command, a.self) &&
		len(config.Transport.Args) == 2 &&
		config.Transport.Args[0] == "mcp" && config.Transport.Args[1] == "proxy"
}

func findBinary(configured string) (string, error) {
	var desktop string
	if strings.TrimSpace(configured) == "" && strings.TrimSpace(os.Getenv("LAZYMIND_CODEX_BIN")) == "" {
		if _, bound, err := agentexec.ExecutableBinding(agentexec.CodexCLI); err == nil && !bound {
			// The Windows desktop launcher can also be named Codex.exe. Prefer
			// the bundled CLI before falling back to generic executable discovery.
			desktop, _ = agentexec.FindDesktopApplication(codexDesktopApplication())
			if strings.EqualFold(filepath.Ext(desktop), ".exe") {
				bundled := filepath.Join(filepath.Dir(desktop), "resources", "codex.exe")
				if resolved, err := agentexec.ResolveExecutable(bundled); err == nil {
					return resolved, nil
				}
			}
		}
	}
	resolved, err := agentexec.FindBoundExecutable(
		configured, "LAZYMIND_CODEX_BIN", agentexec.CodexCLI, []string{"codex"}, desktop,
	)
	if err != nil {
		return "", errors.New("Codex CLI is not installed")
	}
	return resolved, nil
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
	store, err := credentials.NewStore(a.home, "")
	if err != nil {
		return false
	}
	account, err := store.AccountScope()
	return err == nil && workflowhost.Configured(a.home, record.ConnectorID, account)
}

func (a *Adapter) configureShared(ctx context.Context, enable bool, install func() error) error {
	if a.sharedRuntime != nil {
		return a.sharedRuntime(ctx, enable, install)
	}
	profile, err := codexHome()
	if err != nil {
		return err
	}
	if enable {
		return codexruntime.WithInstallation(ctx, a.binary, profile, install)
	}
	return codexruntime.Disable(ctx, a.binary, profile)
}
