package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAlgorithmPreparePythonPinsSetuptoolsForLocalVenv(t *testing.T) {
	t.Setenv("UV", "uv")
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

	runner := &fakeRunner{t: t}
	manager := NewAlgorithmServiceManager(runner)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
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
			assertCommand(t, cmd, "uv", "pip", "install", "--python", paths.AlgorithmPython, "--link-mode", "copy", "--strict", "lazyllm")
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, filepath.Join(paths.AlgorithmVenv, "bin", "lazyllm"), "install", "rag")
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "pip", "install", "--python", paths.AlgorithmPython, "--link-mode", "copy", "--strict", "-r", requirements)
			return CommandResult{}, nil
		},
	)

	if err := manager.preparePython(context.Background(), paths, false); err != nil {
		t.Fatalf("prepare algorithm python: %v", err)
	}
	runner.assertCommandCount(7)
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
