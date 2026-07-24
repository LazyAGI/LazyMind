package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	Shell           *processComposeShell             `yaml:"shell,omitempty"`
	Processes       map[string]processComposeProcess `yaml:"processes"`
}

type processComposeShell struct {
	Command  string `yaml:"shell_command"`
	Argument string `yaml:"shell_argument"`
}

type processComposeProcess struct {
	WorkingDir  string                 `yaml:"working_dir"`
	Command     string                 `yaml:"command"`
	Shutdown    processComposeShutdown `yaml:"shutdown"`
	LogLocation string                 `yaml:"log_location"`
	Namespace   string                 `yaml:"namespace"`
	Environment []string               `yaml:"environment,omitempty"`
}

type processComposeShutdown struct {
	Command        string `yaml:"command"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

func (m *ProcessComposeManager) WriteGeneratedConfig(w io.Writer, repoRoot string, paths RuntimePaths, cfg RuntimeConfig, tokenPath string, apiPort int) error {
	commandEnv := runtimeCommandEnv(paths, cfg)
	plan := buildRuntimeProcessPlan(cfg)
	windowsDesktopShell := runtime.GOOS == "windows" && cfg.Profile == "desktop"
	commandPrefix := quoteShellArg(m.execPath) + " "
	if windowsDesktopShell {
		// The custom shell already runs this executable. Omitting the absolute
		// path also avoids nested Windows quoting when the ZIP is extracted into
		// a directory containing spaces.
		commandPrefix = ""
	}
	commandForLocalProxyRun := commandWithEnv(commandEnv, commandPrefix+"internal local-proxy-run")
	commandForLocalProxyDown := commandWithEnv(commandEnv, commandPrefix+"internal local-proxy-down")
	commandForAuthServiceRun := commandWithEnv(commandEnv, commandPrefix+"internal auth-service-run")
	commandForAuthServiceDown := commandWithEnv(commandEnv, commandPrefix+"internal auth-service-down")
	commandForCoreRun := commandWithEnv(commandEnv, commandPrefix+"internal core-run")
	commandForCoreDown := commandWithEnv(commandEnv, commandPrefix+"internal core-down")
	commandForScanControlPlaneRun := commandWithEnv(commandEnv, commandPrefix+"internal scan-control-plane-run")
	commandForScanControlPlaneDown := commandWithEnv(commandEnv, commandPrefix+"internal scan-control-plane-down")
	commandForFileWatcherRun := commandWithEnv(commandEnv, commandPrefix+"internal file-watcher-run")
	commandForFileWatcherDown := commandWithEnv(commandEnv, commandPrefix+"internal file-watcher-down")
	commandForFrontendRun := commandWithEnv(commandEnv, commandPrefix+"internal frontend-run")
	commandForFrontendDown := commandWithEnv(commandEnv, commandPrefix+"internal frontend-down")
	commandForMilvusLiteRun := commandWithEnv(commandEnv, commandPrefix+"internal milvus-lite-run")
	commandForMilvusLiteDown := commandWithEnv(commandEnv, commandPrefix+"internal milvus-lite-down")

	pcCfg := processComposeConfig{
		Version:         "0.5",
		IsStrict:        true,
		OrderedShutdown: true,
		Processes: map[string]processComposeProcess{
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
	if !plan.includes(scanControlPlaneProcessName) {
		delete(pcCfg.Processes, scanControlPlaneProcessName)
	}
	if !plan.includes(fileWatcherProcessName) {
		delete(pcCfg.Processes, fileWatcherProcessName)
	}
	if windowsDesktopShell {
		// process-compose normally starts every command through cmd.exe. A GUI
		// application has no inherited console, so those shells each allocate a
		// visible terminal window. Route commands through our GUI-subsystem
		// sidecar, which directly launches a restricted internal subcommand with
		// CREATE_NO_WINDOW instead.
		pcCfg.Shell = &processComposeShell{Command: m.execPath, Argument: "shell"}
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
	for _, svc := range plan.AlgorithmServices {
		run := commandWithEnv(commandEnv, commandPrefix+"internal algorithm-run --service "+svc.Name)
		down := commandWithEnv(commandEnv, commandPrefix+"internal algorithm-down --service "+svc.Name)
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
	for name, process := range pcCfg.Processes {
		process.Environment = runtimeProcessEnvironment(commandEnv, cfg, plan, name)
		pcCfg.Processes[name] = process
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
	_ = env
	return command
}

func runtimeCommandEnv(paths RuntimePaths, cfg RuntimeConfig) []string {
	routerPoolStart, routerPoolEnd := localRouterPortPool(cfg)
	env := append([]string{}, localRuntimeEnv(cfg)...)
	env = append(env, serviceRuntimeEnv(paths)...)
	env = append(env,
		runtimeProfileEnvVar+"="+cfg.Profile,
		runtimeRootEnvVar+"="+cfg.RuntimeRoot,
		localBuildRootEnvVar+"="+cfg.BuildRoot,
		runtimeResourcesRootEnvVar+"="+cfg.ResourcesRoot,
		localPortsPinnedEnvVar+"=1",
		processComposePortEnvVar+"="+strconv.Itoa(cfg.ProcessComposePort),
		localAuthPortEnvVar+"="+strconv.Itoa(cfg.AuthService.Port),
		authServicePortEnvVar+"="+strconv.Itoa(cfg.AuthService.Port),
		localCorePortEnvVar+"="+strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		localProxyCoreHostPortEnvVar+"="+strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		localProxyChatHostPortEnvVar+"="+strconv.Itoa(cfg.LocalProxy.ChatHostPort),
		localProxyScanHostPortEnvVar+"="+strconv.Itoa(cfg.LocalProxy.ScanHostPort),
		localProxyEvoHostPortEnvVar+"="+strconv.Itoa(cfg.LocalProxy.EvoHostPort),
		localFileWatcherPortEnvVar+"="+strconv.Itoa(cfg.FileWatcher.Port),
		localPostgresPortEnvVar+"="+strconv.Itoa(cfg.Algorithm.PostgresPort),
		localDocPortEnvVar+"="+strconv.Itoa(cfg.Algorithm.DocPort),
		localProcessorPortEnvVar+"="+strconv.Itoa(cfg.Algorithm.ProcessorPort),
		localAlgoPortEnvVar+"="+strconv.Itoa(cfg.Algorithm.AlgoPort),
		localWorkerPortEnvVar+"="+strconv.Itoa(cfg.Algorithm.WorkerPort),
		localChatPortEnvVar+"="+strconv.Itoa(cfg.Algorithm.ChatPort),
		localEvoPortEnvVar+"="+strconv.Itoa(cfg.Algorithm.EvoPort),
		localMilvusPortEnvVar+"="+strconv.Itoa(cfg.ModeProfile.VectorStore.Port),
		localMilvusLiteDataDirEnvVar+"="+cfg.ModeProfile.VectorStore.DBPath,
		localOpenSearchPortEnvVar+"="+strconv.Itoa(cfg.Algorithm.OpenSearchPort),
		routerPortPoolStartEnvVar+"="+strconv.Itoa(routerPoolStart),
		routerPortPoolEndEnvVar+"="+strconv.Itoa(routerPoolEnd),
		routerPortsPerInstanceEnvVar+"="+strconv.Itoa(defaultRouterPortsPerInstance),
	)
	return env
}

func runtimeProcessEnvironment(base []string, cfg RuntimeConfig, plan runtimeProcessPlan, processName string) []string {
	env := append([]string(nil), base...)
	if cfg.MaintenanceMode != installerWarmupMaintenanceMode {
		return env
	}
	overrides := []string{}
	if processName == authServiceProcessName || plan.isAlgorithmProcess(processName) {
		overrides = append(overrides,
			"HF_HUB_OFFLINE=1",
			"TRANSFORMERS_OFFLINE=1",
			"PIP_NO_INDEX=1",
			"PYTHONDONTWRITEBYTECODE=0",
		)
	}
	switch processName {
	case authServiceProcessName:
		overrides = append(overrides, "LAZYMIND_CLOUD_AUTH_HEALTH_CHECK_ENABLED=false")
	case coreProcessName:
		overrides = append(overrides, "LAZYMIND_BACKGROUND_JOBS_ENABLED=false")
	case chatProcessName:
		overrides = append(overrides,
			"LAZYMIND_BACKGROUND_JOBS_ENABLED=false",
			"LAZYMIND_ROUTER_CHILD_PROCESSES_ENABLED=false",
		)
	}
	return withEnvOverrides(env, overrides...)
}

func withEnvOverrides(env []string, overrides ...string) []string {
	for _, override := range overrides {
		key, _, ok := strings.Cut(override, "=")
		if !ok {
			continue
		}
		prefix := key + "="
		replaced := false
		for i := range env {
			if strings.HasPrefix(env[i], prefix) {
				env[i] = override
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, override)
		}
	}
	return env
}

func (m *ProcessComposeManager) Up(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := m.EnsureBinary(ctx, paths); err != nil {
		return err
	}
	args := []string{
		"--config", filepath.ToSlash(paths.GeneratedConfig),
		"-t=false",
		"-p", strconv.Itoa(cfg.ProcessComposePort),
		"--token-file", paths.RunDirTokenFile,
		"--ordered-shutdown",
		"up",
	}
	if _, ok := m.runner.(*ExecRunner); ok {
		logFile, err := os.OpenFile(paths.LogFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		cmd := exec.Command(processComposeCommand(paths), args...)
		cmd.Dir = paths.RepoRoot
		cmd.Env = append(os.Environ(), processComposeRuntimeEnv(paths)...)
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		configureChildProcess(cmd, true)
		if err := cmd.Start(); err != nil {
			_ = logFile.Close()
			return fmt.Errorf("process-compose up failed: %w", err)
		}
		if err := os.WriteFile(paths.ProcessComposePIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
			_ = killAlgorithmProcess(cmd.Process)
			_ = logFile.Close()
			return err
		}
		registerLocalProcess(paths, processComposeServiceName, cmd.Process.Pid, []int{cfg.ProcessComposePort}, append([]string{processComposeCommand(paths)}, args...))
		go func() {
			_ = cmd.Wait()
			_ = logFile.Close()
			_ = os.Remove(paths.ProcessComposePIDFile)
			unregisterLocalProcess(paths, processComposeServiceName, cmd.Process.Pid)
		}()
		return nil
	}
	res, err := m.runner.Run(ctx, Command{Name: processComposeCommand(paths), Args: args, Dir: paths.RepoRoot, Env: processComposeRuntimeEnv(paths)})
	if err != nil {
		return fmt.Errorf("process-compose up failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *ProcessComposeManager) FollowLogs(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, stdout io.Writer, stderr io.Writer) error {
	return nil
}

func (m *ProcessComposeManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths, stdout io.Writer, stderr io.Writer) error {
	if err := m.EnsureBinary(ctx, paths); err != nil {
		return err
	}
	args := []string{
		"-p", strconv.Itoa(cfg.ProcessComposePort),
		"--token-file", paths.RunDirTokenFile,
		"down",
	}
	if streamer, ok := m.runner.(CommandStreamer); ok {
		if err := streamer.Stream(ctx, Command{Name: processComposeCommand(paths), Args: args, Dir: paths.RepoRoot, Env: processComposeRuntimeEnv(paths)}, stdout, stderr); err != nil {
			return fmt.Errorf("process-compose down failed: %w", err)
		}
		return nil
	}
	res, err := m.runner.Run(ctx, Command{Name: processComposeCommand(paths), Args: args, Dir: paths.RepoRoot, Env: processComposeRuntimeEnv(paths)})
	if res.Stdout != "" && stdout != nil {
		_, _ = io.WriteString(stdout, res.Stdout)
	}
	if res.Stderr != "" && stderr != nil {
		_, _ = io.WriteString(stderr, res.Stderr)
	}
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

func (m *ProcessComposeManager) EnsureBinary(ctx context.Context, paths RuntimePaths) error {
	if _, ok := m.runner.(*ExecRunner); !ok {
		return nil
	}
	if paths.ResourcesRoot != "" && filepath.Clean(paths.ResourcesRoot) != filepath.Clean(paths.RepoRoot) && !pathIsUnderRoot(paths.ProcessComposeBin, paths.ResourcesRoot) {
		return fmt.Errorf("process-compose binary not found in runtime resources: %s", paths.ProcessComposeBin)
	}
	if info, err := os.Stat(paths.ProcessComposeBin); err == nil && !info.IsDir() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(paths.ProcessComposeBin), 0o755); err != nil {
		return err
	}
	gobin, err := processComposeGOBIN(paths)
	if err != nil {
		return fmt.Errorf("resolve process-compose GOBIN: %w", err)
	}
	res, err := m.runner.Run(ctx, Command{
		Name: "go",
		Args: []string{"install", processComposePackage},
		Dir:  paths.RepoRoot,
		Env:  append(goToolEnv(paths), "GOBIN="+gobin),
	})
	if err != nil {
		return fmt.Errorf("install process-compose failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func processComposeGOBIN(paths RuntimePaths) (string, error) {
	return filepath.Abs(filepath.Dir(paths.ProcessComposeBin))
}

func quoteShellArg(value string) string {
	if runtime.GOOS == "windows" {
		if value == "" {
			return `""`
		}
		escaped := strings.ReplaceAll(value, "%", "%%")
		if strings.IndexAny(escaped, " \t&|<>^()!\"") == -1 {
			return escaped
		}
		return `"` + strings.ReplaceAll(escaped, `"`, `\"`) + `"`
	}
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

func processComposeCommand(paths RuntimePaths) string {
	if info, err := os.Stat(paths.ProcessComposeBin); err == nil && !info.IsDir() {
		return paths.ProcessComposeBin
	}
	candidate := filepath.Join(paths.RepoRoot, localProcessComposeBin)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return "process-compose"
}
