package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

func (m *ComposeManager) composeArgs(repoRoot string) []string {
	args := m.composeBaseArgs(repoRoot)
	args = append(args, derivedComposeProfileArgs()...)
	return args
}

func derivedComposeProfileArgs() []string {
	profiles := []string{}
	if enabledFromEnv("LAZYMIND_DEPLOY_MINERU") {
		profiles = append(profiles, "mineru")
	}
	if isBuiltInServiceURI("LAZYMIND_MILVUS_URI", "http://milvus:19530") {
		profiles = append(profiles, "milvus")
	}
	if isBuiltInServiceURI("LAZYMIND_OPENSEARCH_URI", "https://opensearch:9200") {
		profiles = append(profiles, "opensearch")
	}
	if enabledFromEnv("LAZYMIND_ENABLE_MILVUS_DASHBOARD") && containsProfile(profiles, "milvus") {
		profiles = append(profiles, "milvus-dashboard")
	}
	if enabledFromEnv("LAZYMIND_ENABLE_OPENSEARCH_DASHBOARD") && containsProfile(profiles, "opensearch") {
		profiles = append(profiles, "opensearch-dashboard")
	}

	args := make([]string, 0, len(profiles)*2)
	for _, profile := range profiles {
		args = append(args, "--profile", profile)
	}
	return args
}

func enabledFromEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func isBuiltInServiceURI(envName, fallback string) bool {
	v := strings.TrimSpace(os.Getenv(envName))
	if v == "" {
		v = fallback
	}
	return v == fallback || v == fallback+"/"
}

func containsProfile(profiles []string, want string) bool {
	for _, profile := range profiles {
		if profile == want {
			return true
		}
	}
	return false
}

func (m *ComposeManager) ComposeServices(ctx context.Context, repoRoot string) ([]string, error) {
	args := append(m.composeArgs(repoRoot), "config", "--services")
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot})
	if err != nil {
		return nil, fmt.Errorf("docker compose config --services failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	services := parseServiceLines(res.Stdout)
	return services, nil
}

func (m *ComposeManager) ComposeDown(ctx context.Context, repoRoot string, profile string) error {
	_ = profile
	args := append(m.composeArgs(repoRoot), "down", "--remove-orphans")
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot})
	if err != nil {
		return fmt.Errorf("docker compose down failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *ComposeManager) ComposePS(ctx context.Context, repoRoot string) (string, error) {
	args := append(m.composeArgs(repoRoot), "ps", "-a")
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot})
	if err != nil {
		return res.Stdout + res.Stderr, fmt.Errorf("docker compose ps failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

func (m *ComposeManager) ComposeStatus(ctx context.Context, repoRoot string) ([]ComposeServiceStatus, error) {
	args := append(m.composeArgs(repoRoot), "ps", "-a", "--format", "json")
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

func (m *ComposeManager) ComposeUp(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	repoRoot := paths.RepoRoot
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
		_ = cfg.Profile
	}
	if err := m.BuildEnabledServices(ctx, repoRoot, remaining); err != nil {
		return err
	}

	args := append(m.composeArgs(repoRoot), "up", "--no-build", "--detach", "--no-deps")
	args = append(args, remaining...)
	env := localComposeEnv(cfg)
	if streamer, ok := m.runner.(CommandStreamer); ok {
		err := streamer.Stream(ctx, Command{Name: "docker", Args: args, Dir: repoRoot, Env: env}, os.Stdout, os.Stderr)
		if err != nil {
			return fmt.Errorf("docker compose up failed: %w", err)
		}
		return nil
	}
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot, Env: env})
	if err != nil {
		return fmt.Errorf("docker compose up failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

type composeConfigJSON struct {
	Services map[string]composeConfigService `json:"services"`
}

type composeConfigService struct {
	Build *composeBuildConfig `json:"build"`
}

type composeBuildConfig struct{}

func (m *ComposeManager) BuildEnabledServices(ctx context.Context, repoRoot string, services []string) error {
	if len(services) == 0 {
		return nil
	}
	config, err := m.ComposeConfigJSON(ctx, repoRoot)
	if err != nil {
		return err
	}

	seen := map[string]struct{}{}
	buildServices := []string{}
	for _, serviceName := range services {
		service, ok := config.Services[serviceName]
		if !ok || service.Build == nil {
			continue
		}
		if _, ok := seen[serviceName]; ok {
			continue
		}
		seen[serviceName] = struct{}{}
		buildServices = append(buildServices, serviceName)
	}
	if len(buildServices) == 0 {
		return nil
	}

	args := append(m.composeArgs(repoRoot), "build")
	args = append(args, buildServices...)
	cmd := Command{Name: "docker", Args: args, Dir: repoRoot}
	if streamer, ok := m.runner.(CommandStreamer); ok {
		if err := streamer.Stream(ctx, cmd, os.Stdout, os.Stderr); err != nil {
			return fmt.Errorf("docker compose build failed: %w", err)
		}
		return nil
	}
	res, err := m.runner.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("docker compose build failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *ComposeManager) ComposeConfigJSON(ctx context.Context, repoRoot string) (composeConfigJSON, error) {
	args := append(m.composeArgs(repoRoot), "config", "--format", "json")
	res, err := m.runner.Run(ctx, Command{Name: "docker", Args: args, Dir: repoRoot})
	if err != nil {
		return composeConfigJSON{}, fmt.Errorf("docker compose config --format json failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	var config composeConfigJSON
	if err := json.Unmarshal([]byte(res.Stdout), &config); err != nil {
		return composeConfigJSON{}, fmt.Errorf("parse docker compose config json: %w", err)
	}
	return config, nil
}

func localComposeEnv(cfg RuntimeConfig) []string {
	return []string{
		"LAZYMIND_FRONTEND_PORT=" + strconv.Itoa(cfg.FrontendPort),
		"LAZYMIND_LOCAL_PROXY_PORT=" + strconv.Itoa(cfg.LocalProxy.Port),
		"LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT=" + strconv.Itoa(cfg.LocalProxy.AuthHostPort),
		"LAZYMIND_LOCAL_PROXY_CORE_HOST_PORT=" + strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		"LAZYMIND_LOCAL_CORE_PORT=" + strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		"LAZYMIND_LOCAL_PROXY_CHAT_HOST_PORT=" + strconv.Itoa(cfg.LocalProxy.ChatHostPort),
		"LAZYMIND_LOCAL_PROXY_SCAN_HOST_PORT=" + strconv.Itoa(cfg.LocalProxy.ScanHostPort),
		"LAZYMIND_LOCAL_PROXY_EVO_HOST_PORT=" + strconv.Itoa(cfg.LocalProxy.EvoHostPort),
		"LAZYMIND_LOCAL_POSTGRES_PORT=" + strconv.Itoa(cfg.Algorithm.PostgresPort),
		"LAZYMIND_LOCAL_DOC_PORT=" + strconv.Itoa(cfg.Algorithm.DocPort),
		"LAZYMIND_LOCAL_PROCESSOR_PORT=" + strconv.Itoa(cfg.Algorithm.ProcessorPort),
		"LAZYMIND_LOCAL_ALGO_PORT=" + strconv.Itoa(cfg.Algorithm.AlgoPort),
		"LAZYMIND_LOCAL_WORKER_PORT=" + strconv.Itoa(cfg.Algorithm.WorkerPort),
		"LAZYMIND_LOCAL_CHAT_PORT=" + strconv.Itoa(cfg.Algorithm.ChatPort),
		"LAZYMIND_LOCAL_EVO_PORT=" + strconv.Itoa(cfg.Algorithm.EvoPort),
		"LAZYMIND_LOCAL_MILVUS_PORT=" + strconv.Itoa(cfg.Algorithm.MilvusPort),
		"LAZYMIND_LOCAL_OPENSEARCH_PORT=" + strconv.Itoa(cfg.Algorithm.OpenSearchPort),
	}
}

func filterRemainingServices(allServices []string, disabled []string) ([]string, error) {
	disabledSet, err := validateKnownServices(allServices, disabled, "disabled service")
	if err != nil {
		return nil, err
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

func validateKnownServices(allServices []string, services []string, label string) (map[string]struct{}, error) {
	available := make(map[string]struct{}, len(allServices))
	for _, svc := range allServices {
		available[svc] = struct{}{}
	}
	serviceSet := map[string]struct{}{}
	for _, d := range services {
		if d == "" {
			continue
		}
		if _, ok := available[d]; !ok {
			return nil, fmt.Errorf("unknown %s: %s", label, d)
		}
		serviceSet[d] = struct{}{}
	}
	return serviceSet, nil
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
