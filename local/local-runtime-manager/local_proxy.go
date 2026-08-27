package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	registerLocalProcess(paths, localProxyProcessName, os.Getpid(), []int{cfg.LocalProxy.Port}, []string{paths.LocalProxyBin})
	defer unregisterLocalProcess(paths, localProxyProcessName, os.Getpid())
	if err := os.MkdirAll(filepath.Dir(paths.LocalProxyBin), 0o755); err != nil {
		return err
	}

	if cfg.Profile == "desktop" {
		if info, err := os.Stat(paths.LocalProxyBin); err != nil || info.IsDir() {
			return fmt.Errorf("desktop local-proxy binary not found: %s", paths.LocalProxyBin)
		}
	} else {
		goBin := strings.TrimSpace(os.Getenv("GO"))
		if goBin == "" {
			goBin = "go"
		}
		build := Command{
			Name: goBin,
			Args: []string{"build", "-buildvcs=false", "-o", paths.LocalProxyBin, "./cmd/local-proxy"},
			Dir:  filepath.Join(paths.RepoRoot, localProxySourceDirName),
			Env:  goToolEnv(paths),
		}
		if res, err := m.runner.Run(ctx, build); err != nil {
			return fmt.Errorf("build local-proxy failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
		}
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
	if runtime.GOOS == "windows" {
		records, err := scanLocalRuntimeProcesses(paths)
		if err != nil {
			return fmt.Errorf("scan local-proxy processes: %w", err)
		}
		proxyRecords := make([]LocalProcessRecord, 0, len(records))
		for _, record := range records {
			if record.Service == localProxyProcessName && record.PID != os.Getpid() {
				proxyRecords = append(proxyRecords, record)
			}
		}
		if err := stopLocalProcessRecords(ctx, proxyRecords); err != nil {
			return fmt.Errorf("stop local-proxy failed: %w", err)
		}
		cleanupLocalProcessRecords(paths, proxyRecords)
		return nil
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
		runtimeProfileEnvVar + "=" + cfg.Profile,
		runtimeOwnerTokenEnvVar + "=" + cfg.OwnerToken,
		runtimeRootEnvVar + "=" + cfg.RuntimeRoot,
		localBuildRootEnvVar + "=" + cfg.BuildRoot,
		runtimeResourcesRootEnvVar + "=" + cfg.ResourcesRoot,
		processComposePortEnvVar + "=" + strconv.Itoa(cfg.ProcessComposePort),
		frontendPortEnvVar + "=" + strconv.Itoa(cfg.FrontendPort),
		frontendLANOriginEnvVar + "=" + frontendLANOrigin(cfg),
		localNetworkProfileEnvVar + "=" + cfg.NetworkProfile,
		localAutoLoginAllowLANEnvVar + "=" + envText(localAutoLoginAllowLANEnvVar, "false"),
		localProxyAddressEnvVar + "=" + cfg.LocalProxy.Address,
		localProxyPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.Port),
		localAuthPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.AuthHostPort),
		localProxyAuthHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.AuthHostPort),
		localProxyCoreHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		localProxyChatHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.ChatHostPort),
		localProxyScanHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.ScanHostPort),
		localProxyChannelHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.ChannelHostPort),
		localProxyEvoHostPortEnvVar + "=" + strconv.Itoa(cfg.LocalProxy.EvoHostPort),
	}
}

func frontendLANOrigin(cfg RuntimeConfig) string {
	if explicit := strings.TrimSpace(os.Getenv(frontendLANOriginEnvVar)); explicit != "" {
		return explicit
	}
	if cfg.NetworkProfile != "lan" {
		return ""
	}
	ip := firstLANIPv4()
	if ip == "" {
		return ""
	}
	return "http://" + ip + ":" + strconv.Itoa(cfg.FrontendPort)
}
