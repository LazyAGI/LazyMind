package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func defaultProfileValue() string {
	return ""
}

func TestRuntimeConfigUsesLocalRuntimeRootAndManagerBinaryPaths(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if cfg.Profile != "local" {
		t.Fatalf("profile = %q, want local", cfg.Profile)
	}
	if paths.RuntimeRoot != filepath.Join(repo, ".lazymind-local") {
		t.Fatalf("runtime root = %q", paths.RuntimeRoot)
	}
	if localProcessComposeBin != ".lazymind-local/bin/process-compose" {
		t.Fatalf("process-compose bin = %q", localProcessComposeBin)
	}
}

func TestRuntimeConfigKeepsRuntimeDataUnderLocalRoot(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	wantUploadRoot := filepath.Join(paths.RuntimeRoot, "data", "core", "uploads")
	if paths.UploadRoot != wantUploadRoot {
		t.Fatalf("upload root = %q, want %q", paths.UploadRoot, wantUploadRoot)
	}
	for name, path := range map[string]string{
		"upload root":    paths.UploadRoot,
		"lazyllm temp":   paths.LazyLLMTempDir,
		"ocr cache":      paths.OCRCacheDir,
		"subagent data":  paths.SubagentDataDir,
		"trace data":     paths.TracesDir,
		"algorithm home": paths.AlgorithmHome,
		"milvus lite db": paths.MilvusLiteDBPath,
		"core sqlite db": paths.CoreDBPath,
		"lazyllm sqlite": paths.LazyLLMDBPath,
		"scan sqlite db": paths.ScanDBPath,
	} {
		if !strings.HasPrefix(path, paths.RuntimeRoot+string(os.PathSeparator)) {
			t.Fatalf("%s path %q is outside runtime root %q", name, path, paths.RuntimeRoot)
		}
	}
}

func TestEnsurePathUnderRootResolvesSymlinks(t *testing.T) {
	temp := t.TempDir()
	realRoot := filepath.Join(temp, "real-root")
	linkRoot := filepath.Join(temp, "link-root")
	python := filepath.Join(realRoot, "runtimes", "python", "bin", "python3")
	if err := os.MkdirAll(filepath.Dir(python), 0o755); err != nil {
		t.Fatalf("mkdir python dir: %v", err)
	}
	if err := os.WriteFile(python, []byte("python"), 0o755); err != nil {
		t.Fatalf("write python: %v", err)
	}
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatalf("symlink runtime root: %v", err)
	}
	linkPython := filepath.Join(linkRoot, "runtimes", "python", "bin", "python3")
	if err := ensurePathUnderRoot(linkPython, realRoot); err != nil {
		t.Fatalf("symlinked path should be under real root: %v", err)
	}
	if err := ensurePathUnderRoot(python, linkRoot); err != nil {
		t.Fatalf("real path should be under symlinked root: %v", err)
	}
}

func TestRegisterLocalProcessConcurrent(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	done := make(chan struct{}, 20)
	for i := 0; i < cap(done); i++ {
		i := i
		go func() {
			registerLocalProcess(paths, "svc-"+strconv.Itoa(i), 9000+i, []int{18000 + i}, []string{"cmd", strconv.Itoa(i)})
			done <- struct{}{}
		}()
	}
	for i := 0; i < cap(done); i++ {
		<-done
	}
	registry, err := readLocalProcessRegistry(paths)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if len(registry.Processes) != cap(done) {
		t.Fatalf("registry process count = %d, want %d: %#v", len(registry.Processes), cap(done), registry.Processes)
	}
}

func TestEnsureAllDirsCreatesRuntimeDataDirs(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure all dirs: %v", err)
	}
	for _, path := range []string{
		paths.UploadRoot,
		paths.LazyLLMTempDir,
		paths.OCRCacheDir,
		paths.SubagentDataDir,
		paths.TracesDir,
		paths.LazyLLMHome,
		paths.EvoDataDir,
	} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("expected runtime data dir %s: info=%v err=%v", path, info, err)
		}
	}
}

func TestEnsureAllDirsUsesOnlyApprovedTopLevelDirs(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure all dirs: %v", err)
	}
	allowed := map[string]bool{
		"bin":       true,
		"cache":     true,
		"config":    true,
		"data":      true,
		"deps":      true,
		"generated": true,
		"logs":      true,
		"run":       true,
		"runtimes":  true,
		"state":     true,
		"tmp":       true,
	}
	entries, err := os.ReadDir(paths.RuntimeRoot)
	if err != nil {
		t.Fatalf("read runtime root: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !allowed[entry.Name()] {
			t.Fatalf("unexpected top-level runtime dir %q", entry.Name())
		}
	}
}

func TestCLIRejectsProfileFlag(t *testing.T) {
	cli := NewCLI(io.Discard, io.Discard, &fakeRunner{t: t}, filepath.Join(t.TempDir(), "local-runtime-manager"))
	profileFlag := "--" + "profile"
	if err := cli.Run(context.Background(), []string{"status", profileFlag, "linux-browser"}); err == nil {
		t.Fatal("expected profile flag to be rejected")
	}
}

func TestProcessComposeGeneratedConfigContainsOnlyHostProcesses(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	manager := NewRuntimeManager(&fakeRunner{t: t}, filepath.Join(repo, ".lazymind-local", "bin", "local-runtime-manager"))
	var out strings.Builder
	if err := manager.processCompose.WriteGeneratedConfig(&out, repo, paths, cfg, paths.RunDirTokenFile, cfg.ProcessComposePort); err != nil {
		t.Fatalf("write generated config: %v", err)
	}
	var parsed processComposeConfig
	if err := yaml.Unmarshal([]byte(out.String()), &parsed); err != nil {
		t.Fatalf("generated config invalid yaml: %v\n%s", err, out.String())
	}
	for _, forbidden := range []string{legacyComposeServiceName, "internal " + "compose-", "--" + "profile"} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("generated config contains %q:\n%s", forbidden, out.String())
		}
	}
	for _, name := range []string{localProxyProcessName, authServiceProcessName, coreProcessName, scanControlPlaneProcessName, fileWatcherProcessName, frontendProcessName, milvusLiteProcessName, docServerProcessName, processorServerProcessName, processorWorkerProcessName, algoProcessName, chatProcessName} {
		proc, ok := parsed.Processes[name]
		if !ok {
			t.Fatalf("missing process %s", name)
		}
		if proc.Namespace != "host" {
			t.Fatalf("process %s namespace = %q, want host", name, proc.Namespace)
		}
	}
}

func TestProcessComposeGOBINIsUnderLocalRuntimeRoot(t *testing.T) {
	repo := t.TempDir()
	got, err := processComposeGOBIN(repo)
	if err != nil {
		t.Fatalf("process compose GOBIN: %v", err)
	}
	want := filepath.Join(repo, ".lazymind-local", "bin")
	if got != want {
		t.Fatalf("GOBIN = %q, want %q", got, want)
	}
}

func TestProcessComposeUsesLocalConfigHome(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner := &fakeRunner{t: t}
	manager := NewProcessComposeManager(runner, filepath.Join(paths.BinDir, "local-runtime-manager"))
	if err := manager.Up(context.Background(), cfg, paths); err != nil {
		t.Fatalf("process compose up: %v", err)
	}
	runner.assertCommandCount(1)
	env := map[string]string{}
	for _, item := range runner.calls[0].Env {
		k, v, ok := strings.Cut(item, "=")
		if ok {
			env[k] = v
		}
	}
	if env["HOME"] != paths.ProcessComposeHome {
		t.Fatalf("HOME = %q, want %q", env["HOME"], paths.ProcessComposeHome)
	}
	if env["XDG_CONFIG_HOME"] != paths.ConfigDir {
		t.Fatalf("XDG_CONFIG_HOME = %q, want %q", env["XDG_CONFIG_HOME"], paths.ConfigDir)
	}
}

func TestProcessComposeDownStreamsOutputWhenRunnerSupportsIt(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner := &fakeStreamRunner{fakeRunner: fakeRunner{t: t}}
	runner.streamHandlers = append(runner.streamHandlers, func(cmd Command) error {
		assertCommand(t, cmd, processComposeCommand(repo),
			"-p", strconv.Itoa(cfg.ProcessComposePort),
			"--token-file", paths.RunDirTokenFile,
			"down",
		)
		return nil
	})
	manager := NewProcessComposeManager(runner, filepath.Join(paths.BinDir, "local-runtime-manager"))
	if err := manager.Down(context.Background(), cfg, paths, io.Discard, io.Discard); err != nil {
		t.Fatalf("process-compose down: %v", err)
	}
	if len(runner.streamCalls) != 1 {
		t.Fatalf("expected 1 stream call got %d", len(runner.streamCalls))
	}
	runner.assertCommandCount(0)
}

func TestProcessComposeEnvPinsAllPlannedPorts(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	env := map[string]string{}
	for _, item := range runtimeCommandEnv(paths, cfg) {
		k, v, ok := strings.Cut(item, "=")
		if ok {
			env[k] = v
		}
	}
	wants := map[string]int{
		processComposePortEnvVar:     cfg.ProcessComposePort,
		frontendPortEnvVar:           cfg.FrontendPort,
		localProxyPortEnvVar:         cfg.LocalProxy.Port,
		localAuthPortEnvVar:          cfg.AuthService.Port,
		localCorePortEnvVar:          cfg.LocalProxy.CoreHostPort,
		localDocPortEnvVar:           cfg.Algorithm.DocPort,
		localProcessorPortEnvVar:     cfg.Algorithm.ProcessorPort,
		localAlgoPortEnvVar:          cfg.Algorithm.AlgoPort,
		localWorkerPortEnvVar:        cfg.Algorithm.WorkerPort,
		localChatPortEnvVar:          cfg.Algorithm.ChatPort,
		localMilvusPortEnvVar:        cfg.ModeProfile.VectorStore.Port,
		routerPortPoolStartEnvVar:    cfg.Algorithm.RouterPortPoolStart,
		routerPortPoolEndEnvVar:      cfg.Algorithm.RouterPortPoolEnd,
		routerPortsPerInstanceEnvVar: defaultRouterPortsPerInstance,
	}
	for key, want := range wants {
		if env[key] != strconv.Itoa(want) {
			t.Fatalf("%s = %q, want %d", key, env[key], want)
		}
	}
	if env[localPortsPinnedEnvVar] != "1" {
		t.Fatalf("%s = %q, want 1", localPortsPinnedEnvVar, env[localPortsPinnedEnvVar])
	}
	if env["HOME"] != paths.ServiceHome {
		t.Fatalf("HOME = %q, want %q", env["HOME"], paths.ServiceHome)
	}
	if env["XDG_CONFIG_HOME"] != paths.ConfigDir {
		t.Fatalf("XDG_CONFIG_HOME = %q, want %q", env["XDG_CONFIG_HOME"], paths.ConfigDir)
	}
}

func TestRuntimeConfigMovesRouterPortPoolWhenDefaultRangeUnavailable(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	listeners := occupyLocalPorts(t, defaultRouterPortPoolStart)
	defer func() {
		for _, ln := range listeners {
			_ = ln.Close()
		}
	}()
	cfg, _, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if cfg.Algorithm.RouterPortPoolStart == defaultRouterPortPoolStart {
		t.Fatalf("router pool start did not move from occupied default %d", defaultRouterPortPoolStart)
	}
	if cfg.Algorithm.RouterPortPoolEnd != cfg.Algorithm.RouterPortPoolStart+defaultRouterPortsPerInstance-1 {
		t.Fatalf("router pool end = %d, want start+%d", cfg.Algorithm.RouterPortPoolEnd, defaultRouterPortsPerInstance-1)
	}
}

func TestKillStaleRuntimeProcessesStopsScannerOrphan(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("sleep command unavailable: %v", err)
	}
	defer func() {
		if processAlive(cmd.Process.Pid) {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}()
	manager := NewRuntimeManager(&fakeRunner{t: t}, filepath.Join(paths.BinDir, "local-runtime-manager"))
	manager.processScanner = func(paths RuntimePaths) ([]LocalProcessRecord, error) {
		return []LocalProcessRecord{{
			Service:     "test-orphan",
			PID:         cmd.Process.Pid,
			RepoRoot:    paths.RepoRoot,
			RuntimeRoot: paths.RuntimeRoot,
		}}, nil
	}
	if err := manager.killStaleRuntimeProcesses(context.Background(), cfg, paths); err != nil {
		t.Fatalf("kill stale: %v", err)
	}
	_, _ = cmd.Process.Wait()
	if processAlive(cmd.Process.Pid) {
		t.Fatalf("expected orphan process %d to stop", cmd.Process.Pid)
	}
}

func TestStatusMigratesLegacyDockerStackState(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	state := defaultRuntimeState(cfg, cfg.ProcessComposePort, paths.RunDirTokenFile)
	state.Services[legacyComposeServiceName] = RuntimeServiceState{Kind: "docker" + "-compose", Status: "running"}
	delete(state.Services, processComposeServiceName)
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		t.Fatalf("write state: %v", err)
	}
	manager := NewRuntimeManager(&fakeRunner{t: t}, filepath.Join(repo, ".lazymind-local", "bin", "local-runtime-manager"))
	manager.probeAPI = func(port int, timeout time.Duration) bool { return false }
	out, err := manager.Status(context.Background(), cfg, paths, true)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var resp StatusResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	svc, ok := resp.Services[processComposeServiceName]
	if !ok {
		t.Fatalf("missing %s service", processComposeServiceName)
	}
	if svc.Kind != "host-supervisor" {
		t.Fatalf("kind = %q, want host-supervisor", svc.Kind)
	}
}

func TestDerivedToolInstallPathsStayUnderLocalRuntimeRoot(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	for _, path := range []string{paths.BinDir, paths.GeneratedDir, paths.StateDir, paths.LogsDir, paths.RunDir, paths.ConfigDir, paths.AuthServiceVenvDir, paths.AlgorithmVenv, paths.FrontendNodeModules, paths.MilvusLiteDBPath, paths.FileWatcherBaseRoot} {
		if !strings.HasPrefix(path, paths.RuntimeRoot+string(os.PathSeparator)) {
			t.Fatalf("%s is outside runtime root %s", path, paths.RuntimeRoot)
		}
	}
	if paths.AuthServiceVenvDir != filepath.Join(paths.RuntimeRoot, "deps", "python", "auth-service") {
		t.Fatalf("auth-service venv = %q", paths.AuthServiceVenvDir)
	}
	if paths.AlgorithmVenv != filepath.Join(paths.RuntimeRoot, "deps", "python", "algorithm") {
		t.Fatalf("algorithm venv = %q", paths.AlgorithmVenv)
	}
	if paths.FrontendNodeModules != filepath.Join(paths.RuntimeRoot, "deps", "node", "frontend") {
		t.Fatalf("frontend node_modules = %q", paths.FrontendNodeModules)
	}
	if paths.PythonRuntimeDir != filepath.Join(paths.RuntimeRoot, "runtimes", "python") {
		t.Fatalf("python runtime dir = %q", paths.PythonRuntimeDir)
	}
	if paths.PythonStateDir != filepath.Join(paths.RuntimeRoot, "state", "python") {
		t.Fatalf("python state dir = %q", paths.PythonStateDir)
	}
}

func TestGoToolEnvUsesHostCacheOutsideRuntimeRoot(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	hostHome := filepath.Join(t.TempDir(), "host-home")
	t.Setenv(localHostHomeEnvVar, hostHome)
	t.Setenv("HOME", paths.ServiceHome)
	t.Setenv("GOCACHE", filepath.Join(paths.RuntimeRoot, "Library", "Caches", "go-build"))
	t.Setenv("GOMODCACHE", filepath.Join(paths.RuntimeRoot, "go", "pkg", "mod"))

	env := map[string]string{}
	for _, item := range goToolEnv(paths) {
		k, v, ok := strings.Cut(item, "=")
		if ok {
			env[k] = v
		}
	}
	for _, key := range []string{"GOCACHE", "GOMODCACHE"} {
		if pathIsUnderRoot(env[key], paths.RuntimeRoot) {
			t.Fatalf("%s = %q is under runtime root %q", key, env[key], paths.RuntimeRoot)
		}
		if !strings.HasPrefix(env[key], hostHome+string(os.PathSeparator)) {
			t.Fatalf("%s = %q, want under host home %q", key, env[key], hostHome)
		}
	}
}

func TestPrepareFrontendNodeModulesLinksSourceTreeToRuntimeRoot(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	frontendDir := filepath.Join(repo, "frontend")
	nodeModules := filepath.Join(frontendDir, "node_modules")
	if err := os.MkdirAll(nodeModules, 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nodeModules, ".modules.yaml"), []byte(frontendModulesYAML(t, paths, nodeModules)), 0o644); err != nil {
		t.Fatalf("write pnpm metadata: %v", err)
	}
	if err := prepareFrontendNodeModules(paths, frontendDir); err != nil {
		t.Fatalf("prepare frontend node_modules: %v", err)
	}
	target, ok := symlinkTarget(nodeModules)
	if !ok {
		t.Fatalf("node_modules should be a symlink into runtime root")
	}
	if target != frontendRuntimeNodeModules(paths) {
		t.Fatalf("node_modules symlink = %q, want %q", target, frontendRuntimeNodeModules(paths))
	}
}

func TestPrepareFrontendNodeModulesKeepsRuntimeRootSymlink(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	frontendDir := filepath.Join(repo, "frontend")
	nodeModules := filepath.Join(frontendDir, "node_modules")
	runtimeNodeModules := frontendRuntimeNodeModules(paths)
	for _, dir := range []string{
		filepath.Join(runtimeNodeModules, ".bin"),
		filepath.Join(runtimeNodeModules, "vite", "bin"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir frontend dependency dir: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(runtimeNodeModules, ".modules.yaml"), []byte(frontendModulesYAML(t, paths, runtimeNodeModules)), 0o644); err != nil {
		t.Fatalf("write pnpm metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeNodeModules, ".bin", "vite"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write vite bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeNodeModules, "vite", "bin", "vite.js"), []byte("console.log('vite')\n"), 0o644); err != nil {
		t.Fatalf("write vite js: %v", err)
	}
	if err := os.MkdirAll(frontendDir, 0o755); err != nil {
		t.Fatalf("mkdir frontend dir: %v", err)
	}
	if err := os.Symlink(runtimeNodeModules, nodeModules); err != nil {
		t.Fatalf("link node_modules: %v", err)
	}
	if err := prepareFrontendNodeModules(paths, frontendDir); err != nil {
		t.Fatalf("prepare frontend node_modules: %v", err)
	}
	target, ok := symlinkTarget(nodeModules)
	if !ok || target != runtimeNodeModules {
		t.Fatalf("node_modules symlink = %q ok=%v, want %q", target, ok, runtimeNodeModules)
	}
	if ready, reason, err := frontendNodeModulesReady(paths, frontendDir); err != nil || !ready {
		t.Fatalf("node_modules should remain usable: ready=%v reason=%q err=%v", ready, reason, err)
	}
}

func TestPNPMLocalCacheEnvIsNonInteractive(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig("", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	env := map[string]string{}
	for _, item := range pnpmLocalCacheEnv(paths) {
		k, v, ok := strings.Cut(item, "=")
		if ok {
			env[k] = v
		}
	}
	for key, want := range map[string]string{
		"CI":                              "true",
		"COREPACK_ENABLE_DOWNLOAD_PROMPT": "0",
		"NPM_CONFIG_UPDATE_NOTIFIER":      "false",
		"npm_config_yes":                  "true",
	} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q", key, env[key], want)
		}
	}
	for _, key := range []string{"HOME", "XDG_CACHE_HOME"} {
		if pathIsUnderRoot(env[key], paths.RuntimeRoot) {
			t.Fatalf("%s = %q is under runtime root %q", key, env[key], paths.RuntimeRoot)
		}
	}
	for _, key := range []string{"PNPM_HOME", "NPM_CONFIG_STORE_DIR"} {
		if _, ok := env[key]; ok {
			t.Fatalf("%s should not be pinned into local runtime env", key)
		}
	}
	args := pnpmLocalCacheArgs(paths)
	virtualStoreFlag := false
	for i, arg := range args {
		if arg == "--store-dir" {
			t.Fatalf("pnpm store-dir should use pnpm's user-level default cache")
		}
		if arg == "--virtual-store-dir" && i+1 < len(args) {
			virtualStoreFlag = true
			want := filepath.Join(paths.FrontendNodeModules, ".pnpm")
			if args[i+1] != want {
				t.Fatalf("virtual store = %q, want %q", args[i+1], want)
			}
		}
	}
	if !virtualStoreFlag {
		t.Fatalf("pnpm virtual-store-dir flag is missing")
	}
}

func frontendModulesYAML(t *testing.T, paths RuntimePaths, nodeModules string) string {
	t.Helper()
	virtualStore, err := filepath.Rel(nodeModules, filepath.Join(paths.FrontendNodeModules, ".pnpm"))
	if err != nil {
		t.Fatalf("relative virtual store: %v", err)
	}
	return "nodeLinker: isolated\n" +
		"packageManager: pnpm@10.0.0\n" +
		"virtualStoreDir: " + filepath.ToSlash(virtualStore) + "\n"
}

func (r *fakeRunner) assertCommandCount(expected int) {
	if len(r.calls) != expected {
		r.t.Fatalf("expected %d calls got %d", expected, len(r.calls))
	}
}

func (r *fakeRunner) Run(ctx context.Context, cmd Command) (CommandResult, error) {
	r.calls = append(r.calls, cmd)
	if len(r.handlers) == 0 {
		return CommandResult{}, nil
	}
	call := r.handlers[0]
	r.handlers = r.handlers[1:]
	return call(cmd)
}

type fakeRunner struct {
	calls    []Command
	handlers []func(Command) (CommandResult, error)
	t        *testing.T
}

type fakeStreamRunner struct {
	fakeRunner
	streamCalls    []Command
	streamHandlers []func(Command) error
}

func (r *fakeStreamRunner) Stream(ctx context.Context, cmd Command, stdout, stderr io.Writer) error {
	r.streamCalls = append(r.streamCalls, cmd)
	if len(r.streamHandlers) == 0 {
		return nil
	}
	call := r.streamHandlers[0]
	r.streamHandlers = r.streamHandlers[1:]
	return call(cmd)
}

func writeComposeFixture(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("help:\n\t@true\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	mod := filepath.Join(repo, "local", "local-runtime-manager", "go.mod")
	if err := os.MkdirAll(filepath.Dir(mod), 0o755); err != nil {
		t.Fatalf("mkdir local runtime manager fixture: %v", err)
	}
	if err := os.WriteFile(mod, []byte("module fixture\n"), 0o644); err != nil {
		t.Fatalf("write go.mod fixture: %v", err)
	}
}

func occupyLocalPorts(t *testing.T, ports ...int) []net.Listener {
	return occupyPortsOn(t, "127.0.0.1", ports...)
}

func occupyPortsOn(t *testing.T, address string, ports ...int) []net.Listener {
	t.Helper()
	listeners := make([]net.Listener, 0, len(ports))
	for _, port := range ports {
		ln, err := net.Listen("tcp", net.JoinHostPort(address, strconv.Itoa(port)))
		if err != nil {
			for _, existing := range listeners {
				_ = existing.Close()
			}
			t.Skipf("port %d is already in use on %s on this test host: %v", port, address, err)
		}
		listeners = append(listeners, ln)
	}
	return listeners
}

func assertCommand(t *testing.T, cmd Command, name string, args ...string) {
	t.Helper()
	if cmd.Name != name {
		t.Fatalf("expected command %s got %s", name, cmd.Name)
	}
	if len(cmd.Args) != len(args) {
		t.Fatalf("expected args len %d got %d (%v)", len(args), len(cmd.Args), cmd.Args)
	}
	for i := range args {
		if cmd.Args[i] != args[i] {
			t.Fatalf("arg mismatch at %d expected %q got %q", i, args[i], cmd.Args[i])
		}
	}
}

func assertCommandContainsInOrder(t *testing.T, cmd Command, name string, args []string) {
	t.Helper()
	if cmd.Name != name {
		t.Fatalf("expected command %s got %s", name, cmd.Name)
	}
	if len(cmd.Args) < len(args) {
		t.Fatalf("expected at least %d args got %d", len(args), len(cmd.Args))
	}
	for i := range args {
		if cmd.Args[i] != args[i] {
			t.Fatalf("arg mismatch at %d expected %q got %q", i, args[i], cmd.Args[i])
		}
	}
}

func assertStringSlicesEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %v got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v got %v", want, got)
		}
	}
}

func assertContains(t *testing.T, args []string, want string) {
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Fatalf("missing arg %s in %v", want, args)
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func assertStringArgAfter(t *testing.T, args []string, flag string, want string) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == want {
			return
		}
	}
	t.Fatalf("missing arg pair %s %s in %v", flag, want, args)
}
