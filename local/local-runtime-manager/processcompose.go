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

func (m *ProcessComposeManager) WriteGeneratedConfig(w io.Writer, repoRoot string, profile string, paths RuntimePaths, cfg RuntimeConfig, tokenPath string, apiPort int) error {
	commandEnv := runtimeCommandEnv(cfg)
	commandForComposeUp := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal compose-up --profile "+profile)
	commandForComposeDown := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal compose-down --profile "+profile)
	commandForLocalProxyRun := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal local-proxy-run --profile "+profile)
	commandForLocalProxyDown := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal local-proxy-down --profile "+profile)
	commandForAuthServiceRun := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal auth-service-run --profile "+profile)
	commandForAuthServiceDown := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal auth-service-down --profile "+profile)
	commandForCoreRun := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal core-run --profile "+profile)
	commandForCoreDown := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal core-down --profile "+profile)
	commandForFrontendRun := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal frontend-run --profile "+profile)
	commandForFrontendDown := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal frontend-down --profile "+profile)

	pcCfg := processComposeConfig{
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
				LogLocation: paths.LogFilePath,
				Namespace:   "container",
			},
			localProxyProcessName: {
				WorkingDir: repoRoot,
				Command:    commandForLocalProxyRun,
				Shutdown: processComposeShutdown{
					Command:        commandForLocalProxyDown,
					TimeoutSeconds: 15,
				},
				LogLocation: paths.LocalProxyLog,
				Namespace:   "host",
			},
			authServiceProcessName: {
				WorkingDir: repoRoot,
				Command:    commandForAuthServiceRun,
				Shutdown: processComposeShutdown{
					Command:        commandForAuthServiceDown,
					TimeoutSeconds: 15,
				},
				LogLocation: paths.AuthServiceLog,
				Namespace:   "host",
			},
			coreProcessName: {
				WorkingDir: repoRoot,
				Command:    commandForCoreRun,
				Shutdown: processComposeShutdown{
					Command:        commandForCoreDown,
					TimeoutSeconds: 15,
				},
				LogLocation: paths.CoreLog,
				Namespace:   "host",
			},
			frontendProcessName: {
				WorkingDir: repoRoot,
				Command:    commandForFrontendRun,
				Shutdown: processComposeShutdown{
					Command:        commandForFrontendDown,
					TimeoutSeconds: 15,
				},
				LogLocation: paths.FrontendLog,
				Namespace:   "host",
			},
		},
	}
	for _, svc := range algorithmProcessSpecs(cfg.Algorithm) {
		run := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal algorithm-run --service "+svc.Name+" --profile "+profile)
		down := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal algorithm-down --service "+svc.Name+" --profile "+profile)
		pcCfg.Processes[svc.Name] = processComposeProcess{
			WorkingDir: repoRoot,
			Command:    run,
			Shutdown: processComposeShutdown{
				Command:        down,
				TimeoutSeconds: 20,
			},
			LogLocation: algorithmLogPath(paths, svc.Name),
			Namespace:   "host",
		}
	}
	_ = tokenPath
	_ = apiPort
	out, err := yaml.Marshal(pcCfg)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

func commandWithEnv(env []string, command string) string {
	if len(env) == 0 {
		return command
	}
	parts := make([]string, 0, len(env)+2)
	parts = append(parts, "env")
	for _, item := range env {
		parts = append(parts, quoteShellArg(item))
	}
	parts = append(parts, command)
	return strings.Join(parts, " ")
}

func runtimeCommandEnv(cfg RuntimeConfig) []string {
	env := append([]string{}, localComposeEnv(cfg)...)
	env = append(env,
		localPortsPinnedEnvVar+"=1",
		processComposePortEnvVar+"="+strconv.Itoa(cfg.ProcessComposePort),
		authServicePortEnvVar+"="+strconv.Itoa(cfg.AuthService.Port),
	)
	return env
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
	args := []string{
		"-p", strconv.Itoa(cfg.ProcessComposePort),
		"--token-file", paths.RunDirTokenFile,
		"down",
	}
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
