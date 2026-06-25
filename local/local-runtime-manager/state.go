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
	RepoRoot       string                         `json:"repoRoot"`
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
	FrontendPort       int               `json:"frontendPort,omitempty"`
	LocalProxy         LocalProxyConfig  `json:"localProxy,omitempty"`
	AuthService        AuthServiceConfig `json:"authService,omitempty"`
	Algorithm          AlgorithmConfig   `json:"algorithm,omitempty"`
	ProcessComposePort int               `json:"processComposePort,omitempty"`
}

type RuntimeServiceState struct {
	Kind   string `json:"kind"`
	Status string `json:"status"`
}

type StatusResponse struct {
	Runtime        string                         `json:"runtime"`
	Profile        string                         `json:"profile"`
	OverallStatus  string                         `json:"overallStatus"`
	RepoRoot       string                         `json:"repoRoot"`
	RuntimeRoot    string                         `json:"runtimeRoot"`
	ProcessCompose ProcessComposeState            `json:"processCompose"`
	Services       map[string]RuntimeServiceState `json:"services"`
}

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
	return RuntimeState{
		Version:     processComposeVersion,
		Runtime:     "local",
		Profile:     cfg.Profile,
		RepoRoot:    cfg.RepoRoot,
		RuntimeRoot: cfg.RuntimeRoot,
		ProcessCompose: ProcessComposeState{
			APIPort:   apiPort,
			APIRoot:   "http://127.0.0.1:" + itoa(apiPort),
			TokenFile: tokenPath,
			PID:       0,
		},
		Config: snapshotRuntimeConfig(cfg),
		Services: map[string]RuntimeServiceState{
			processComposeServiceName: {
				Kind:   "docker-compose",
				Status: "stopped",
			},
			localProxyProcessName: {
				Kind:   "host-process",
				Status: "stopped",
			},
			authServiceProcessName: {
				Kind:   "host-process",
				Status: "stopped",
			},
			frontendProcessName: {
				Kind:   "host-process",
				Status: "stopped",
			},
			coreProcessName: {
				Kind:   "host-process",
				Status: "stopped",
			},
			docServerProcessName: {
				Kind:   "host-process",
				Status: "stopped",
			},
			processorServerProcessName: {
				Kind:   "host-process",
				Status: "stopped",
			},
			processorWorkerProcessName: {
				Kind:   "host-process",
				Status: "stopped",
			},
			algoProcessName: {
				Kind:   "host-process",
				Status: "stopped",
			},
			chatProcessName: {
				Kind:   "host-process",
				Status: "stopped",
			},
		},
		OverallStatus: "unknown",
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
}

func snapshotRuntimeConfig(cfg RuntimeConfig) RuntimeConfigSnapshot {
	return RuntimeConfigSnapshot{
		FrontendPort:       cfg.FrontendPort,
		LocalProxy:         cfg.LocalProxy,
		AuthService:        cfg.AuthService,
		Algorithm:          cfg.Algorithm,
		ProcessComposePort: cfg.ProcessComposePort,
	}
}

func applyStateConfig(cfg RuntimeConfig, state RuntimeState) RuntimeConfig {
	if state.Config.ProcessComposePort > 0 {
		cfg.ProcessComposePort = state.Config.ProcessComposePort
	}
	if state.Config.FrontendPort > 0 {
		cfg.FrontendPort = state.Config.FrontendPort
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
	return cfg
}

func itoa(v int) string {
	return strconv.Itoa(v)
}

func newStateWithServiceStatus(state RuntimeState, serviceStatus string) RuntimeState {
	state.Services = normalizeRuntimeServices(state.Services)
	ds := state.Services[processComposeServiceName]
	ds.Kind = "docker-compose"
	ds.Status = serviceStatus
	state.Services[processComposeServiceName] = ds
	lp := state.Services[localProxyProcessName]
	lp.Kind = "host-process"
	lp.Status = serviceStatus
	state.Services[localProxyProcessName] = lp
	auth := state.Services[authServiceProcessName]
	auth.Kind = "host-process"
	auth.Status = serviceStatus
	state.Services[authServiceProcessName] = auth
	fe := state.Services[frontendProcessName]
	fe.Kind = "host-process"
	fe.Status = serviceStatus
	state.Services[frontendProcessName] = fe
	core := state.Services[coreProcessName]
	core.Kind = "host-process"
	core.Status = serviceStatus
	state.Services[coreProcessName] = core
	for _, name := range []string{
		docServerProcessName,
		processorServerProcessName,
		processorWorkerProcessName,
		algoProcessName,
		chatProcessName,
	} {
		svc := state.Services[name]
		svc.Kind = "host-process"
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
	st.Services = normalizeRuntimeServices(st.Services)
	return st, nil
}

func normalizeRuntimeServices(services map[string]RuntimeServiceState) map[string]RuntimeServiceState {
	if services == nil {
		services = map[string]RuntimeServiceState{}
	}
	normalized := map[string]RuntimeServiceState{}
	if _, ok := services[processComposeServiceName]; !ok {
		normalized[processComposeServiceName] = RuntimeServiceState{
			Kind:   "docker-compose",
			Status: "unknown",
		}
	} else {
		svc := services[processComposeServiceName]
		svc.Kind = "docker-compose"
		normalized[processComposeServiceName] = svc
	}
	if _, ok := services[localProxyProcessName]; !ok {
		normalized[localProxyProcessName] = RuntimeServiceState{
			Kind:   "host-process",
			Status: "unknown",
		}
	} else {
		svc := services[localProxyProcessName]
		svc.Kind = "host-process"
		normalized[localProxyProcessName] = svc
	}
	if _, ok := services[authServiceProcessName]; !ok {
		normalized[authServiceProcessName] = RuntimeServiceState{
			Kind:   "host-process",
			Status: "unknown",
		}
	} else {
		svc := services[authServiceProcessName]
		svc.Kind = "host-process"
		normalized[authServiceProcessName] = svc
	}
	if _, ok := services[frontendProcessName]; !ok {
		normalized[frontendProcessName] = RuntimeServiceState{
			Kind:   "host-process",
			Status: "unknown",
		}
	} else {
		svc := services[frontendProcessName]
		svc.Kind = "host-process"
		normalized[frontendProcessName] = svc
	}
	if _, ok := services[coreProcessName]; !ok {
		normalized[coreProcessName] = RuntimeServiceState{
			Kind:   "host-process",
			Status: "unknown",
		}
	} else {
		svc := services[coreProcessName]
		svc.Kind = "host-process"
		normalized[coreProcessName] = svc
	}
	for _, name := range []string{
		docServerProcessName,
		processorServerProcessName,
		processorWorkerProcessName,
		algoProcessName,
		chatProcessName,
	} {
		if _, ok := services[name]; !ok {
			normalized[name] = RuntimeServiceState{
				Kind:   "host-process",
				Status: "unknown",
			}
		} else {
			svc := services[name]
			svc.Kind = "host-process"
			normalized[name] = svc
		}
	}
	return normalized
}
