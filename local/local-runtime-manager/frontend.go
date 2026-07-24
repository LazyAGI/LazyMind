package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const caddyInstallAttempts = 3

type FrontendManager struct {
	runner CommandRunner
}

func NewFrontendManager(r CommandRunner) *FrontendManager {
	return &FrontendManager{runner: r}
}

func (m *FrontendManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(paths.CaddyBin), 0o755); err != nil {
		return err
	}

	frontendDir := filepath.Join(paths.RepoRoot, "frontend")
	if cfg.Profile == "desktop" {
		if info, err := os.Stat(filepath.Join(frontendDir, "dist", "index.html")); err != nil || info.IsDir() {
			return fmt.Errorf("desktop frontend dist not found: %s", filepath.Join(frontendDir, "dist"))
		}
	} else {
		if err := prepareFrontendNodeModules(paths, frontendDir); err != nil {
			return err
		}
		install := Command{
			Name: pnpmCommand(),
			Args: append([]string{"install", "--frozen-lockfile", "--prefer-offline", "--reporter", "append-only"}, pnpmLocalCacheArgs(paths)...),
			Dir:  frontendDir,
			Env:  pnpmLocalCacheEnv(paths),
		}
		if err := m.runFrontendCommand(ctx, install, envDuration("LAZYMIND_FRONTEND_INSTALL_TIMEOUT", 15*time.Minute), "frontend dependency install"); err != nil {
			return err
		}
		if ready, reason, err := frontendNodeModulesReady(paths, frontendDir); err != nil {
			return err
		} else if !ready {
			return fmt.Errorf("frontend dependency install completed but node_modules is not usable: %s", reason)
		}

		build := Command{
			Name: pnpmCommand(),
			Args: []string{"build"},
			Dir:  frontendDir,
			Env:  append(pnpmLocalCacheEnv(paths), frontendBuildEnv()...),
		}
		if err := m.runFrontendCommand(ctx, build, envDuration("LAZYMIND_FRONTEND_BUILD_TIMEOUT", 10*time.Minute), "frontend build"); err != nil {
			return err
		}
	}

	if err := writeCaddyfile(paths, cfg); err != nil {
		return err
	}
	caddyBin, err := m.ensureCaddy(ctx, cfg, paths)
	if err != nil {
		return err
	}

	run := Command{
		Name: caddyBin,
		Args: []string{"run", "--config", paths.CaddyConfig, "--adapter", "caddyfile"},
		Dir:  paths.RepoRoot,
	}
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if err := os.WriteFile(paths.FrontendPIDFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	registerLocalProcess(paths, frontendProcessName, os.Getpid(), []int{cfg.FrontendPort}, append([]string{caddyBin}, run.Args...))
	defer func() {
		_ = os.Remove(paths.FrontendPIDFile)
		unregisterLocalProcess(paths, frontendProcessName, os.Getpid())
	}()
	if res, err := m.runner.Run(ctx, run); err != nil {
		return fmt.Errorf("frontend caddy exited: %w (%s)", err, commandResultDetail(res))
	}
	return nil
}

func (m *FrontendManager) runFrontendCommand(ctx context.Context, cmd Command, timeout time.Duration, description string) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if streamer, ok := m.runner.(CommandStreamer); ok {
		if err := streamer.Stream(ctx, cmd, os.Stdout, os.Stderr); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("%s timed out after %s", description, timeout)
			}
			return fmt.Errorf("%s failed: %w", description, err)
		}
		return nil
	}
	if res, err := m.runner.Run(ctx, cmd); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s timed out after %s (%s)", description, timeout, commandResultDetail(res))
		}
		return fmt.Errorf("%s failed: %w (%s)", description, err, commandResultDetail(res))
	}
	return nil
}

func (m *FrontendManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	if readPIDFileQuiet(paths.FrontendPIDFile) > 0 {
		return stopPIDFileProcess(ctx, paths, frontendProcessName, paths.FrontendPIDFile)
	}

	records := discoverLocalRuntimeProcesses(paths, cfg, scanLocalRuntimeProcesses)
	frontendRecords := make([]LocalProcessRecord, 0, len(records))
	for _, record := range records {
		if record.Service == frontendProcessName && record.PID != os.Getpid() {
			frontendRecords = append(frontendRecords, record)
		}
	}
	if len(frontendRecords) == 0 {
		return nil
	}
	err := stopLocalProcessRecords(ctx, frontendRecords)
	cleanupLocalProcessRecords(paths, frontendRecords)
	return err
}

func frontendBuildEnv() []string {
	mode := strings.TrimSpace(os.Getenv("VITE_LAZYMIND_MODE"))
	if mode == "" {
		mode = "local"
	}
	env := []string{"VITE_LAZYMIND_MODE=" + mode}
	for _, key := range []string{"VITE_HIDE_EVO", "VITE_API_BASE_URL", "VITE_APP_LOGO", "VITE_APP_CHAT_TITLE"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func pnpmLocalCacheArgs(paths RuntimePaths) []string {
	return []string{
		"--virtual-store-dir", filepath.Join(frontendRuntimeNodeModules(paths), ".pnpm"),
	}
}

func pnpmLocalCacheEnv(paths RuntimePaths) []string {
	return append(hostToolEnv(paths),
		"CI=true",
		"COREPACK_ENABLE_DOWNLOAD_PROMPT=0",
		"NPM_CONFIG_UPDATE_NOTIFIER=false",
		"npm_config_yes=true",
	)
}

func prepareFrontendNodeModules(paths RuntimePaths, frontendDir string) error {
	nodeModules := filepath.Join(frontendDir, "node_modules")
	runtimeNodeModules := frontendRuntimeNodeModules(paths)
	if err := os.MkdirAll(runtimeNodeModules, 0o755); err != nil {
		return fmt.Errorf("create frontend runtime node_modules: %w", err)
	}
	if target, ok := directoryLinkTarget(nodeModules); ok {
		if filepath.Clean(target) == filepath.Clean(runtimeNodeModules) {
			if ready, _, err := frontendNodeModulesReady(paths, frontendDir); err != nil {
				return err
			} else if !ready {
				if err := os.RemoveAll(runtimeNodeModules); err != nil {
					return fmt.Errorf("recreate stale frontend runtime node_modules: %w", err)
				}
				if err := os.MkdirAll(runtimeNodeModules, 0o755); err != nil {
					return fmt.Errorf("recreate frontend runtime node_modules: %w", err)
				}
			}
			return nil
		}
		if err := os.Remove(nodeModules); err != nil {
			return fmt.Errorf("remove stale frontend node_modules symlink: %w", err)
		}
	} else if _, err := os.Stat(nodeModules); err == nil {
		ready, _, err := frontendNodeModulesReady(paths, frontendDir)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(nodeModules); err != nil {
			return fmt.Errorf("move frontend node_modules into local build root: %w", err)
		}
		if ready {
			_ = os.RemoveAll(runtimeNodeModules)
			if err := os.MkdirAll(runtimeNodeModules, 0o755); err != nil {
				return fmt.Errorf("recreate frontend runtime node_modules: %w", err)
			}
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect frontend node_modules: %w", err)
	}
	if err := createDirectoryLink(runtimeNodeModules, nodeModules); err != nil {
		return fmt.Errorf("link frontend node_modules to local build root: %w", err)
	}
	return nil
}

func pnpmCommand() string {
	if explicit := strings.TrimSpace(os.Getenv("PNPM")); explicit != "" {
		return explicit
	}
	if runtime.GOOS == "windows" {
		return "pnpm.cmd"
	}
	return "pnpm"
}

func frontendRuntimeNodeModules(paths RuntimePaths) string {
	return paths.FrontendNodeModules
}

func frontendNodeModulesReady(paths RuntimePaths, frontendDir string) (bool, string, error) {
	nodeModules := filepath.Join(frontendDir, "node_modules")
	if _, err := os.Stat(nodeModules); err != nil {
		if os.IsNotExist(err) {
			return false, "node_modules is missing", nil
		}
		return false, "", fmt.Errorf("inspect frontend node_modules: %w", err)
	}
	modulesYAML := filepath.Join(nodeModules, ".modules.yaml")
	content, err := os.ReadFile(modulesYAML)
	if err != nil {
		if os.IsNotExist(err) {
			return false, ".modules.yaml is missing", nil
		}
		return false, "", fmt.Errorf("read frontend pnpm metadata: %w", err)
	}
	metadata := filepath.ToSlash(string(content))
	virtualStoreDir := filepath.ToSlash(filepath.Join(frontendRuntimeNodeModules(paths), ".pnpm"))
	if !metadataContainsAnyPath(metadata, virtualStoreDir, relativePathOrEmpty(nodeModules, filepath.Join(frontendRuntimeNodeModules(paths), ".pnpm")), relativePathOrEmpty(frontendRuntimeNodeModules(paths), filepath.Join(frontendRuntimeNodeModules(paths), ".pnpm"))) {
		return false, "pnpm virtualStoreDir does not point at frontend runtime node_modules", nil
	}
	for _, required := range []string{
		frontendToolPath(nodeModules, "vite"),
		filepath.Join(nodeModules, "vite", "bin", "vite.js"),
	} {
		if _, err := os.Stat(required); err != nil {
			if os.IsNotExist(err) {
				return false, fmt.Sprintf("%s is missing or points to a missing file", required), nil
			}
			return false, "", fmt.Errorf("inspect frontend dependency %s: %w", required, err)
		}
	}
	return true, "", nil
}

func frontendToolPath(nodeModules, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(nodeModules, ".bin", name+".cmd")
	}
	return filepath.Join(nodeModules, ".bin", name)
}

func relativePathOrEmpty(base string, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func metadataContainsAnyPath(metadata string, paths ...string) bool {
	for _, path := range paths {
		if path != "" && strings.Contains(metadata, filepath.ToSlash(path)) {
			return true
		}
	}
	return false
}

func commandResultDetail(res CommandResult) string {
	detail := strings.TrimSpace(res.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(res.Stdout)
	}
	if detail == "" {
		return "no command output"
	}
	if len(detail) > 4000 {
		return detail[:4000] + "...(truncated)"
	}
	return detail
}

func writeCaddyfile(paths RuntimePaths, cfg RuntimeConfig) error {
	distRoot := filepath.ToSlash(filepath.Join(paths.RepoRoot, "frontend", "dist"))
	proxy := "http://127.0.0.1:" + strconv.Itoa(cfg.LocalProxy.Port)
	siteAddress := fmt.Sprintf("http://localhost:%d, http://127.0.0.1:%d", cfg.FrontendPort, cfg.FrontendPort)
	bindAddress := "127.0.0.1"
	if cfg.NetworkProfile == "lan" {
		siteAddress = fmt.Sprintf("http://:%d", cfg.FrontendPort)
		bindAddress = "0.0.0.0"
	}
	content := fmt.Sprintf(`{
	admin off
	auto_https off
}

%s {
	bind %s
	root * %s
	encode gzip

	handle /api/* {
		reverse_proxy %s {
			flush_interval -1
		}
	}

	handle /api-docs/* {
		reverse_proxy %s {
			flush_interval -1
		}
	}

	handle /_local/* {
		reverse_proxy %s {
			flush_interval -1
		}
	}

	handle {
		try_files {path} /index.html
		file_server
	}
}
`, siteAddress, bindAddress, strconv.Quote(distRoot), proxy, proxy, proxy)
	return os.WriteFile(paths.CaddyConfig, []byte(content), 0o644)
}

func (m *FrontendManager) ensureCaddy(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) (string, error) {
	if explicit := strings.TrimSpace(os.Getenv(caddyBinEnvVar)); explicit != "" {
		return explicit, nil
	}
	if info, err := os.Stat(paths.CaddyBin); err == nil && !info.IsDir() {
		return paths.CaddyBin, nil
	}
	if cfg.Profile == "desktop" {
		return "", fmt.Errorf("desktop Caddy binary not found: %s", paths.CaddyBin)
	}
	goBin := strings.TrimSpace(os.Getenv("GO"))
	if goBin == "" {
		goBin = "go"
	}
	if err := os.MkdirAll(filepath.Dir(paths.CaddyBin), 0o755); err != nil {
		return "", err
	}
	install := Command{
		Name: goBin,
		Args: []string{"install", "github.com/caddyserver/caddy/v2/cmd/caddy@v" + cfg.CaddyVersion},
		Dir:  paths.RepoRoot,
		Env:  append(goToolEnv(paths), "GOBIN="+paths.BinDir),
	}
	var lastErr error
	var lastDetail string
	for attempt := 1; attempt <= caddyInstallAttempts; attempt++ {
		res, err := m.runner.Run(ctx, install)
		if err == nil {
			return paths.CaddyBin, nil
		}
		lastErr = err
		lastDetail = commandResultDetail(res)
		if ctx.Err() != nil {
			break
		}
	}
	return "", fmt.Errorf("install Caddy failed after %d attempt(s): %w (%s)", caddyInstallAttempts, lastErr, lastDetail)
}
