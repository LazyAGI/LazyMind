package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ProcessComposeManager struct {
	runner   CommandRunner
	execPath string
}

func NewProcessComposeManager(r CommandRunner, execPath string) *ProcessComposeManager {
	return &ProcessComposeManager{runner: r, execPath: execPath}
}

type processComposeConfig struct {
	Version         string                           `yaml:"version"`
	IsStrict        bool                             `yaml:"is_strict"`
	OrderedShutdown bool                             `yaml:"ordered_shutdown"`
	Processes       map[string]processComposeProcess `yaml:"processes"`
}

type processComposeProcess struct {
	WorkingDir  string                 `yaml:"working_dir"`
	Command     string                 `yaml:"command"`
	Shutdown    processComposeShutdown `yaml:"shutdown"`
	LogLocation string                 `yaml:"log_location"`
	Namespace   string                 `yaml:"namespace"`
}

type processComposeShutdown struct {
	Command        string `yaml:"command"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

func (m *ProcessComposeManager) WriteGeneratedConfig(w io.Writer, repoRoot string, profile string, logPath string, localProxyLogPath string, authServiceLogPath string, tokenPath string, apiPort int) error {
	commandForComposeUp := quoteShellArg(m.execPath) + " internal compose-up --profile " + profile
	commandForComposeDown := quoteShellArg(m.execPath) + " internal compose-down --profile " + profile
	commandForLocalProxyRun := quoteShellArg(m.execPath) + " internal local-proxy-run --profile " + profile
	commandForLocalProxyDown := quoteShellArg(m.execPath) + " internal local-proxy-down --profile " + profile
	commandForAuthServiceRun := quoteShellArg(m.execPath) + " internal auth-service-run --profile " + profile
	commandForAuthServiceDown := quoteShellArg(m.execPath) + " internal auth-service-down --profile " + profile

	cfg := processComposeConfig{
		Version:         "0.5",
		IsStrict:        true,
		OrderedShutdown: true,
		Processes: map[string]processComposeProcess{
			processComposeServiceName: {
				WorkingDir: repoRoot,
				Command:    commandForComposeUp,
				Shutdown: processComposeShutdown{
					Command:        commandForComposeDown,
					TimeoutSeconds: 60,
				},
				LogLocation: logPath,
				Namespace:   "container",
			},
			localProxyProcessName: {
				WorkingDir: repoRoot,
				Command:    commandForLocalProxyRun,
				Shutdown: processComposeShutdown{
					Command:        commandForLocalProxyDown,
					TimeoutSeconds: 15,
				},
				LogLocation: localProxyLogPath,
				Namespace:   "host",
			},
			authServiceProcessName: {
				WorkingDir: repoRoot,
				Command:    commandForAuthServiceRun,
				Shutdown: processComposeShutdown{
					Command:        commandForAuthServiceDown,
					TimeoutSeconds: 15,
				},
				LogLocation: authServiceLogPath,
				Namespace:   "host",
			},
		},
	}
	_ = tokenPath
	_ = apiPort
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

func (m *ProcessComposeManager) Up(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	args := []string{
		"--config", filepath.ToSlash(paths.GeneratedConfig),
		"-D",
		"-t=false",
		"-p", strconv.Itoa(cfg.ProcessComposePort),
		"--token-file", paths.RunDirTokenFile,
		"--ordered-shutdown",
		"up",
	}
	res, err := m.runner.Run(ctx, Command{Name: processComposeCommand(paths.RepoRoot), Args: args, Dir: paths.RepoRoot})
	if err != nil {
		return fmt.Errorf("process-compose up failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *ProcessComposeManager) FollowLogs(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, stdout io.Writer, stderr io.Writer) error {
	streamer, ok := m.runner.(CommandStreamer)
	if !ok {
		return nil
	}
	args := []string{
		"-p", strconv.Itoa(cfg.ProcessComposePort),
		"--token-file", paths.RunDirTokenFile,
		"process",
		"logs",
		processComposeServiceName,
		"--follow",
		"--tail",
		"0",
	}
	err := streamer.Stream(ctx, Command{Name: processComposeCommand(paths.RepoRoot), Args: args, Dir: paths.RepoRoot}, stdout, stderr)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("process-compose logs failed: %w", err)
	}
	return nil
}

func (m *ProcessComposeManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	args := []string{"--config", filepath.ToSlash(paths.GeneratedConfig)}
	args = append(args,
		"-p", strconv.Itoa(cfg.ProcessComposePort),
		"--token-file", paths.RunDirTokenFile,
		"down",
	)
	res, err := m.runner.Run(ctx, Command{Name: processComposeCommand(paths.RepoRoot), Args: args, Dir: paths.RepoRoot})
	if err != nil {
		return fmt.Errorf("process-compose down failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *ProcessComposeManager) ProbeAPI(port int, timeout time.Duration) bool {
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/api/v1/processes"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	_ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req = req.WithContext(_ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}

func quoteShellArg(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n'
	}) == -1 {
		return value
	}
	return strconv.Quote(value)
}

func processComposeCommand(repoRoot string) string {
	candidate := filepath.Join(repoRoot, localProcessComposeBin)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return "process-compose"
}
