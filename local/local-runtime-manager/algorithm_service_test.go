package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestAlgorithmPreparePythonPinsSetuptoolsForLocalVenv(t *testing.T) {
	installFakeUVOnPath(t)
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	if err := os.MkdirAll(filepath.Join(repo, "algorithm", "lazyllm", "lazyllm"), 0o755); err != nil {
		t.Fatalf("mkdir lazyllm submodule fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "algorithm"), 0o755); err != nil {
		t.Fatalf("mkdir algorithm dir: %v", err)
	}
	requirements := filepath.Join(repo, "algorithm", "requirements.txt")
	if err := os.WriteFile(requirements, []byte("pymilvus==2.4.14\n"), 0o644); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	localRequirements := filepath.Join(repo, "algorithm", "requirements-local.txt")
	if err := os.WriteFile(localRequirements, []byte("pymilvus==3.0.0\nmilvus-lite==3.0.0\n"), 0o644); err != nil {
		t.Fatalf("write local requirements: %v", err)
	}

	runner := &fakeRunner{t: t}
	manager := NewAlgorithmServiceManager(runner)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure runtime dirs: %v", err)
	}
	runner.handlers = append(runner.handlers,
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "python", "install", "--install-dir", paths.PythonRuntimeDir, defaultLocalPythonVersion)
			assertEnvContains(t, cmd.Env, "UV_PYTHON_INSTALL_DIR="+paths.PythonRuntimeDir)
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "python", "find", "--managed-python", "--no-python-downloads", "--resolve-links", defaultLocalPythonVersion)
			assertEnvContains(t, cmd.Env, "UV_PYTHON_INSTALL_DIR="+paths.PythonRuntimeDir)
			return CommandResult{Stdout: filepath.Join(paths.PythonRuntimeDir, "cpython-3.11.15", "bin", "python3.11") + "\n"}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "venv", "--managed-python", "--no-python-downloads", "--relocatable", "--seed", "--link-mode", "copy", "--python", filepath.Join(paths.PythonRuntimeDir, "cpython-3.11.15", "bin", "python3.11"), paths.AlgorithmVenv)
			if err := os.MkdirAll(filepath.Dir(paths.AlgorithmPython), 0o755); err != nil {
				t.Fatalf("mkdir algorithm venv bin: %v", err)
			}
			if err := os.WriteFile(paths.AlgorithmPython, []byte("python"), 0o755); err != nil {
				t.Fatalf("write algorithm python: %v", err)
			}
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "pip", "install", "--python", paths.AlgorithmPython, "--link-mode", "copy", "--strict", "setuptools<81")
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "pip", "install", "--python", paths.AlgorithmPython, "--link-mode", "copy", "--strict", "lazyllm==1.3.0a1")
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, venvExecutable(paths.AlgorithmVenv, "lazyllm"), "install", "rag")
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "pip", "install", "--python", paths.AlgorithmPython, "--link-mode", "copy", "--strict", "-r", requirements)
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "pip", "install", "--python", paths.AlgorithmPython, "--link-mode", "copy", "--strict", "-r", localRequirements)
			return CommandResult{}, nil
		},
	)

	if err := manager.preparePython(context.Background(), cfg, paths, false); err != nil {
		t.Fatalf("prepare algorithm python: %v", err)
	}
	runner.assertCommandCount(8)
}

func TestEnsureLazyLLMSubmoduleInitializesMissingSubmodule(t *testing.T) {
	repo := t.TempDir()
	runner := &fakeRunner{t: t}
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, "git", "submodule", "update", "--init", "algorithm/lazyllm")
		if cmd.Dir != repo {
			t.Fatalf("git dir = %q, want %q", cmd.Dir, repo)
		}
		required := filepath.Join(repo, "algorithm", "lazyllm", "lazyllm")
		if err := os.MkdirAll(required, 0o755); err != nil {
			t.Fatalf("mkdir initialized submodule fixture: %v", err)
		}
		return CommandResult{}, nil
	})

	if err := ensureLazyLLMSubmodule(context.Background(), runner, repo); err != nil {
		t.Fatalf("ensure lazyllm submodule: %v", err)
	}
	runner.assertCommandCount(1)
}

func TestAlgorithmServiceEnvPinsLocalRouterHost(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	env := algorithmServiceEnv(cfg, paths, chatProcessName)

	assertEnvContains(t, env, "LAZYMIND_ROUTER_HOST=127.0.0.1")
	assertEnvContains(t, env, "LAZYMIND_WORKFLOW_EXECUTOR_TOKEN=dev-workflow-executor-token")
	assertEnvContains(t, env, "LAZYMIND_WORKFLOWS_DIR="+filepath.Join(repo, "workflows"))
}

func TestWorkflowExecutorTokenMatchesCoreAndAlgorithmOverride(t *testing.T) {
	t.Setenv("LAZYMIND_WORKFLOW_EXECUTOR_TOKEN", "custom-workflow-secret")
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatal(err)
	}
	assertEnvContains(t, algorithmServiceEnv(cfg, paths, chatProcessName),
		"LAZYMIND_WORKFLOW_EXECUTOR_TOKEN=custom-workflow-secret")
	assertEnvContains(t, coreServiceEnv(cfg, paths),
		"LAZYMIND_WORKFLOW_EXECUTOR_TOKEN=custom-workflow-secret")
}

func TestAlgorithmServiceEnvTrustedLocalMode(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	for _, tc := range []struct {
		name        string
		manifest    bool
		environment string
		want        string
	}{
		{name: "disabled by default", want: "LAZYMIND_TRUSTED_LOCAL_MODE=false"},
		{name: "enabled by desktop manifest", manifest: true, want: "LAZYMIND_TRUSTED_LOCAL_MODE=true"},
		{name: "enabled by source environment", environment: "true", want: "LAZYMIND_TRUSTED_LOCAL_MODE=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LAZYMIND_TRUSTED_LOCAL_MODE", tc.environment)
			testPaths := paths
			testPaths.TrustedLocalMode = tc.manifest

			env := algorithmServiceEnv(cfg, testPaths, chatProcessName)

			assertEnvContains(t, env, tc.want)
		})
	}
}

func TestAlgorithmServiceEnvDoesNotForceEditablePPT(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	env := algorithmServiceEnv(cfg, paths, chatProcessName)

	assertEnvNotContains(t, env, "LAZYMIND_OUTPUT_EDITABLE_PPT=")
	assertEnvContains(t, env, "LAZYMIND_PPT_EXPORT_CLI="+filepath.Join(paths.RepoRoot, "workflows", "ppt-workflow", "runtime", "scripts", "export_pptx", "html_to_pptx.mjs"))
	assertEnvContains(t, env, "LAZYMIND_PPT_EXPORT_DEPS="+filepath.Join(paths.RuntimeRoot, "deps", "editable-ppt"))
	assertEnvContains(t, env, "PLAYWRIGHT_BROWSERS_PATH="+filepath.Join(paths.RuntimeRoot, "deps", "editable-ppt", "browsers"))
}

func TestDesktopAlgorithmRegisterPolicyForInstallVersion(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig("desktop", repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure runtime dirs: %v", err)
	}
	t.Setenv("LAZYLLM_ALGO_REGISTER_POLICY", "")
	t.Setenv(desktopAppVersionEnvVar, "1.2.3")

	if got := algorithmRegisterPolicy(cfg, paths); got != "force" {
		t.Fatalf("first registration policy = %q, want force", got)
	}
	if err := markAlgorithmRegistrationVersion(cfg, paths); err != nil {
		t.Fatalf("mark registration version: %v", err)
	}
	if got := algorithmRegisterPolicy(cfg, paths); got != "update" {
		t.Fatalf("ordinary restart policy = %q, want update", got)
	}

	t.Setenv(desktopAppVersionEnvVar, "1.2.4")
	if got := algorithmRegisterPolicy(cfg, paths); got != "force" {
		t.Fatalf("upgraded registration policy = %q, want force", got)
	}
}

func TestDesktopAlgorithmRegisterPolicyHonorsExplicitOverride(t *testing.T) {
	cfg := RuntimeConfig{Profile: "desktop"}
	paths := RuntimePaths{StateDir: t.TempDir()}
	t.Setenv(desktopAppVersionEnvVar, "1.2.3")
	t.Setenv("LAZYLLM_ALGO_REGISTER_POLICY", "none")

	if got := algorithmRegisterPolicy(cfg, paths); got != "none" {
		t.Fatalf("explicit registration policy = %q, want none", got)
	}
}

func TestDesktopAlgorithmRegisterPolicyWithoutVersionDefaultsToUpdate(t *testing.T) {
	cfg := RuntimeConfig{Profile: "desktop"}
	paths := RuntimePaths{StateDir: t.TempDir()}
	t.Setenv(desktopAppVersionEnvVar, "")
	t.Setenv("LAZYLLM_ALGO_REGISTER_POLICY", "")

	if got := algorithmRegisterPolicy(cfg, paths); got != "update" {
		t.Fatalf("versionless desktop registration policy = %q, want update", got)
	}
}

func TestLocalAlgorithmRegisterPolicyDefaultsToUpdate(t *testing.T) {
	cfg := RuntimeConfig{Profile: "local"}
	paths := RuntimePaths{StateDir: t.TempDir()}
	t.Setenv("LAZYLLM_ALGO_REGISTER_POLICY", "")

	if got := algorithmRegisterPolicy(cfg, paths); got != "update" {
		t.Fatalf("local registration policy = %q, want update", got)
	}
}

func TestAlgorithmServiceEnvDisablesRouter(t *testing.T) {
	for _, profile := range []string{"local", "desktop"} {
		t.Run(profile, func(t *testing.T) {
			repo := t.TempDir()
			writeComposeFixture(t, repo)
			cfg, paths, err := NewRuntimeConfig(profile, repo)
			if err != nil {
				t.Fatalf("runtime config: %v", err)
			}
			t.Setenv("LAZYMIND_ENABLE_ROUTER", "true")

			env := algorithmServiceEnv(cfg, paths, algoProcessName)

			assertEnvContains(t, env, "LAZYMIND_ENABLE_ROUTER=false")
		})
	}
}

func TestAlgorithmServiceEnvAlwaysDisablesLazyLLMRuntimeDocs(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	t.Setenv("LAZYLLM_INIT_DOC", "True")

	env := algorithmServiceEnv(cfg, paths, chatProcessName)

	assertEnvContains(t, env, "LAZYLLM_INIT_DOC=False")
}

func TestRAGServicesDoNotWaitBeforeStarting(t *testing.T) {
	manager := NewAlgorithmServiceManager(&fakeRunner{t: t})

	for _, service := range []string{
		processorServerProcessName,
		processorWorkerProcessName,
		algoProcessName,
		docServerProcessName,
	} {
		if err := manager.waitForDependencies(context.Background(), RuntimeConfig{}, service); err != nil {
			t.Fatalf("%s dependencies: %v", service, err)
		}
	}
}

func TestInstallerWarmupDoesNotWaitForExcludedProcessorWorker(t *testing.T) {
	normal := ragReadinessChecks(RuntimeConfig{})
	warmup := ragReadinessChecks(RuntimeConfig{MaintenanceMode: installerWarmupMaintenanceMode})

	if !hasRAGReadinessLabel(normal, "processor-worker") {
		t.Fatal("normal runtime must wait for processor-worker")
	}
	if hasRAGReadinessLabel(warmup, "processor-worker") {
		t.Fatal("installer warmup must not wait for excluded processor-worker")
	}
}

func TestWarmupChatCapabilityDoesNotWaitForProcessorWorker(t *testing.T) {
	t.Setenv(installerWarmupSkipProcessorWorkerEnvVar, "true")
	checks := ragReadinessChecks(RuntimeConfig{})
	if hasRAGReadinessLabel(checks, "processor-worker") {
		t.Fatal("warmup Chat capability must skip processor-worker readiness")
	}
}

func hasRAGReadinessLabel(checks []ragReadinessCheck, label string) bool {
	for _, check := range checks {
		if check.label == label {
			return true
		}
	}
	return false
}

func TestAlgorithmServiceEnvUsesRuntimeDataPaths(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}

	env := algorithmServiceEnv(cfg, paths, chatProcessName)

	assertEnvContains(t, env, "LAZYMIND_SHARED_UPLOAD_DIR="+paths.UploadRoot)
	assertEnvContains(t, env, "LAZYMIND_UPLOAD_DIR="+paths.UploadRoot)
	assertEnvContains(t, env, "LAZYMIND_UPLOAD_ROOT="+paths.UploadRoot)
	assertEnvContains(t, env, "LAZYMIND_HOME="+paths.AlgorithmHome)
	assertEnvContains(t, env, "LAZYLLM_HOME="+paths.LazyLLMHome)
	assertEnvContains(t, env, "TIKTOKEN_CACHE_DIR="+filepath.Join(paths.LazyLLMHome, "tiktoken"))
	assertEnvContains(t, env, "LAZYMIND_DOCUMENT_SERVICE_STORAGE_DIR="+paths.UploadRoot)
	assertEnvContains(t, env, "LAZYLLM_TEMP_DIR="+paths.LazyLLMTempDir)
	assertEnvContains(t, env, "LAZYMIND_OCR_CACHE_DIR="+paths.OCRCacheDir)
	assertEnvContains(t, env, "LAZYMIND_MOUNT_BASE_DIR="+paths.UploadRoot)
	assertEnvContains(t, env, "LAZYLLM_TRACE_LOCAL_STORAGE_DIR="+paths.TracesDir)
	assertEnvContains(t, env, "LAZYMIND_SUBAGENT_WORKSPACE="+paths.SubagentDataDir)
	assertEnvContains(t, env, "LAZYMIND_EVO_BASE_DIR="+paths.EvoDataDir)
	assertEnvNotContains(t, env, filepath.Join(paths.RepoRoot, "data", "core", "uploads"))
	assertEnvNotContains(t, env, filepath.Join(paths.RepoRoot, "data", "traces"))
	assertEnvNotContains(t, env, filepath.Join(paths.RepoRoot, "data", "subagent"))
	assertEnvNotContains(t, env, filepath.Join(paths.RepoRoot, "data", "evo"))
}

func TestEnsureTiktokenCacheWarmsOnceAndWritesMarker(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure runtime dirs: %v", err)
	}
	runner := &fakeRunner{t: t}
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, paths.AlgorithmPython, "-c", "import tiktoken; tiktoken.get_encoding('gpt2')")
		assertEnvContains(t, cmd.Env, "TIKTOKEN_CACHE_DIR="+filepath.Join(paths.LazyLLMHome, "tiktoken"))
		cacheFile := filepath.Join(paths.LazyLLMHome, "tiktoken", "gpt2-cache")
		if err := os.WriteFile(cacheFile, []byte("cached"), 0o644); err != nil {
			t.Fatalf("write fake tiktoken cache: %v", err)
		}
		return CommandResult{}, nil
	})
	manager := NewAlgorithmServiceManager(runner)

	if err := manager.ensureTiktokenCache(context.Background(), paths); err != nil {
		t.Fatalf("first tiktoken warmup: %v", err)
	}
	if err := manager.ensureTiktokenCache(context.Background(), paths); err != nil {
		t.Fatalf("second tiktoken warmup: %v", err)
	}
	runner.assertCommandCount(1)
	if _, err := os.Stat(filepath.Join(paths.PythonStateDir, tiktokenReadyFileName)); err != nil {
		t.Fatalf("tiktoken ready marker: %v", err)
	}
}

func TestAlgorithmServiceEnvUsesFileBackedRelayArgumentsOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific process policy")
	}
	for _, profile := range []string{"local", "desktop"} {
		t.Run(profile, func(t *testing.T) {
			repo := t.TempDir()
			writeComposeFixture(t, repo)
			cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
			if err != nil {
				t.Fatalf("runtime config: %v", err)
			}
			cfg.Profile = profile

			env := algorithmServiceEnv(cfg, paths, chatProcessName)

			assertEnvContains(t, env, "LAZYLLM_PASS_ARGS_BY_FILE=1")
		})
	}
}

func TestAlgorithmServiceCommandArgsUsesWindowsDesktopBootstrap(t *testing.T) {
	for _, profile := range []string{"local", "desktop"} {
		t.Run(profile, func(t *testing.T) {
			cfg := RuntimeConfig{Profile: profile}
			spec := AlgorithmServiceSpec{
				Name:   chatProcessName,
				Module: []string{"-m", "lazymind.chat.app", "--host", "0.0.0.0", "--port", "8092"},
				Port:   8092,
			}

			args := algorithmServiceCommandArgs(cfg, spec)

			want := []string{"-m", "lazymind.chat.app", "--host", "0.0.0.0", "--port", "8092"}
			if runtime.GOOS == "windows" && profile == "desktop" {
				want = append([]string{"-m", "lazymind.windows_runtime", "--"}, want...)
			}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("algorithm service args = %#v, want %#v", args, want)
			}
		})
	}
}
