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

const processComposePackage = "github.com/f1bonacc1/process-compose@v1.116.0"

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
	commandForScanControlPlaneRun := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal scan-control-plane-run --profile "+profile)
	commandForScanControlPlaneDown := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal scan-control-plane-down --profile "+profile)
	commandForFileWatcherRun := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal file-watcher-run --profile "+profile)
	commandForFileWatcherDown := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal file-watcher-down --profile "+profile)
	commandForFrontendRun := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal frontend-run --profile "+profile)
	commandForFrontendDown := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal frontend-down --profile "+profile)
	commandForMilvusLiteRun := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal milvus-lite-run --profile "+profile)
	commandForMilvusLiteDown := commandWithEnv(commandEnv, quoteShellArg(m.execPath)+" internal milvus-lite-down --profile "+profile)

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
			scanControlPlaneProcessName: {
				WorkingDir: repoRoot,
				Command:    commandForScanControlPlaneRun,
				Shutdown: processComposeShutdown{
					Command:        commandForScanControlPlaneDown,
					TimeoutSeconds: 15,
				},
				LogLocation: paths.ScanControlPlaneLog,
				Namespace:   "host",
			},
			fileWatcherProcessName: {
				WorkingDir: repoRoot,
				Command:    commandForFileWatcherRun,
				Shutdown: processComposeShutdown{
					Command:        commandForFileWatcherDown,
					TimeoutSeconds: 15,
				},
				LogLocation: paths.FileWatcherLog,
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
	if cfg.ModeProfile.VectorStore.ManagedProcess {
		pcCfg.Processes[milvusLiteProcessName] = processComposeProcess{
			WorkingDir: repoRoot,
			Command:    commandForMilvusLiteRun,
			Shutdown: processComposeShutdown{
				Command:        commandForMilvusLiteDown,
				TimeoutSeconds: 20,
			},
			LogLocation: paths.MilvusLiteLog,
			Namespace:   "host",
		}
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
		localAuthPortEnvVar+"="+strconv.Itoa(cfg.AuthService.Port),
		authServicePortEnvVar+"="+strconv.Itoa(cfg.AuthService.Port),
		localFileWatcherPortEnvVar+"="+strconv.Itoa(cfg.FileWatcher.Port),
		localMilvusLiteDBPathEnvVar+"="+cfg.ModeProfile.VectorStore.DBPath,
	)
	return env
}

func (m *ProcessComposeManager) Up(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := m.EnsureBinary(ctx, paths.RepoRoot); err != nil {
		return err
	}
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
	if err := m.EnsureBinary(ctx, paths.RepoRoot); err != nil {
		return err
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
	if err := m.EnsureBinary(ctx, paths.RepoRoot); err != nil {
		return err
	}
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

func (m *ProcessComposeManager) EnsureBinary(ctx context.Context, repoRoot string) error {
	if _, ok := m.runner.(*ExecRunner); !ok {
		return nil
	}
	candidate := filepath.Join(repoRoot, localProcessComposeBin)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(candidate), 0o755); err != nil {
		return err
	}
	gobin, err := processComposeGOBIN(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve process-compose GOBIN: %w", err)
	}
	res, err := m.runner.Run(ctx, Command{
		Name: "go",
		Args: []string{"install", processComposePackage},
		Dir:  repoRoot,
		Env:  []string{"GOBIN=" + gobin},
	})
	if err != nil {
		return fmt.Errorf("install process-compose failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func processComposeGOBIN(repoRoot string) (string, error) {
	return filepath.Abs(filepath.Join(repoRoot, "local", "bin"))
}

func quoteShellArg(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z') &&
			!(r >= 'a' && r <= 'z') &&
			!(r >= '0' && r <= '9') &&
			r != '_' && r != '-' && r != '.' && r != '/'
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func processComposeCommand(repoRoot string) string {
	candidate := filepath.Join(repoRoot, localProcessComposeBin)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return "process-compose"
}
