package main

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"
)

type RuntimeState struct {
	Version        int                            `json:"version"`
	Runtime        string                         `json:"runtime"`
	Profile        string                         `json:"profile"`
	OwnerToken     string                         `json:"ownerToken,omitempty"`
	RepoRoot       string                         `json:"repoRoot"`
	ResourcesRoot  string                         `json:"resourcesRoot,omitempty"`
	RuntimeRoot    string                         `json:"runtimeRoot"`
	ProcessCompose ProcessComposeState            `json:"processCompose"`
	Config         RuntimeConfigSnapshot          `json:"config,omitempty"`
	Services       map[string]RuntimeServiceState `json:"services"`
	OverallStatus  string                         `json:"overallStatus,omitempty"`
	UpdatedAt      string                         `json:"updatedAt"`
}

type ProcessComposeState struct {
	APIPort   int    `json:"apiPort"`
	APIRoot   string `json:"api"`
	TokenFile string `json:"apiTokenFile"`
	PID       int    `json:"pid"`
}

type RuntimeConfigSnapshot struct {
	MaintenanceMode    string                    `json:"maintenanceMode,omitempty"`
	FrontendPort       int                       `json:"frontendPort,omitempty"`
	ModeProfile        RuntimeModeProfileConfig  `json:"modeProfile,omitempty"`
	NetworkProfile     string                    `json:"networkProfile,omitempty"`
	LocalProxy         LocalProxyConfig          `json:"localProxy,omitempty"`
	AuthService        AuthServiceConfig         `json:"authService,omitempty"`
	Algorithm          AlgorithmConfig           `json:"algorithm,omitempty"`
	FileWatcher        FileWatcherConfigSnapshot `json:"fileWatcher,omitempty"`
	ProcessComposePort int                       `json:"processComposePort,omitempty"`
}

type FileWatcherConfigSnapshot struct {
	Port          int    `json:"port,omitempty"`
	AgentID       string `json:"agentId,omitempty"`
	WatchHostDir  string `json:"watchHostDir,omitempty"`
	HostPathStyle string `json:"hostPathStyle,omitempty"`
}

type RuntimeServiceState struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type StatusResponse struct {
	Runtime        string                         `json:"runtime"`
	Profile        string                         `json:"profile"`
	OwnerMatched   bool                           `json:"ownerMatched,omitempty"`
	OverallStatus  string                         `json:"overallStatus"`
	RepoRoot       string                         `json:"repoRoot"`
	ResourcesRoot  string                         `json:"resourcesRoot,omitempty"`
	BuildRoot      string                         `json:"buildRoot,omitempty"`
	RuntimeRoot    string                         `json:"runtimeRoot"`
	DataDir        string                         `json:"dataDir,omitempty"`
	LogsDir        string                         `json:"logsDir,omitempty"`
	ProcessCompose ProcessComposeState            `json:"processCompose"`
	Config         RuntimeConfigSnapshot          `json:"config,omitempty"`
	Services       map[string]RuntimeServiceState `json:"services"`
}

const legacyComposeServiceName = "docker" + "-stack"

func readRuntimeState(path string) (RuntimeState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return RuntimeState{}, err
	}
	var state RuntimeState
	if err := json.Unmarshal(b, &state); err != nil {
		return RuntimeState{}, err
	}
	return state, nil
}

func writeRuntimeState(path string, state RuntimeState) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func defaultRuntimeState(cfg RuntimeConfig, apiPort int, tokenPath string) RuntimeState {
	state := RuntimeState{
		Version:       processComposeVersion,
		Runtime:       cfg.Profile,
		Profile:       cfg.Profile,
		OwnerToken:    cfg.OwnerToken,
		RepoRoot:      cfg.RepoRoot,
		ResourcesRoot: cfg.ResourcesRoot,
		RuntimeRoot:   cfg.RuntimeRoot,
		ProcessCompose: ProcessComposeState{
			APIPort:   apiPort,
			APIRoot:   "http://127.0.0.1:" + itoa(apiPort),
			TokenFile: tokenPath,
			PID:       0,
		},
		Config:        snapshotRuntimeConfig(cfg),
		Services:      map[string]RuntimeServiceState{},
		OverallStatus: "unknown",
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	return newStateWithServiceStatus(state, cfg, "stopped")
}

func snapshotRuntimeConfig(cfg RuntimeConfig) RuntimeConfigSnapshot {
	return RuntimeConfigSnapshot{
		MaintenanceMode: cfg.MaintenanceMode,
		FrontendPort:    cfg.FrontendPort,
		ModeProfile:     cfg.ModeProfile,
		NetworkProfile:  cfg.NetworkProfile,
		LocalProxy:      cfg.LocalProxy,
		AuthService:     cfg.AuthService,
		Algorithm:       cfg.Algorithm,
		FileWatcher: FileWatcherConfigSnapshot{
			Port:          cfg.FileWatcher.Port,
			AgentID:       cfg.FileWatcher.AgentID,
			WatchHostDir:  cfg.FileWatcher.WatchHostDir,
			HostPathStyle: cfg.FileWatcher.HostPathStyle,
		},
		ProcessComposePort: cfg.ProcessComposePort,
	}
}

func applyStateConfig(cfg RuntimeConfig, state RuntimeState) RuntimeConfig {
	if state.Config.MaintenanceMode != "" {
		cfg.MaintenanceMode = state.Config.MaintenanceMode
	}
	if state.Config.ProcessComposePort > 0 {
		cfg.ProcessComposePort = state.Config.ProcessComposePort
	}
	if state.Config.FrontendPort > 0 {
		cfg.FrontendPort = state.Config.FrontendPort
	}
	if state.Config.ModeProfile.Name != "" {
		cfg.ModeProfile = state.Config.ModeProfile
	}
	if state.Config.LocalProxy.Port > 0 {
		cfg.LocalProxy = state.Config.LocalProxy
	}
	if state.Config.AuthService.Port > 0 {
		cfg.AuthService = state.Config.AuthService
	}
	if state.Config.Algorithm.DocPort > 0 {
		cfg.Algorithm = state.Config.Algorithm
	}
	if state.Config.FileWatcher.Port > 0 {
		cfg.FileWatcher.Port = state.Config.FileWatcher.Port
	}
	if state.Config.FileWatcher.AgentID != "" {
		cfg.FileWatcher.AgentID = state.Config.FileWatcher.AgentID
	}
	if state.Config.FileWatcher.WatchHostDir != "" {
		cfg.FileWatcher.WatchHostDir = state.Config.FileWatcher.WatchHostDir
	}
	if state.Config.FileWatcher.HostPathStyle != "" {
		cfg.FileWatcher.HostPathStyle = state.Config.FileWatcher.HostPathStyle
	}
	return cfg
}

func itoa(v int) string {
	return strconv.Itoa(v)
}

func newStateWithServiceStatus(state RuntimeState, cfg RuntimeConfig, serviceStatus string) RuntimeState {
	state.Services = normalizeRuntimeServices(state.Services, cfg)
	for name, svc := range state.Services {
		if name == processComposeServiceName {
			svc.Kind = "host-supervisor"
		} else {
			svc.Kind = "host-process"
		}
		svc.Status = serviceStatus
		state.Services[name] = svc
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return state
}

func readOrNewState(paths RuntimePaths, cfg RuntimeConfig) (RuntimeState, error) {
	st, err := readRuntimeState(paths.StateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultRuntimeState(cfg, cfg.ProcessComposePort, paths.RunDirTokenFile), nil
		}
		return RuntimeState{}, err
	}
	if st.ProcessCompose.APIPort == 0 {
		st.ProcessCompose.APIPort = cfg.ProcessComposePort
	}
	st.Services = normalizeRuntimeServices(st.Services, applyStateConfig(cfg, st))
	return st, nil
}

func normalizeRuntimeServices(services map[string]RuntimeServiceState, cfg RuntimeConfig) map[string]RuntimeServiceState {
	if services == nil {
		services = map[string]RuntimeServiceState{}
	}
	normalized := map[string]RuntimeServiceState{}
	for _, name := range buildRuntimeProcessPlan(cfg).serviceNames() {
		svc, ok := services[name]
		if name == processComposeServiceName && !ok {
			svc, ok = services[legacyComposeServiceName]
		}
		if !ok || svc.Status == "" {
			svc.Status = "unknown"
		}
		if name == processComposeServiceName {
			svc.Kind = "host-supervisor"
		} else {
			svc.Kind = "host-process"
		}
		normalized[name] = svc
	}
	return normalized
}
