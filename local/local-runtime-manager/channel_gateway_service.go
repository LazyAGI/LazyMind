package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const channelGatewayHealthTimeout = 180 * time.Second

type ChannelGatewayManager struct {
	runner CommandRunner
}

func NewChannelGatewayManager(r CommandRunner) *ChannelGatewayManager {
	return &ChannelGatewayManager{runner: r}
}

func (m *ChannelGatewayManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := m.preparePythonEnv(ctx, cfg, paths); err != nil {
		return err
	}

	python := channelGatewayPythonPath(paths)
	cmd := exec.CommandContext(
		ctx,
		python,
		"-m",
		"uvicorn",
		"main:app",
		"--host",
		"127.0.0.1",
		"--port",
		strconv.Itoa(cfg.ChannelGateway.Port),
	)
	cmd.Dir = filepath.Join(paths.RepoRoot, channelGatewaySourceDirName)
	cmd.Env = channelGatewayProcessEnv(cfg, paths)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	configureChildProcess(cmd, false)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start channel-gateway failed: %w", err)
	}
	releaseJob, err := attachManagedProcess(paths, channelGatewayProcessName, cmd.Process)
	if err != nil {
		_ = forceKillProcessTree(cmd.Process.Pid)
		return fmt.Errorf("attach channel-gateway process containment failed: %w", err)
	}
	defer releaseJob()
	if err := os.WriteFile(paths.ChannelGatewayPIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	registerLocalProcess(
		paths,
		channelGatewayProcessName,
		cmd.Process.Pid,
		[]int{cfg.ChannelGateway.Port},
		append([]string{python}, cmd.Args...),
	)

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()
	if err := waitForHTTPHealth(
		ctx,
		cfg.ChannelGateway.Port,
		"/readyz",
		channelGatewayProcessName,
		channelGatewayHealthTimeout,
		waitErr,
	); err != nil {
		_ = cmd.Process.Kill()
		_ = os.Remove(paths.ChannelGatewayPIDFile)
		unregisterLocalProcess(paths, channelGatewayProcessName, cmd.Process.Pid)
		return err
	}

	err = <-waitErr
	_ = os.Remove(paths.ChannelGatewayPIDFile)
	unregisterLocalProcess(paths, channelGatewayProcessName, cmd.Process.Pid)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("channel-gateway exited: %w", err)
	}
	return nil
}

func (m *ChannelGatewayManager) Down(ctx context.Context, paths RuntimePaths) error {
	return stopPIDFileProcess(ctx, paths, channelGatewayProcessName, paths.ChannelGatewayPIDFile)
}

func (m *ChannelGatewayManager) preparePythonEnv(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	python := channelGatewayPythonPath(paths)
	if cfg.Profile == "desktop" {
		if info, err := os.Stat(python); err == nil && !info.IsDir() {
			return nil
		}
		return fmt.Errorf("desktop channel-gateway Python not found: %s", python)
	}
	if _, err := os.Stat(python); err != nil {
		basePython, runtimeErr := ensureLocalPythonRuntime(ctx, m.runner, paths, cfg.ChannelGateway.PythonVersion)
		if runtimeErr != nil {
			return runtimeErr
		}
		uv, ok := uvCommand()
		if !ok {
			return fmt.Errorf("uv is required to create channel-gateway venv")
		}
		res, runErr := m.runner.Run(ctx, Command{
			Name: uv,
			Args: localPythonVenvArgs(basePython, false, paths.ChannelGatewayVenvDir),
			Dir:  paths.RepoRoot,
			Env:  pythonRuntimeEnv(paths),
		})
		if runErr != nil {
			return fmt.Errorf("create channel-gateway venv failed: %w (%s)", runErr, strings.TrimSpace(res.Stderr))
		}
	}
	if !cfg.ChannelGateway.InstallDeps {
		return nil
	}

	requirements := filepath.Join(paths.RepoRoot, channelGatewaySourceDirName, "requirements.txt")
	raw, err := os.ReadFile(requirements)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	marker := filepath.Join(paths.ChannelGatewayVenvDir, ".lazymind-requirements.sha256")
	if current, readErr := os.ReadFile(marker); readErr == nil && strings.TrimSpace(string(current)) == hash {
		return nil
	}
	uv, ok := uvCommand()
	if !ok {
		return fmt.Errorf("uv is required to install channel-gateway dependencies")
	}
	res, runErr := m.runner.Run(ctx, Command{
		Name: uv,
		Args: localPythonPipInstallArgs(python, "-r", requirements),
		Dir:  paths.RepoRoot,
		Env:  pythonRuntimeEnv(paths),
	})
	if runErr != nil {
		return fmt.Errorf("install channel-gateway requirements failed: %w (%s)", runErr, strings.TrimSpace(res.Stderr))
	}
	return os.WriteFile(marker, []byte(hash+"\n"), 0o644)
}

func channelGatewayPythonPath(paths RuntimePaths) string {
	return venvExecutable(paths.ChannelGatewayVenvDir, "python")
}

func channelGatewayEnv(cfg RuntimeConfig, paths RuntimePaths) []string {
	return []string{
		"LAZYMIND_CHANNEL_GATEWAY_DATABASE_DSN=" + sqliteURL(paths.ChannelGatewayDBPath),
		"LAZYMIND_CHANNEL_GATEWAY_CREDENTIAL_KEY_PATH=" + paths.ChannelGatewayKeyPath,
		"LAZYMIND_CHANNEL_GATEWAY_CORE_BASE_URL=http://127.0.0.1:" + strconv.Itoa(cfg.LocalProxy.CoreHostPort),
	}
}

func channelGatewayProcessEnv(cfg RuntimeConfig, paths RuntimePaths) []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(key) {
		case "PYTHONHOME", "PYTHONPATH", "VIRTUAL_ENV":
			continue
		}
		env = append(env, item)
	}
	return append(env, channelGatewayEnv(cfg, paths)...)
}
