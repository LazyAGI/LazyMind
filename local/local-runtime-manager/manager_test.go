package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestResolveRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, repoComposeFileName), []byte(""), 0o644); err != nil {
		t.Fatalf("write compose root file: %v", err)
	}

	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := resolveRepoRoot(nested)
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if got != filepath.Clean(root) {
		t.Fatalf("expected %s got %s", root, got)
	}
}

func TestParseRuntimeOverlay(t *testing.T) {
	root := t.TempDir()
	overlay := filepath.Join(root, localComposeOverrideName)
	if err := os.MkdirAll(filepath.Dir(overlay), 0o755); err != nil {
		t.Fatalf("mkdir overlay dir: %v", err)
	}
	if err := os.WriteFile(overlay, []byte(`
x-lazymind-local:
  mode: "local" # quoted values and inline comments should parse cleanly
  disabled_container_services:
    - "auth-service"
    - core
  scale_disabled_container_services:
    - auth-service
`), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	cfg, err := parseRuntimeOverlay(overlay)
	if err != nil {
		t.Fatalf("parse overlay: %v", err)
	}
	if cfg.Mode != "local" {
		t.Fatalf("expected mode local got %s", cfg.Mode)
	}
	if len(cfg.DisabledContainerTypes) != 2 {
		t.Fatalf("expected 2 disabled services got %d", len(cfg.DisabledContainerTypes))
	}
	if cfg.DisabledContainerTypes[0] != "auth-service" || cfg.DisabledContainerTypes[1] != "core" {
		t.Fatalf("unexpected disabled services: %#v", cfg.DisabledContainerTypes)
	}
	if len(cfg.ScaleDisabledContainerTypes) != 1 || cfg.ScaleDisabledContainerTypes[0] != "auth-service" {
		t.Fatalf("unexpected scale disabled services: %#v", cfg.ScaleDisabledContainerTypes)
	}
}

func TestRuntimePathsEnsureAllDirsCreatesOnlyV1Directories(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	for _, dir := range []string{paths.StateDir, paths.LogsDir, paths.RunDir, paths.GeneratedDir} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("expected directory %s, info=%v err=%v", dir, info, err)
		}
	}
	for _, name := range []string{"data", "cache", "diagnostics"} {
		if _, err := os.Stat(filepath.Join(paths.RuntimeRoot, name)); !os.IsNotExist(err) {
			t.Fatalf("expected %s not to be created, err=%v", name, err)
		}
	}
}

func TestAcquireUpLockRemovesStaleLock(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	if err := os.WriteFile(paths.UpLockFile, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	release, err := acquireUpLock(paths)
	if err != nil {
		t.Fatalf("acquire stale lock: %v", err)
	}
	release()
	if _, err := os.Stat(paths.UpLockFile); !os.IsNotExist(err) {
		t.Fatalf("expected lock to be released, err=%v", err)
	}
}

func TestAcquireUpLockKeepsLiveLock(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	if err := os.WriteFile(paths.UpLockFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("write live lock: %v", err)
	}

	if _, err := acquireUpLock(paths); err == nil {
		t.Fatal("expected live lock to block acquire")
	}
}

func TestFilterRemainingServices(t *testing.T) {
	services := []string{"auth-service", "core", "web"}
	remaining, err := filterRemainingServices(services, []string{"core"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(remaining) != 2 || remaining[0] != "auth-service" || remaining[1] != "web" {
		t.Fatalf("unexpected remaining services: %v", remaining)
	}
	if _, err := filterRemainingServices(services, []string{"does-not-exist"}); err == nil {
		t.Fatalf("expected error for unknown disabled service")
	}
}

func TestClassifyComposeReadinessReportsFatalBeforePending(t *testing.T) {
	state, reason := classifyComposeReadiness([]ComposeServiceStatus{
		{Service: "chat", State: "created"},
		{Service: "lazyllm-parse-server", State: "exited", ExitCode: 1},
	})
	if state != composeReadinessFailed {
		t.Fatalf("expected failed state got %v", state)
	}
	if !strings.Contains(reason, "lazyllm-parse-server") {
		t.Fatalf("expected fatal service in reason got %q", reason)
	}
}

func TestComposeUpCommandIsCanonical(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	call := 0
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		call++
		expected := []string{"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"config", "--services",
		}
		assertCommand(t, cmd, "docker", expected...)
		return CommandResult{Stdout: "auth-service\ncore\nweb\n"}, nil
	}, func(cmd Command) (CommandResult, error) {
		call++
		assertCommandContainsInOrder(t, cmd, "docker", []string{
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"up",
			"--build",
			"auth-service", "core", "web",
		})
		return CommandResult{}, nil
	})

	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := manager.compose.ComposeUp(context.Background(), paths.RepoRoot, cfg.Profile); err != nil {
		t.Fatalf("compose up: %v", err)
	}
	if call != 2 {
		t.Fatalf("expected 2 compose calls got %d", call)
	}
}

func TestComposeUpScalesDisabledServicesToZero(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	overlay := filepath.Join(repo, localComposeOverrideName)
	if err := os.WriteFile(overlay, []byte("x-lazymind-local:\n  mode: local\n  disabled_container_services:\n    - redis\n    - auth-service\n    - evo-api\n  scale_disabled_container_services:\n    - redis\n    - auth-service\n"), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		return CommandResult{Stdout: "redis\nevo-api\nauth-service\ncore\n"}, nil
	}, func(cmd Command) (CommandResult, error) {
		assertCommandContainsInOrder(t, cmd, "docker", []string{
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"up",
			"--build",
			"--scale", "redis=0",
			"--scale", "auth-service=0",
			"core",
		})
		for i, arg := range cmd.Args {
			if arg == "--scale" && i+1 < len(cmd.Args) && cmd.Args[i+1] == "evo-api=0" {
				t.Fatalf("evo-api should not be scale guarded when omitted from scale_disabled_container_services: %v", cmd.Args)
			}
		}
		for _, arg := range cmd.Args {
			if arg == "redis" || arg == "auth-service" || arg == "evo-api" {
				t.Fatalf("disabled service %s should not be in explicit service list: %v", arg, cmd.Args)
			}
		}
		return CommandResult{}, nil
	})

	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := manager.compose.ComposeUp(context.Background(), paths.RepoRoot, cfg.Profile); err != nil {
		t.Fatalf("compose up: %v", err)
	}
}

func TestWriteGeneratedComposeConfig(t *testing.T) {
	runner := &fakeRunner{t: t}
	m := NewRuntimeManager(runner, filepath.Join("/tmp", "lazymind-local"))
	var b strings.Builder
	profile := "linux-browser"
	repo := t.TempDir()
	logPath := filepath.Join(repo, "run.log")
	tokenPath := filepath.Join(repo, "token")
	if err := m.processCompose.WriteGeneratedConfig(
		&builderWriter{builder: &b},
		repo,
		profile,
		logPath,
		filepath.Join(repo, "local-proxy.log"),
		filepath.Join(repo, "auth-service.log"),
		tokenPath,
		defaultProcessComposePort,
	); err != nil {
		t.Fatalf("write generated config: %v", err)
	}
	out := b.String()
	var parsed processComposeConfig
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("generated config is not valid yaml: %v\n%s", err, out)
	}
	proc, ok := parsed.Processes[processComposeServiceName]
	if !ok {
		t.Fatal("missing docker-stack process")
	}
	if parsed.Version != "0.5" || !parsed.IsStrict || !parsed.OrderedShutdown {
		t.Fatalf("unexpected root config: %#v", parsed)
	}
	if proc.WorkingDir != repo {
		t.Fatalf("unexpected working dir %q", proc.WorkingDir)
	}
	if !strings.Contains(proc.Command, "internal compose-up --profile "+profile) {
		t.Fatalf("missing compose-up command: %q", proc.Command)
	}
	if !strings.Contains(proc.Shutdown.Command, "internal compose-down --profile "+profile) {
		t.Fatalf("missing compose-down command: %q", proc.Shutdown.Command)
	}
	if proc.Shutdown.TimeoutSeconds != 60 {
		t.Fatalf("unexpected shutdown timeout %d", proc.Shutdown.TimeoutSeconds)
	}
	if proc.LogLocation != logPath {
		t.Fatalf("unexpected log location %q", proc.LogLocation)
	}
	if proc.Namespace != "container" {
		t.Fatalf("unexpected namespace %q", proc.Namespace)
	}
	localProxy, ok := parsed.Processes[localProxyProcessName]
	if !ok {
		t.Fatal("missing local-proxy process")
	}
	if !strings.Contains(localProxy.Command, "internal local-proxy-run --profile "+profile) {
		t.Fatalf("missing local-proxy-run command: %q", localProxy.Command)
	}
	if !strings.Contains(localProxy.Shutdown.Command, "internal local-proxy-down --profile "+profile) {
		t.Fatalf("missing local-proxy-down command: %q", localProxy.Shutdown.Command)
	}
	if localProxy.Namespace != "host" {
		t.Fatalf("unexpected local-proxy namespace %q", localProxy.Namespace)
	}
	authService, ok := parsed.Processes[authServiceProcessName]
	if !ok {
		t.Fatal("missing auth-service process")
	}
	if !strings.Contains(authService.Command, "internal auth-service-run --profile "+profile) {
		t.Fatalf("missing auth-service-run command: %q", authService.Command)
	}
	if !strings.Contains(authService.Shutdown.Command, "internal auth-service-down --profile "+profile) {
		t.Fatalf("missing auth-service-down command: %q", authService.Shutdown.Command)
	}
	if authService.LogLocation != filepath.Join(repo, "auth-service.log") {
		t.Fatalf("unexpected auth-service log location %q", authService.LogLocation)
	}
	if authService.Namespace != "host" {
		t.Fatalf("unexpected auth-service namespace %q", authService.Namespace)
	}
	if strings.Contains(out, "readiness_probe:") {
		t.Fatal("generated config should not include process-compose readiness_probe")
	}
}

func TestDerivedComposeProfilesUseBuiltInStoresByDefault(t *testing.T) {
	t.Setenv("LAZYMIND_DEPLOY_MINERU", "")
	t.Setenv("LAZYMIND_MILVUS_URI", "")
	t.Setenv("LAZYMIND_OPENSEARCH_URI", "")
	t.Setenv("LAZYMIND_ENABLE_MILVUS_DASHBOARD", "")
	t.Setenv("LAZYMIND_ENABLE_OPENSEARCH_DASHBOARD", "")

	assertStringSlicesEqual(t, derivedComposeProfileArgs(), []string{
		"--profile", "milvus",
		"--profile", "opensearch",
	})
}

func TestDerivedComposeProfilesSkipExternalStores(t *testing.T) {
	t.Setenv("LAZYMIND_DEPLOY_MINERU", "")
	t.Setenv("LAZYMIND_MILVUS_URI", "http://127.0.0.1:19530")
	t.Setenv("LAZYMIND_OPENSEARCH_URI", "https://search.example.test:9200")
	t.Setenv("LAZYMIND_ENABLE_MILVUS_DASHBOARD", "")
	t.Setenv("LAZYMIND_ENABLE_OPENSEARCH_DASHBOARD", "")

	assertStringSlicesEqual(t, derivedComposeProfileArgs(), nil)
}

func TestComposeUpStreamsDockerComposeLogsWhenSupported(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeStreamRunner{fakeRunner: fakeRunner{t: t}}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, "docker",
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"config", "--services",
		)
		return CommandResult{Stdout: "auth-service\ncore\n"}, nil
	})
	runner.streamHandlers = append(runner.streamHandlers, func(cmd Command) error {
		assertCommandContainsInOrder(t, cmd, "docker", []string{
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"up",
			"--build",
			"auth-service",
			"core",
		})
		return nil
	})

	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := manager.compose.ComposeUp(context.Background(), paths.RepoRoot, cfg.Profile); err != nil {
		t.Fatalf("compose up: %v", err)
	}
	if len(runner.streamCalls) != 1 {
		t.Fatalf("expected 1 stream call got %d", len(runner.streamCalls))
	}
}

func TestManagerUpWritesStateAndStartsProcessCompose(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	manager.probeAPI = func(port int, timeout time.Duration) bool { return true }
	manager.probeAuth = func(port int, timeout time.Duration) bool { return true }
	manager.pollInterval = time.Millisecond
	manager.upTimeout = time.Second
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		if cmd.Name != "process-compose" {
			t.Fatalf("expected process-compose got %s", cmd.Name)
		}
		assertContains(t, cmd.Args, "-D")
		assertContains(t, cmd.Args, "--ordered-shutdown")
		assertContains(t, cmd.Args, "-t=false")
		assertStringArgAfter(t, cmd.Args, "-p", strconv.Itoa(defaultProcessComposePort))
		return CommandResult{}, nil
	}, func(cmd Command) (CommandResult, error) {
		assertCommandContainsInOrder(t, cmd, "docker", []string{
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"ps",
			"-a",
			"--format",
			"json",
		})
		return CommandResult{Stdout: readyComposeStatusJSON()}, nil
	})
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := manager.Up(context.Background(), cfg, paths); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := os.Stat(paths.RunDirTokenFile); err != nil {
		t.Fatalf("token file missing: %v", err)
	}
	st, err := readRuntimeState(paths.StateFile)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	dockerStack, ok := st.Services[processComposeServiceName]
	if !ok {
		t.Fatalf("state missing docker-stack service")
	}
	if dockerStack.Status != "running" {
		t.Fatalf("unexpected docker-stack status: %s", dockerStack.Status)
	}
}

func TestWaitForAuthServiceHealthyFailsFastWhenPIDIsDead(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	manager.probeAuth = func(port int, timeout time.Duration) bool { return false }

	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("prepare dirs: %v", err)
	}
	if err := os.WriteFile(paths.AuthServicePIDFile, []byte("-1\n"), 0o600); err != nil {
		t.Fatalf("write auth pid: %v", err)
	}

	start := time.Now()
	err = manager.waitForAuthServiceHealthy(context.Background(), defaultLocalProxyAuthHostPort, time.Minute, paths.AuthServicePIDFile)
	if err == nil {
		t.Fatal("expected auth-service process failure")
	}
	if !strings.Contains(err.Error(), "auth-service process exited") {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected fail-fast, took %s", elapsed)
	}
}

func TestWaitForAuthServiceHealthyIgnoresMissingPIDUntilTimeout(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	manager.probeAuth = func(port int, timeout time.Duration) bool { return false }

	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("prepare dirs: %v", err)
	}

	err = manager.waitForAuthServiceHealthy(context.Background(), defaultLocalProxyAuthHostPort, time.Millisecond, paths.AuthServicePIDFile)
	if err == nil {
		t.Fatal("expected auth-service health timeout")
	}
	if !strings.Contains(err.Error(), "health check timed out") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeManagerUpReusesRunningProcessCompose(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	probeCalls := 0
	manager.probeAPI = func(port int, timeout time.Duration) bool {
		probeCalls++
		return probeCalls == 1
	}
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("prepare dirs: %v", err)
	}
	state := defaultRuntimeState(cfg, defaultProcessComposePort, paths.RunDirTokenFile)
	state.OverallStatus = "ready"
	state.Services[processComposeServiceName] = RuntimeServiceState{Kind: "docker-compose", Status: "running"}
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		t.Fatalf("write state: %v", err)
	}
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommandContainsInOrder(t, cmd, "docker", []string{
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"ps",
			"-a",
		})
		return CommandResult{Stdout: "NAME STATUS\nweb running\n"}, nil
	})

	if err := manager.Up(context.Background(), cfg, paths); err != nil {
		t.Fatalf("up: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected only docker ps call, got %d calls", len(runner.calls))
	}
}

func TestRuntimeManagerUpFailsOnExitedService(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	manager.probeAPI = func(port int, timeout time.Duration) bool { return true }
	manager.probeAuth = func(port int, timeout time.Duration) bool { return true }
	manager.pollInterval = time.Millisecond
	manager.upTimeout = time.Second
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner.handlers = append(runner.handlers,
		func(cmd Command) (CommandResult, error) {
			assertCommandContainsInOrder(t, cmd, "process-compose", []string{"--config", filepath.ToSlash(paths.GeneratedConfig)})
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommandContainsInOrder(t, cmd, "docker", []string{
				"compose",
				"-f", filepath.Join(repo, repoComposeFileName),
				"-f", filepath.Join(repo, localComposeOverrideName),
				"--profile", "milvus",
				"--profile", "opensearch",
				"ps",
				"-a",
				"--format",
				"json",
			})
			return CommandResult{Stdout: `[{"Name":"parse","Service":"lazyllm-parse-server","State":"exited","ExitCode":1}]`}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommandContainsInOrder(t, cmd, "docker", []string{
				"compose",
				"-f", filepath.Join(repo, repoComposeFileName),
				"-f", filepath.Join(repo, localComposeOverrideName),
				"--profile", "milvus",
				"--profile", "opensearch",
				"ps",
				"-a",
			})
			return CommandResult{Stdout: "parse exited\n"}, nil
		},
	)

	if err := manager.Up(context.Background(), cfg, paths); err == nil {
		t.Fatalf("expected up failure")
	}
	state, err := readRuntimeState(paths.StateFile)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.OverallStatus != "failed" {
		t.Fatalf("expected failed state got %s", state.OverallStatus)
	}
}

func TestProcessComposeManagerDownCommandIncludesPortAndTokenFile(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join("/tmp", "lazymind-local"))
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("prepare dirs: %v", err)
	}

	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, "process-compose",
			"--config", filepath.ToSlash(paths.GeneratedConfig),
			"-p", strconv.Itoa(defaultProcessComposePort),
			"--token-file", paths.RunDirTokenFile,
			"down",
		)
		return CommandResult{}, nil
	})
	if err := manager.processCompose.Down(context.Background(), cfg, paths); err != nil {
		t.Fatalf("process-compose down: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 command got %d", len(runner.calls))
	}
}

func TestRuntimeManagerDownFallsBackToComposeDownOnProcessComposeFailure(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	probeCalls := 0
	manager.probeAPI = func(port int, timeout time.Duration) bool {
		probeCalls++
		return probeCalls == 1
	}
	manager.probeAuth = func(port int, timeout time.Duration) bool { return false }
	manager.pollInterval = time.Millisecond
	manager.downTimeout = time.Second
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("prepare dirs: %v", err)
	}
	if err := os.WriteFile(paths.GeneratedConfig, []byte("generated"), 0o644); err != nil {
		t.Fatalf("write generated: %v", err)
	}
	if err := os.WriteFile(paths.StateFile, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	runner.handlers = append(runner.handlers,
		func(cmd Command) (CommandResult, error) {
			assertCommandContainsInOrder(t, cmd, "process-compose", []string{
				"--config", filepath.ToSlash(paths.GeneratedConfig),
				"-p", strconv.Itoa(defaultProcessComposePort),
				"--token-file", paths.RunDirTokenFile,
				"down",
			})
			return CommandResult{}, fmt.Errorf("process-compose failure")
		},
		func(cmd Command) (CommandResult, error) {
			assertCommandContainsInOrder(t, cmd, "docker", []string{
				"compose",
				"-f", filepath.Join(repo, repoComposeFileName),
				"-f", filepath.Join(repo, localComposeOverrideName),
				"--profile", "milvus",
				"--profile", "opensearch",
				"down",
				"--remove-orphans",
			})
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommandContainsInOrder(t, cmd, "docker", []string{
				"compose",
				"-f", filepath.Join(repo, repoComposeFileName),
				"-f", filepath.Join(repo, localComposeOverrideName),
				"--profile", "milvus",
				"--profile", "opensearch",
				"ps",
				"-a",
				"--format",
				"json",
			})
			return CommandResult{Stdout: "[]"}, nil
		},
	)

	if err := manager.Down(context.Background(), cfg, paths); err != nil {
		t.Fatalf("down: %v", err)
	}
	state, err := readRuntimeState(paths.StateFile)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if got := state.OverallStatus; got != "stopped" {
		t.Fatalf("unexpected overallStatus %s", got)
	}
	if got := state.Services[processComposeServiceName].Status; got != "stopped" {
		t.Fatalf("unexpected service status %s", got)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 commands got %d", len(runner.calls))
	}
}

func TestStatusJSONContainsDockerStackService(t *testing.T) {
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join("/tmp", "lazymind-local"))
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("prepare dirs: %v", err)
	}
	state := defaultRuntimeState(RuntimeConfig{
		Profile:     defaultProfileValue(),
		RepoRoot:    paths.RepoRoot,
		RuntimeRoot: paths.RuntimeRoot,
	}, defaultProcessComposePort, filepath.Join(paths.RunDir, tokenFileName))
	state.Services["docker-stack"] = RuntimeServiceState{Kind: "docker-compose", Status: "running"}
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	out, err := manager.Status(context.Background(), RuntimeConfig{Profile: defaultProfileValue(), RepoRoot: repo, RuntimeRoot: paths.RuntimeRoot}, paths, true)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var resp StatusResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Runtime != "local" {
		t.Fatalf("expected runtime local got %s", resp.Runtime)
	}
	if resp.ProcessCompose.APIPort == 0 {
		t.Fatalf("expected process compose port")
	}
	if _, ok := resp.Services[processComposeServiceName]; !ok {
		t.Fatalf("missing docker-stack service")
	}
}

func TestStatusMarksStaleStateWhenProcessComposeAPIIsDown(t *testing.T) {
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join("/tmp", "lazymind-local"))
	manager.probeAPI = func(port int, timeout time.Duration) bool { return false }
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("prepare dirs: %v", err)
	}
	state := defaultRuntimeState(cfg, defaultProcessComposePort, paths.RunDirTokenFile)
	state.OverallStatus = "ready"
	state.Services[processComposeServiceName] = RuntimeServiceState{Kind: "docker-compose", Status: "running"}
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		t.Fatalf("write state: %v", err)
	}
	out, err := manager.Status(context.Background(), cfg, paths, true)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var resp StatusResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.OverallStatus != "stale" {
		t.Fatalf("expected stale overallStatus got %s", resp.OverallStatus)
	}
	if got := resp.Services[processComposeServiceName].Status; got != "stale" {
		t.Fatalf("expected stale service got %s", got)
	}
}

type builderWriter struct {
	builder *strings.Builder
}

func (w *builderWriter) Write(p []byte) (int, error) {
	return w.builder.Write(p)
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
	if err := os.WriteFile(filepath.Join(repo, repoComposeFileName), []byte("services: {}"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	overlay := filepath.Join(repo, localComposeOverrideName)
	if err := os.MkdirAll(filepath.Dir(overlay), 0o755); err != nil {
		t.Fatalf("mkdir overlay dir: %v", err)
	}
	if err := os.WriteFile(overlay, []byte("# Local Runtime override file\nx-lazymind-local:\n  mode: local\n  disabled_container_services: []\n"), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
}

func readyComposeStatusJSON() string {
	return `[
{"Name":"auth","Service":"auth-service","State":"running","Health":"healthy","ExitCode":0},
{"Name":"core","Service":"core","State":"running","Health":"","ExitCode":0},
{"Name":"db","Service":"db-bootstrap","State":"exited","Health":"","ExitCode":0}
]`
}

func assertCommand(t *testing.T, cmd Command, name string, args ...string) {
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

func assertStringArgAfter(t *testing.T, args []string, flag string, want string) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == want {
			return
		}
	}
	t.Fatalf("missing arg pair %s %s in %v", flag, want, args)
}
