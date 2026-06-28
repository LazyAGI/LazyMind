package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type LocalProxyManager struct {
	runner CommandRunner
}

func NewLocalProxyManager(r CommandRunner) *LocalProxyManager {
	return &LocalProxyManager{runner: r}
}

func (m *LocalProxyManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.LocalProxyBin), 0o755); err != nil {
		return err
	}

	goBin := strings.TrimSpace(os.Getenv("GO"))
	if goBin == "" {
		goBin = "go"
	}
	build := Command{
		Name: goBin,
		Args: []string{"build", "-buildvcs=false", "-o", paths.LocalProxyBin, "./cmd/local-proxy"},
		Dir:  filepath.Join(paths.RepoRoot, localProxySourceDirName),
	}
	if res, err := m.runner.Run(ctx, build); err != nil {
		return fmt.Errorf("build local-proxy failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}

	run := Command{
		Name: paths.LocalProxyBin,
		Args: []string{"--config", paths.LocalProxyConfig},
		Dir:  paths.RepoRoot,
		Env:  localProxyEnv(cfg, paths),
	}
	if res, err := m.runner.Run(ctx, run); err != nil {
		return fmt.Errorf("local-proxy exited: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *LocalProxyManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	stop := Command{
		Name: paths.LocalProxyStopScript,
		Dir:  paths.RepoRoot,
		Env:  localProxyEnv(cfg, paths),
	}
	if res, err := m.runner.Run(ctx, stop); err != nil {
		return fmt.Errorf("stop local-proxy failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func localProxyEnv(cfg RuntimeConfig, paths RuntimePaths) []string {
	env := append([]string{}, localRuntimeEnv(cfg)...)
	env = append(env,
		"LAZYMIND_LOCAL_PROXY_BASE_ROOT="+filepath.Join(paths.RuntimeRoot, "local-proxy"),
		"LAZYMIND_LOCAL_PROXY_BIN="+paths.LocalProxyBin,
		"LAZYMIND_LOCAL_PROXY_CONFIG="+paths.LocalProxyConfig,
		"LAZYMIND_LOCAL_PROXY_LOG_FILE="+paths.LocalProxyLog,
	)
	return env
}

func localRuntimeEnv(cfg RuntimeConfig) []string {
	return []string{
		processComposePortEnvVar + "=" + strconv.Itoa(cfg.ProcessComposePort),
		frontendPortEnvVar + "=" + strconv.Itoa(cfg.FrontendPort),
		localProxyAddressEnvVar + "=" + cfg.LocalProxy.Address,
		localProxyPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.Port),
		localProxyAuthHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.AuthHostPort),
		localProxyCoreHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		localProxyChatHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.ChatHostPort),
		localProxyScanHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.ScanHostPort),
		localProxyEvoHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.EvoHostPort),
	}
}
