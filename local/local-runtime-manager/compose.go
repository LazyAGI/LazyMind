package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ComposeManager struct {
	runner CommandRunner
}

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

func (m *ComposeManager) ComposeReady(ctx context.Context, repoRoot string, profile string) error {
	_ = profile
	args := append(m.composeBaseArgs(repoRoot), "ps", "--status", "running", "--services")
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot})
	if err != nil {
		return fmt.Errorf("docker compose ps failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	running := parseServiceLines(res.Stdout)
	if len(running) == 0 {
		return fmt.Errorf("compose readiness: no running services")
	}
	return nil
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
	args := append(m.composeBaseArgs(repoRoot), "up")
	args = append(args, remaining...)
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
