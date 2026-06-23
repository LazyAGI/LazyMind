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
		Services: map[string]RuntimeServiceState{
			processComposeServiceName: {
				Kind:   "docker-compose",
				Status: "stopped",
			},
			localProxyProcessName: {
				Kind:   "host-process",
				Status: "stopped",
			},
		},
		OverallStatus: "unknown",
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
}

func itoa(v int) string {
	return strconv.Itoa(v)
}

func newStateWithServiceStatus(state RuntimeState, serviceStatus string) RuntimeState {
	if state.Services == nil {
		state.Services = map[string]RuntimeServiceState{}
	}
	ds := state.Services[processComposeServiceName]
	ds.Kind = "docker-compose"
	ds.Status = serviceStatus
	state.Services[processComposeServiceName] = ds
	lp := state.Services[localProxyProcessName]
	lp.Kind = "host-process"
	lp.Status = serviceStatus
	state.Services[localProxyProcessName] = lp
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
	if st.Services == nil {
		st.Services = map[string]RuntimeServiceState{}
	}
	if _, ok := st.Services[processComposeServiceName]; !ok {
		st.Services[processComposeServiceName] = RuntimeServiceState{
			Kind:   "docker-compose",
			Status: "unknown",
		}
	}
	if _, ok := st.Services[localProxyProcessName]; !ok {
		st.Services[localProxyProcessName] = RuntimeServiceState{
			Kind:   "host-process",
			Status: "unknown",
		}
	}
	return st, nil
}
