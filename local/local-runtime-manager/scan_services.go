package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	scanControlPlaneSourceDirName = "backend/scan-control-plane"
	fileWatcherSourceDirName      = "backend/file-watcher"

	scanControlPlaneHealthTimeout = 180 * time.Second
	fileWatcherHealthTimeout      = 180 * time.Second
)

type ScanControlPlaneManager struct {
	runner CommandRunner
}

func NewScanControlPlaneManager(r CommandRunner) *ScanControlPlaneManager {
	return &ScanControlPlaneManager{runner: r}
}

func (m *ScanControlPlaneManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.ScanControlPlaneBin), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.ScanControlPlaneStateDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.ScanControlPlaneTempDir, 0o755); err != nil {
		return err
	}
	if err := m.build(ctx, cfg, paths); err != nil {
		return err
	}
	if err := m.waitForDatabase(ctx, cfg, paths); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, paths.ScanControlPlaneBin)
	cmd.Dir = filepath.Join(paths.RepoRoot, scanControlPlaneSourceDirName)
	cmd.Env = append(os.Environ(), scanControlPlaneEnv(cfg, paths)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start scan-control-plane failed: %w", err)
	}
	if err := os.WriteFile(paths.ScanControlPlanePIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	registerLocalProcess(paths, scanControlPlaneProcessName, cmd.Process.Pid, []int{cfg.LocalProxy.ScanHostPort}, []string{paths.ScanControlPlaneBin})

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()
	if err := waitForHTTPHealth(ctx, cfg.LocalProxy.ScanHostPort, "/healthz", scanControlPlaneProcessName, scanControlPlaneHealthTimeout, waitErr); err != nil {
		_ = cmd.Process.Kill()
		_ = os.Remove(paths.ScanControlPlanePIDFile)
		unregisterLocalProcess(paths, scanControlPlaneProcessName, cmd.Process.Pid)
		return err
	}

	err := <-waitErr
	_ = os.Remove(paths.ScanControlPlanePIDFile)
	unregisterLocalProcess(paths, scanControlPlaneProcessName, cmd.Process.Pid)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("scan-control-plane exited: %w", err)
	}
	return nil
}

func (m *ScanControlPlaneManager) build(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if cfg.Profile == "desktop" {
		if info, err := os.Stat(paths.ScanControlPlaneBin); err == nil && !info.IsDir() {
			return nil
		}
		return fmt.Errorf("desktop scan-control-plane binary not found: %s", paths.ScanControlPlaneBin)
	}
	goBin := strings.TrimSpace(os.Getenv("GO"))
	if goBin == "" {
		goBin = "go"
	}
	res, err := m.runner.Run(ctx, Command{
		Name: goBin,
		Args: []string{"build", "-buildvcs=false", "-o", paths.ScanControlPlaneBin, "./cmd/scan-control-plane"},
		Dir:  filepath.Join(paths.RepoRoot, scanControlPlaneSourceDirName),
		Env:  goToolEnv(paths),
	})
	if err != nil {
		return fmt.Errorf("build scan-control-plane failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *ScanControlPlaneManager) waitForDatabase(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := os.MkdirAll(filepath.Dir(paths.ScanDBPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.ScanControlPlaneStateDir, 0o755); err != nil {
		return err
	}
	if strings.EqualFold(envText("LAZYMIND_SCAN_CONTROL_PLANE_DB_DRIVER", "sqlite"), "sqlite") {
		return nil
	}
	return fmt.Errorf("local scan-control-plane supports sqlite only; unset LAZYMIND_SCAN_CONTROL_PLANE_DB_DRIVER or set it to sqlite")
}

func (m *ScanControlPlaneManager) Down(ctx context.Context, paths RuntimePaths) error {
	return stopPIDFileProcess(ctx, paths.ScanControlPlanePIDFile)
}

type FileWatcherManager struct {
	runner CommandRunner
}

func NewFileWatcherManager(r CommandRunner) *FileWatcherManager {
	return &FileWatcherManager{runner: r}
}

func (m *FileWatcherManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.FileWatcherBin), 0o755); err != nil {
		return err
	}
	for _, dir := range []string{
		paths.FileWatcherBaseRoot,
		filepath.Join(paths.FileWatcherBaseRoot, "logs"),
		filepath.Join(paths.FileWatcherBaseRoot, "snapshots"),
		filepath.Join(paths.FileWatcherBaseRoot, "staging"),
		filepath.Join(paths.FileWatcherBaseRoot, "run"),
		cfg.FileWatcher.WatchHostDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := m.build(ctx, cfg, paths); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, paths.FileWatcherBin, "-config", filepath.Join(paths.RepoRoot, fileWatcherSourceDirName, "configs", "agent.yaml"))
	cmd.Dir = filepath.Join(paths.RepoRoot, fileWatcherSourceDirName)
	cmd.Env = append(os.Environ(), fileWatcherEnv(cfg, paths)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start file-watcher failed: %w", err)
	}
	if err := os.WriteFile(paths.FileWatcherPIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	registerLocalProcess(paths, fileWatcherProcessName, cmd.Process.Pid, []int{cfg.FileWatcher.Port}, append([]string{paths.FileWatcherBin}, cmd.Args...))

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()
	if err := waitForHTTPHealth(ctx, cfg.FileWatcher.Port, "/healthz", fileWatcherProcessName, fileWatcherHealthTimeout, waitErr); err != nil {
		_ = cmd.Process.Kill()
		_ = os.Remove(paths.FileWatcherPIDFile)
		unregisterLocalProcess(paths, fileWatcherProcessName, cmd.Process.Pid)
		return err
	}

	err := <-waitErr
	_ = os.Remove(paths.FileWatcherPIDFile)
	unregisterLocalProcess(paths, fileWatcherProcessName, cmd.Process.Pid)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("file-watcher exited: %w", err)
	}
	return nil
}

func (m *FileWatcherManager) build(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if cfg.Profile == "desktop" {
		if info, err := os.Stat(paths.FileWatcherBin); err == nil && !info.IsDir() {
			return nil
		}
		return fmt.Errorf("desktop file-watcher binary not found: %s", paths.FileWatcherBin)
	}
	goBin := strings.TrimSpace(os.Getenv("GO"))
	if goBin == "" {
		goBin = "go"
	}
	res, err := m.runner.Run(ctx, Command{
		Name: goBin,
		Args: []string{"build", "-buildvcs=false", "-o", paths.FileWatcherBin, "./cmd/main.go"},
		Dir:  filepath.Join(paths.RepoRoot, fileWatcherSourceDirName),
		Env:  goToolEnv(paths),
	})
	if err != nil {
		return fmt.Errorf("build file-watcher failed: %w (%s)", err, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (m *FileWatcherManager) Down(ctx context.Context, paths RuntimePaths) error {
	return stopPIDFileProcess(ctx, paths.FileWatcherPIDFile)
}

func scanControlPlaneEnv(cfg RuntimeConfig, paths RuntimePaths) []string {
	return []string{
		"LAZYMIND_RUNTIME_MODE=local",
		"LAZYMIND_SCAN_CONTROL_PLANE_ADDRESS=127.0.0.1",
		"LAZYMIND_SCAN_CONTROL_PLANE_PORT=" + strconv.Itoa(cfg.LocalProxy.ScanHostPort),
		"LAZYMIND_SCAN_CONTROL_PLANE_DB_DRIVER=sqlite",
		"LAZYMIND_SCAN_CONTROL_PLANE_DB_DSN=" + paths.ScanDBPath,
		"LAZYMIND_SCAN_CONTROL_PLANE_DB_MIGRATION_FILE=" + filepath.Join(paths.RepoRoot, scanControlPlaneSourceDirName, "migrations", "20260519101723_init.up.sql"),
		"LAZYMIND_SCAN_CONTROL_PLANE_CORE_BASE_URL=http://127.0.0.1:" + strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		"LAZYMIND_SCAN_CONTROL_PLANE_AGENT_BASE_URL=http://127.0.0.1:" + strconv.Itoa(cfg.FileWatcher.Port),
		"LAZYMIND_SCAN_CONTROL_PLANE_AGENT_TOKEN=" + cfg.FileWatcher.AgentToken,
		"LAZYMIND_SCAN_CONTROL_PLANE_LOCAL_FS_DEFAULT_AGENT_ID=" + cfg.FileWatcher.AgentID,
		"LAZYMIND_SCAN_CONTROL_PLANE_LOCAL_FS_PUBLIC_ROOT=" + cfg.FileWatcher.WatchHostDir,
		"LAZYMIND_SCAN_CONTROL_PLANE_FEISHU_BASE_URL=" + envText("LAZYMIND_SCAN_CONTROL_PLANE_FEISHU_BASE_URL", "https://open.feishu.cn"),
		"LAZYMIND_SCAN_CONTROL_PLANE_AUTH_SERVICE_BASE_URL=http://127.0.0.1:" + strconv.Itoa(cfg.AuthService.Port),
		"LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN=" + envText("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "dev-internal-service-token"),
		"LAZYMIND_REDIS_URL=",
		"LAZYMIND_STATE_BACKEND=sqlite",
		"LAZYMIND_STATE_SQLITE_PATH=" + filepath.Join(paths.ScanControlPlaneStateDir, "scan_state.db"),
		"LAZYMIND_SCAN_CONTROL_PLANE_TEMP_DIR=" + paths.ScanControlPlaneTempDir,
		"SOURCEENGINE_TARGET_SEARCH_CACHE_PREWARM_STAGGER=" + envText("SOURCEENGINE_TARGET_SEARCH_CACHE_PREWARM_STAGGER", "10s"),
	}
}

func fileWatcherEnv(cfg RuntimeConfig, paths RuntimePaths) []string {
	return []string{
		"LAZYMIND_RUNTIME_MODE=local",
		"LAZYMIND_FILE_WATCHER_AGENT_ID=" + cfg.FileWatcher.AgentID,
		"LAZYMIND_FILE_WATCHER_AGENT_TOKEN=" + cfg.FileWatcher.AgentToken,
		"LAZYMIND_FILE_WATCHER_LISTEN_ADDR=127.0.0.1:" + strconv.Itoa(cfg.FileWatcher.Port),
		"LAZYMIND_FILE_WATCHER_ADVERTISE_ADDR=http://127.0.0.1:" + strconv.Itoa(cfg.FileWatcher.Port),
		"LAZYMIND_FILE_WATCHER_CONTROL_PLANE_BASE_URL=http://127.0.0.1:" + strconv.Itoa(cfg.LocalProxy.ScanHostPort),
		"LAZYMIND_FILE_WATCHER_BASE_ROOT=" + paths.FileWatcherBaseRoot,
		"LAZYMIND_FILE_WATCHER_HOST_PATH_STYLE=" + cfg.FileWatcher.HostPathStyle,
		"LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR=" + cfg.FileWatcher.WatchHostDir,
		"LAZYMIND_FILE_WATCHER_WATCH_CONTAINER_DIR=" + cfg.FileWatcher.WatchHostDir,
		"LAZYMIND_FILE_WATCHER_ALLOWED_ROOT=" + cfg.FileWatcher.WatchHostDir,
	}
}

func scanControlPlaneHealthAlive(port int, timeout time.Duration) bool {
	return httpOK(context.Background(), "http://127.0.0.1:"+strconv.Itoa(port)+"/healthz", timeout)
}

func fileWatcherHealthAlive(port int, timeout time.Duration) bool {
	return httpOK(context.Background(), "http://127.0.0.1:"+strconv.Itoa(port)+"/healthz", timeout)
}

func stopPIDFileProcess(ctx context.Context, pidFile string) error {
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		_ = os.Remove(pidFile)
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidFile)
		return nil
	}
	if err := signalProcessGroup(pid, syscall.SIGINT); err != nil {
		_ = proc.Signal(os.Interrupt)
	}
	if !processAlive(pid) {
		_ = os.Remove(pidFile)
		return nil
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		_ = proc.Kill()
	}

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = signalProcessGroup(pid, syscall.SIGKILL)
			_ = proc.Kill()
			return ctx.Err()
		case <-deadline.C:
			_ = signalProcessGroup(pid, syscall.SIGKILL)
			_ = proc.Kill()
			_ = os.Remove(pidFile)
			return nil
		case <-ticker.C:
			alive, err := upLockProcessAlive(pidFile)
			if err != nil || !alive {
				_ = os.Remove(pidFile)
				return nil
			}
		}
	}
}
