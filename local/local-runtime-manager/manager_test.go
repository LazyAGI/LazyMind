package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
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

func TestRuntimeConfigAllocatesAvailableLocalPorts(t *testing.T) {
	for _, envName := range []string{
		processComposePortEnvVar,
		frontendPortEnvVar,
		localProxyPortEnvVar,
		localProxyAuthHostPortEnvVar,
		localProxyCoreHostPortEnvVar,
		localPostgresPortEnvVar,
	} {
		t.Setenv(envName, "")
	}
	listeners := occupyLocalPorts(t,
		defaultProcessComposePort,
		defaultFrontendPort,
		defaultLocalProxyPort,
		defaultLocalProxyAuthHostPort,
		defaultLocalProxyCoreHostPort,
		defaultLocalPostgresPort,
	)
	defer func() {
		for _, ln := range listeners {
			_ = ln.Close()
		}
	}()

	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, _, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if cfg.ProcessComposePort == defaultProcessComposePort {
		t.Fatalf("expected process-compose port to avoid occupied default")
	}
	if cfg.FrontendPort == defaultFrontendPort {
		t.Fatalf("expected frontend port to avoid occupied default")
	}
	if cfg.LocalProxy.Port == defaultLocalProxyPort {
		t.Fatalf("expected local proxy port to avoid occupied default")
	}
	if cfg.LocalProxy.AuthHostPort == defaultLocalProxyAuthHostPort {
		t.Fatalf("expected auth host port to avoid occupied default")
	}
	if cfg.LocalProxy.CoreHostPort == defaultLocalProxyCoreHostPort {
		t.Fatalf("expected core host port to avoid occupied default")
	}
	if cfg.Algorithm.PostgresPort == defaultLocalPostgresPort {
		t.Fatalf("expected postgres host port to avoid occupied default")
	}
	env := strings.Join(localComposeEnv(cfg), "\n")
	for _, want := range []string{
		"LAZYMIND_LOCAL_PROXY_PORT=" + strconv.Itoa(cfg.LocalProxy.Port),
		"LAZYMIND_FRONTEND_PORT=" + strconv.Itoa(cfg.FrontendPort),
		"LAZYMIND_LOCAL_PROXY_CORE_HOST_PORT=" + strconv.Itoa(cfg.LocalProxy.CoreHostPort),
		"LAZYMIND_LOCAL_POSTGRES_PORT=" + strconv.Itoa(cfg.Algorithm.PostgresPort),
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("compose env missing %q in %s", want, env)
		}
	}
}

func TestRuntimeConfigMovesDefaultFrontendPortWhenOccupied(t *testing.T) {
	t.Setenv(frontendPortEnvVar, strconv.Itoa(defaultFrontendPort))
	ln := occupyLocalPorts(t, defaultFrontendPort)
	defer func() {
		for _, existing := range ln {
			_ = existing.Close()
		}
	}()

	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, _, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if cfg.FrontendPort == defaultFrontendPort {
		t.Fatalf("expected Makefile default frontend port to move when occupied")
	}
	if !strings.Contains(strings.Join(localComposeEnv(cfg), "\n"), "LAZYMIND_FRONTEND_PORT="+strconv.Itoa(cfg.FrontendPort)) {
		t.Fatalf("compose env missing frontend port")
	}
}

func TestRuntimeConfigKeepsPinnedFrontendPortWhenOccupied(t *testing.T) {
	t.Setenv(frontendPortEnvVar, strconv.Itoa(defaultFrontendPort))
	t.Setenv(localPortsPinnedEnvVar, "1")
	ln := occupyLocalPorts(t, defaultFrontendPort)
	defer func() {
		for _, existing := range ln {
			_ = existing.Close()
		}
	}()

	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, _, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if cfg.FrontendPort != defaultFrontendPort {
		t.Fatalf("expected pinned frontend port %d got %d", defaultFrontendPort, cfg.FrontendPort)
	}
}

func TestFrontendBuildEnvIncludesLocalViteOverrides(t *testing.T) {
	t.Setenv("VITE_LAZYMIND_MODE", "")
	t.Setenv("VITE_HIDE_EVO", "true")
	t.Setenv("VITE_API_BASE_URL", "http://127.0.0.1:5024")
	t.Setenv("VITE_APP_LOGO", "/logo.svg")
	t.Setenv("VITE_APP_CHAT_TITLE", "Local Chat")

	assertStringSlicesEqual(t, frontendBuildEnv(), []string{
		"VITE_LAZYMIND_MODE=local",
		"VITE_HIDE_EVO=true",
		"VITE_API_BASE_URL=http://127.0.0.1:5024",
		"VITE_APP_LOGO=/logo.svg",
		"VITE_APP_CHAT_TITLE=Local Chat",
	})
}

func TestAlgorithmServiceEnvIncludesCloudParityDefaults(t *testing.T) {
	for _, name := range []string{
		"TZ",
		"LANGFUSE_HOST",
		"LANGFUSE_BASE_URL",
		"LANGFUSE_PUBLIC_KEY",
		"LANGFUSE_SECRET_KEY",
		"LAZYLLM_TRACE_ENABLED",
		"OTEL_EXPORTER_OTLP_TIMEOUT",
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT",
		"LAZYMIND_LANGFUSE_FORCE_FLUSH_TIMEOUT_MS",
		"LAZYMIND_OCR_SERVER_URL",
		"LAZYMIND_MINERU_BACKEND",
		"LAZYMIND_MINERU_SERVER_PORT",
		"LAZYLLM_MINERU_BACKEND",
		"LAZYLLM_MINERU_API_KEY",
		"LAZYLLM_PADDLE_API_KEY",
		"LAZYMIND_RESET_ALGO_ON_STARTUP",
		"LAZYMIND_RESET_ALL_ON_STARTUP",
		"LAZYMIND_MAX_RETRIES",
		"LAZYMIND_REVIEW_MAX_RETRIES",
		"LAZYMIND_SKILL_REVIEW_DEBUG",
		"LAZYMIND_WORD_GROUP_APPLY_URL",
		"http_proxy",
		"https_proxy",
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"no_proxy",
		"NO_PROXY",
		"LAZYLLM_OPENAI_API_KEY",
		"LAZYLLM_GLM_API_KEY",
		"LAZYLLM_QWEN_API_KEY",
		"LAZYLLM_SENSENOVA_API_KEY",
		"LAZYLLM_SENSENOVA_SECRET_KEY",
		"LAZYLLM_KIMI_API_KEY",
		"LAZYLLM_DEEPSEEK_API_KEY",
		"LAZYLLM_DOUBAO_API_KEY",
		"LAZYLLM_SILICONFLOW_API_KEY",
		"LAZYLLM_MINIMAX_API_KEY",
		"LAZYLLM_AIPING_API_KEY",
		"LAZYMIND_MAAS_API_KEY",
		"LAZYMIND_OPENSEARCH_URI",
		"LAZYMIND_OPENSEARCH_USER",
		"LAZYMIND_OPENSEARCH_PASSWORD",
		"LAZYMIND_EVO_CODE_TIMEOUT_S",
		"LAZYMIND_EVO_LLM_ROLE",
	} {
		t.Setenv(name, "")
	}
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	env := algorithmServiceEnv(cfg, paths, algoProcessName)
	uploads := filepath.Join(paths.RepoRoot, "data", "core", "uploads")
	noProxy := "127.0.0.1,localhost,::1,core,chat,evo-api,doc-server,lazyllm-algo,parsing,milvus,opensearch,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
	for _, want := range []string{
		"LAZYLLM_INIT_DOC=True",
		"LAZYMIND_MOUNT_BASE_DIR=" + uploads,
		"http_proxy=",
		"https_proxy=",
		"HTTP_PROXY=",
		"HTTPS_PROXY=",
		"no_proxy=" + noProxy,
		"NO_PROXY=" + noProxy,
		"LAZYLLM_OPENAI_API_KEY=",
		"LAZYLLM_GLM_API_KEY=",
		"LAZYLLM_QWEN_API_KEY=",
		"LAZYLLM_SENSENOVA_API_KEY=",
		"LAZYLLM_SENSENOVA_SECRET_KEY=",
		"LAZYLLM_KIMI_API_KEY=",
		"LAZYLLM_DEEPSEEK_API_KEY=",
		"LAZYLLM_DOUBAO_API_KEY=",
		"LAZYLLM_SILICONFLOW_API_KEY=",
		"LAZYLLM_MINIMAX_API_KEY=",
		"LAZYLLM_AIPING_API_KEY=",
		"LAZYMIND_MAAS_API_KEY=",
		"TZ=Asia/Shanghai",
		"LANGFUSE_HOST=",
		"LANGFUSE_BASE_URL=",
		"LANGFUSE_PUBLIC_KEY=",
		"LANGFUSE_SECRET_KEY=",
		"LAZYLLM_TRACE_ENABLED=1",
		"OTEL_EXPORTER_OTLP_TIMEOUT=60",
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT=60",
		"LAZYMIND_LANGFUSE_FORCE_FLUSH_TIMEOUT_MS=70000",
		"LAZYMIND_OCR_SERVER_URL=",
		"LAZYMIND_MINERU_BACKEND=pipeline",
		"LAZYMIND_MINERU_SERVER_PORT=8000",
		"LAZYLLM_MINERU_BACKEND=pipeline",
		"LAZYLLM_MINERU_API_KEY=",
		"LAZYLLM_PADDLE_API_KEY=",
		"LAZYMIND_RESET_ALGO_ON_STARTUP=false",
		"LAZYMIND_RESET_ALL_ON_STARTUP=false",
		"LAZYMIND_MAX_RETRIES=20",
		"LAZYMIND_REVIEW_MAX_RETRIES=5",
		"LAZYMIND_SKILL_REVIEW_DEBUG=false",
		"LAZYMIND_OPENSEARCH_URI=https://127.0.0.1:" + strconv.Itoa(cfg.Algorithm.OpenSearchPort),
		"LAZYMIND_OPENSEARCH_USER=admin",
		"LAZYMIND_OPENSEARCH_PASSWORD=LazyRAG_OpenSearch123!",
		"LAZYMIND_EVO_CODE_TIMEOUT_S=900",
		"LAZYMIND_EVO_LLM_ROLE=evo_llm",
		"LAZYMIND_WORD_GROUP_APPLY_URL=",
	} {
		if !containsString(env, want) {
			t.Fatalf("algorithm env missing %q in %v", want, env)
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

func TestAcquireAlgorithmPythonLockRemovesStaleLock(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	lockFile := filepath.Join(paths.RunDir, "algorithm-python.lock")
	if err := os.WriteFile(lockFile, []byte("-1\n"), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	release, err := acquireAlgorithmPythonLock(context.Background(), paths)
	if err != nil {
		t.Fatalf("acquire stale algorithm lock: %v", err)
	}
	release()
	if _, err := os.Stat(lockFile); !os.IsNotExist(err) {
		t.Fatalf("expected algorithm lock to be released, err=%v", err)
	}
}

func TestAcquireAlgorithmPythonLockWaitsForLiveLock(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	lockFile := filepath.Join(paths.RunDir, "algorithm-python.lock")
	if err := os.WriteFile(lockFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("write live lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := acquireAlgorithmPythonLock(ctx, paths); err == nil {
		t.Fatal("expected live algorithm lock to block until context cancellation")
	}
}

func TestUVCommandFindsUserLocalInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("UV", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", home)

	uv := filepath.Join(home, ".local", "bin", "uv")
	if err := os.MkdirAll(filepath.Dir(uv), 0o755); err != nil {
		t.Fatalf("mkdir uv dir: %v", err)
	}
	if err := os.WriteFile(uv, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write uv: %v", err)
	}

	got, ok := uvCommand()
	if !ok {
		t.Fatal("expected uv command from user local install")
	}
	if got != uv {
		t.Fatalf("expected %s got %s", uv, got)
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

func TestBuildEnabledServicesUsesDockerComposeBuild(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, "docker",
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"config", "--format", "json",
		)
		return CommandResult{Stdout: `{
  "services": {
    "auth-service": {"build": {"context": "./backend/auth"}},
    "core": {"image": "core-image"},
    "web": {"build": {"context": "./frontend"}}
  }
}`}, nil
	}, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, "docker",
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"build",
			"auth-service",
			"web",
		)
		return CommandResult{}, nil
	})
	if err := manager.compose.BuildEnabledServices(context.Background(), repo, []string{"auth-service", "core", "web"}); err != nil {
		t.Fatalf("build enabled services: %v", err)
	}
	runner.assertCommandCount(2)
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
		assertCommand(t, cmd, "docker",
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"config", "--format", "json",
		)
		return CommandResult{Stdout: composeConfigJSONNoBuildFixture()}, nil
	}, func(cmd Command) (CommandResult, error) {
		call++
		assertCommandContainsInOrder(t, cmd, "docker", []string{
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"up",
			"--no-build",
			"--detach",
			"--no-deps",
			"auth-service", "core", "web",
		})
		return CommandResult{}, nil
	})

	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := manager.compose.ComposeUp(context.Background(), cfg, paths); err != nil {
		t.Fatalf("compose up: %v", err)
	}
	if call != 3 {
		t.Fatalf("expected 3 compose calls got %d", call)
	}
}

func TestComposeUpOmitsDisabledServices(t *testing.T) {
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
			"config", "--format", "json",
		})
		return CommandResult{Stdout: composeConfigJSONNoBuildFixture()}, nil
	}, func(cmd Command) (CommandResult, error) {
		assertCommandContainsInOrder(t, cmd, "docker", []string{
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"up",
			"--no-build",
			"--detach",
			"--no-deps",
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
			if arg == "--scale" {
				t.Fatalf("disabled services should be omitted instead of scaled: %v", cmd.Args)
			}
		}
		return CommandResult{}, nil
	})

	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := manager.compose.ComposeUp(context.Background(), cfg, paths); err != nil {
		t.Fatalf("compose up: %v", err)
	}
}

func TestWriteGeneratedComposeConfig(t *testing.T) {
	runner := &fakeRunner{t: t}
	m := NewRuntimeManager(runner, filepath.Join("/tmp", "lazymind-local"))
	var b strings.Builder
	profile := "linux-browser"
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(profile, repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := m.processCompose.WriteGeneratedConfig(
		&builderWriter{builder: &b},
		repo,
		profile,
		paths,
		cfg,
		paths.RunDirTokenFile,
		cfg.ProcessComposePort,
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
	if !strings.Contains(proc.Command, "LAZYMIND_FRONTEND_PORT="+strconv.Itoa(cfg.FrontendPort)) {
		t.Fatalf("compose command missing frontend env: %q", proc.Command)
	}
	if !strings.Contains(proc.Command, localPortsPinnedEnvVar+"=1") {
		t.Fatalf("compose command missing pinned port env: %q", proc.Command)
	}
	if !strings.Contains(proc.Shutdown.Command, "internal compose-down --profile "+profile) {
		t.Fatalf("missing compose-down command: %q", proc.Shutdown.Command)
	}
	if proc.Shutdown.TimeoutSeconds != 60 {
		t.Fatalf("unexpected shutdown timeout %d", proc.Shutdown.TimeoutSeconds)
	}
	if proc.LogLocation != paths.LogFilePath {
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
	if !strings.Contains(localProxy.Command, "LAZYMIND_LOCAL_PROXY_PORT="+strconv.Itoa(cfg.LocalProxy.Port)) {
		t.Fatalf("local-proxy command missing proxy env: %q", localProxy.Command)
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
	if !strings.Contains(authService.Command, "LAZYMIND_AUTH_SERVICE_PORT="+strconv.Itoa(cfg.AuthService.Port)) {
		t.Fatalf("auth-service command missing auth env: %q", authService.Command)
	}
	if !strings.Contains(authService.Shutdown.Command, "internal auth-service-down --profile "+profile) {
		t.Fatalf("missing auth-service-down command: %q", authService.Shutdown.Command)
	}
	if authService.LogLocation != paths.AuthServiceLog {
		t.Fatalf("unexpected auth-service log location %q", authService.LogLocation)
	}
	if authService.Namespace != "host" {
		t.Fatalf("unexpected auth-service namespace %q", authService.Namespace)
	}
	for _, service := range []string{docServerProcessName, processorServerProcessName, processorWorkerProcessName, algoProcessName, chatProcessName} {
		proc, ok := parsed.Processes[service]
		if !ok {
			t.Fatalf("missing algorithm process %s", service)
		}
		if !strings.Contains(proc.Command, "internal algorithm-run --service "+service+" --profile "+profile) {
			t.Fatalf("missing algorithm-run command for %s: %q", service, proc.Command)
		}
		if !strings.Contains(proc.Shutdown.Command, "internal algorithm-down --service "+service+" --profile "+profile) {
			t.Fatalf("missing algorithm-down command for %s: %q", service, proc.Shutdown.Command)
		}
		if proc.Namespace != "host" {
			t.Fatalf("unexpected namespace for %s: %q", service, proc.Namespace)
		}
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
	}, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, "docker",
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"config", "--format", "json",
		)
		return CommandResult{Stdout: composeConfigJSONNoBuildFixture()}, nil
	})
	runner.streamHandlers = append(runner.streamHandlers, func(cmd Command) error {
		assertCommandContainsInOrder(t, cmd, "docker", []string{
			"compose",
			"-f", filepath.Join(repo, repoComposeFileName),
			"-f", filepath.Join(repo, localComposeOverrideName),
			"--profile", "milvus",
			"--profile", "opensearch",
			"up",
			"--no-build",
			"--detach",
			"--no-deps",
			"auth-service",
			"core",
		})
		return nil
	})

	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := manager.compose.ComposeUp(context.Background(), cfg, paths); err != nil {
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
	manager.probeCore = func(port int, timeout time.Duration) bool { return true }
	manager.waitHostReady = func(context.Context, RuntimeConfig) error { return nil }
	manager.pollInterval = time.Millisecond
	manager.upTimeout = time.Second
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		if cmd.Name != "process-compose" {
			t.Fatalf("expected process-compose got %s", cmd.Name)
		}
		assertContains(t, cmd.Args, "-D")
		assertContains(t, cmd.Args, "--ordered-shutdown")
		assertContains(t, cmd.Args, "-t=false")
		assertStringArgAfter(t, cmd.Args, "-p", strconv.Itoa(cfg.ProcessComposePort))
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

func TestRuntimeManagerUpFailsWhenHostAlgorithmsDoNotBecomeReady(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join(repo, "lazymind-local"))
	manager.probeAPI = func(port int, timeout time.Duration) bool { return true }
	manager.probeAuth = func(port int, timeout time.Duration) bool { return true }
	manager.waitHostReady = func(context.Context, RuntimeConfig) error {
		return fmt.Errorf("host algorithm not ready")
	}
	manager.pollInterval = time.Millisecond
	manager.upTimeout = time.Second
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		return CommandResult{}, nil
	}, func(cmd Command) (CommandResult, error) {
		return CommandResult{Stdout: readyComposeStatusJSON()}, nil
	})
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
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
	manager.runtimeReady = func(context.Context, RuntimeConfig, RuntimePaths) bool { return true }
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("prepare dirs: %v", err)
	}
	state := defaultRuntimeState(cfg, cfg.ProcessComposePort, paths.RunDirTokenFile)
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
			"-p", strconv.Itoa(cfg.ProcessComposePort),
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
				"-p", strconv.Itoa(cfg.ProcessComposePort),
				"--token-file", paths.RunDirTokenFile,
				"down",
			})
			return CommandResult{}, fmt.Errorf("process-compose failure")
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "pkill", "-f", regexp.QuoteMeta(repo)+"/(local/bin/process-compose|\\.lazymind-local/bin/local-proxy|\\.lazymind-local/python/\\.venv/bin/python|\\.lazymind-local/venvs/auth-service/bin/python|local/local-runtime-manager/lazymind-local internal)")
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommandContainsInOrder(t, cmd, "sh", []string{"-c"})
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			if cmd.Name != paths.LocalProxyStopScript {
				t.Fatalf("expected local-proxy stop script got %s", cmd.Name)
			}
			return CommandResult{}, nil
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
	if len(runner.calls) != 6 {
		t.Fatalf("expected 6 commands got %d", len(runner.calls))
	}
}

func TestStatusJSONContainsDockerStackService(t *testing.T) {
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, filepath.Join("/tmp", "lazymind-local"))
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("prepare dirs: %v", err)
	}
	state := defaultRuntimeState(cfg, cfg.ProcessComposePort, filepath.Join(paths.RunDir, tokenFileName))
	state.Services["docker-stack"] = RuntimeServiceState{Kind: "docker-compose", Status: "running"}
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
	state := defaultRuntimeState(cfg, cfg.ProcessComposePort, paths.RunDirTokenFile)
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

func occupyLocalPorts(t *testing.T, ports ...int) []net.Listener {
	t.Helper()
	listeners := make([]net.Listener, 0, len(ports))
	for _, port := range ports {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			for _, existing := range listeners {
				_ = existing.Close()
			}
			t.Skipf("port %d is already in use on this test host: %v", port, err)
		}
		listeners = append(listeners, ln)
	}
	return listeners
}

func readyComposeStatusJSON() string {
	return `[
{"Name":"auth","Service":"auth-service","State":"running","Health":"healthy","ExitCode":0},
{"Name":"core","Service":"core","State":"running","Health":"","ExitCode":0},
{"Name":"db","Service":"db-bootstrap","State":"exited","Health":"","ExitCode":0}
]`
}

func composeConfigJSONNoBuildFixture() string {
	return `{
  "services": {
    "auth-service": {"image": "auth-image"},
    "core": {"image": "core-image"},
    "web": {"image": "web-image"},
    "redis": {"image": "redis-image"},
    "evo-api": {"image": "evo-image"},
    "frontend": {"image": "frontend-image"}
  }
}`
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
