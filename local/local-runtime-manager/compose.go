package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ComposeManager struct {
	runner CommandRunner
}

type ComposeServiceStatus struct {
	Name     string
	Service  string
	State    string
	Health   string
	ExitCode int
}

type composeReadinessState int

const (
	composeReadinessPending composeReadinessState = iota
	composeReadinessReady
	composeReadinessFailed
)

func NewComposeManager(r CommandRunner) *ComposeManager {
	return &ComposeManager{runner: r}
}

func (m *ComposeManager) composeBaseArgs(repoRoot string) []string {
	return []string{
		"compose",
		"-f", filepath.Join(repoRoot, repoComposeFileName),
		"-f", filepath.Join(repoRoot, localComposeOverrideName),
	}
}

func (m *ComposeManager) ComposeServices(ctx context.Context, repoRoot string) ([]string, error) {
	args := append(m.composeBaseArgs(repoRoot), "config", "--services")
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot})
	if err != nil {
		return nil, fmt.Errorf("docker compose config --services failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	services := parseServiceLines(res.Stdout)
	return services, nil
}

func (m *ComposeManager) ComposeDown(ctx context.Context, repoRoot string, profile string) error {
	_ = profile
	args := append(m.composeBaseArgs(repoRoot), "down", "--remove-orphans")
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot})
	if err != nil {
		return fmt.Errorf("docker compose down failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *ComposeManager) ComposePS(ctx context.Context, repoRoot string) (string, error) {
	args := append(m.composeBaseArgs(repoRoot), "ps", "-a")
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot})
	if err != nil {
		return res.Stdout + res.Stderr, fmt.Errorf("docker compose ps failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

func (m *ComposeManager) ComposeStatus(ctx context.Context, repoRoot string) ([]ComposeServiceStatus, error) {
	args := append(m.composeBaseArgs(repoRoot), "ps", "-a", "--format", "json")
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot})
	if err != nil {
		return nil, fmt.Errorf("docker compose ps --format json failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return parseComposeStatusJSON(res.Stdout)
}

func (m *ComposeManager) ComposeHasContainers(ctx context.Context, repoRoot string) (bool, error) {
	statuses, err := m.ComposeStatus(ctx, repoRoot)
	if err != nil {
		return false, err
	}
	return len(statuses) > 0, nil
}

func (m *ComposeManager) ComposeUp(ctx context.Context, repoRoot string, profile string) error {
	services, err := m.ComposeServices(ctx, repoRoot)
	if err != nil {
		return err
	}
	disabled, err := parseRuntimeOverlay(filepath.Join(repoRoot, localComposeOverrideName))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	remaining, err := filterRemainingServices(services, disabled.DisabledContainerTypes)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		_ = profile
	}
	args := append(m.composeBaseArgs(repoRoot), "up", "--build")
	for _, svc := range disabled.DisabledContainerTypes {
		if svc == "" {
			continue
		}
		args = append(args, "--scale", svc+"=0")
	}
	args = append(args, remaining...)
	if streamer, ok := m.runner.(CommandStreamer); ok {
		err := streamer.Stream(ctx, Command{Name: "docker", Args: args, Dir: repoRoot}, os.Stdout, os.Stderr)
		if err != nil {
			return fmt.Errorf("docker compose up failed: %w", err)
		}
		return nil
	}
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot})
	if err != nil {
		return fmt.Errorf("docker compose up failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func filterRemainingServices(allServices []string, disabled []string) ([]string, error) {
	available := make(map[string]struct{}, len(allServices))
	for _, svc := range allServices {
		available[svc] = struct{}{}
	}
	disabledSet := map[string]struct{}{}
	for _, d := range disabled {
		if d == "" {
			continue
		}
		if _, ok := available[d]; !ok {
			return nil, fmt.Errorf("unknown disabled service: %s", d)
		}
		disabledSet[d] = struct{}{}
	}
	remaining := make([]string, 0, len(allServices))
	for _, svc := range allServices {
		if _, disabled := disabledSet[svc]; disabled {
			continue
		}
		remaining = append(remaining, svc)
	}
	return remaining, nil
}

func parseComposeStatusJSON(raw string) ([]ComposeServiceStatus, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var rows []struct {
		Name     string `json:"Name"`
		Service  string `json:"Service"`
		State    string `json:"State"`
		Health   string `json:"Health"`
		ExitCode int    `json:"ExitCode"`
	}
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var row struct {
				Name     string `json:"Name"`
				Service  string `json:"Service"`
				State    string `json:"State"`
				Health   string `json:"Health"`
				ExitCode int    `json:"ExitCode"`
			}
			if err2 := json.Unmarshal([]byte(line), &row); err2 != nil {
				rows = nil
				break
			}
			rows = append(rows, row)
		}
		if rows == nil {
			return nil, err
		}
	}
	statuses := make([]ComposeServiceStatus, 0, len(rows))
	for _, row := range rows {
		statuses = append(statuses, ComposeServiceStatus{
			Name:     row.Name,
			Service:  row.Service,
			State:    strings.ToLower(row.State),
			Health:   strings.ToLower(row.Health),
			ExitCode: row.ExitCode,
		})
	}
	return statuses, nil
}

func classifyComposeReadiness(statuses []ComposeServiceStatus) (composeReadinessState, string) {
	if len(statuses) == 0 {
		return composeReadinessPending, "no containers created yet"
	}
	for _, st := range statuses {
		service := st.Service
		if service == "" {
			service = st.Name
		}
		if service == "db-bootstrap" {
			if st.State == "exited" && st.ExitCode != 0 {
				return composeReadinessFailed, fmt.Sprintf("service %s exited with code %d", service, st.ExitCode)
			}
			continue
		}
		if st.State == "exited" || st.State == "dead" || st.State == "removing" {
			return composeReadinessFailed, fmt.Sprintf("service %s is %s (exit=%d)", service, st.State, st.ExitCode)
		}
		if st.Health == "unhealthy" {
			return composeReadinessFailed, fmt.Sprintf("service %s is unhealthy", service)
		}
	}
	for _, st := range statuses {
		service := st.Service
		if service == "" {
			service = st.Name
		}
		if service == "db-bootstrap" {
			if st.State == "exited" && st.ExitCode == 0 {
				continue
			}
			return composeReadinessPending, fmt.Sprintf("service %s is %s", service, st.State)
		}
		if st.State != "running" {
			return composeReadinessPending, fmt.Sprintf("service %s is %s", service, st.State)
		}
		if st.Health != "" && st.Health != "healthy" {
			return composeReadinessPending, fmt.Sprintf("service %s health is %s", service, st.Health)
		}
	}
	return composeReadinessReady, "all services ready"
}
