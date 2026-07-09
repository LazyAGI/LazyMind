package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthServicePreparePythonEnvUsesUV(t *testing.T) {
	t.Setenv("UV", "uv")
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	requirements := filepath.Join(repo, authServiceSourceDirName, "requirements.txt")
	if err := os.MkdirAll(filepath.Dir(requirements), 0o755); err != nil {
		t.Fatalf("mkdir requirements dir: %v", err)
	}
	if err := os.WriteFile(requirements, []byte("fastapi\n"), 0o644); err != nil {
		t.Fatalf("write requirements: %v", err)
	}

	runner := &fakeRunner{t: t}
	manager := NewAuthServiceManager(runner)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner.handlers = append(runner.handlers,
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "python", "install", "--install-dir", paths.PythonRuntimeDir, cfg.AuthService.PythonVersion)
			assertEnvContains(t, cmd.Env, "UV_PYTHON_INSTALL_DIR="+paths.PythonRuntimeDir)
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "python", "find", "--managed-python", "--no-python-downloads", "--resolve-links", cfg.AuthService.PythonVersion)
			assertEnvContains(t, cmd.Env, "UV_PYTHON_INSTALL_DIR="+paths.PythonRuntimeDir)
			return CommandResult{Stdout: filepath.Join(paths.PythonRuntimeDir, "cpython-3.11.15", "bin", "python3.11") + "\n"}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "venv", "--managed-python", "--no-python-downloads", "--relocatable", "--seed", "--link-mode", "copy", "--python", filepath.Join(paths.PythonRuntimeDir, "cpython-3.11.15", "bin", "python3.11"), paths.AuthServiceVenvDir)
			if err := os.MkdirAll(filepath.Dir(authServicePythonPath(paths)), 0o755); err != nil {
				t.Fatalf("mkdir venv bin: %v", err)
			}
			if err := os.WriteFile(authServicePythonPath(paths), []byte("python"), 0o755); err != nil {
				t.Fatalf("write venv python: %v", err)
			}
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "pip", "install", "--python", authServicePythonPath(paths), "--link-mode", "copy", "--strict", "-r", requirements)
			return CommandResult{}, nil
		},
	)

	if err := manager.preparePythonEnv(context.Background(), cfg, paths); err != nil {
		t.Fatalf("prepare python env: %v", err)
	}
	runner.assertCommandCount(4)
}

func TestAuthServiceInstallRequirementsUsesUVOnly(t *testing.T) {
	t.Setenv("UV", "uv")
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	requirements := filepath.Join(repo, authServiceSourceDirName, "requirements.txt")
	if err := os.MkdirAll(filepath.Dir(requirements), 0o755); err != nil {
		t.Fatalf("mkdir requirements dir: %v", err)
	}
	if err := os.WriteFile(requirements, []byte("fastapi\n"), 0o644); err != nil {
		t.Fatalf("write requirements: %v", err)
	}

	runner := &fakeRunner{t: t}
	manager := NewAuthServiceManager(runner)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	python := authServicePythonPath(paths)
	runner.handlers = append(runner.handlers,
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "pip", "install", "--python", python, "--link-mode", "copy", "--strict", "-r", requirements)
			assertEnvContains(t, cmd.Env, "UV_PYTHON_INSTALL_DIR="+paths.PythonRuntimeDir)
			for _, item := range cmd.Env {
				key, value, ok := strings.Cut(item, "=")
				if !ok {
					continue
				}
				switch key {
				case "HOME", "XDG_CACHE_HOME", "UV_CACHE_DIR", "PIP_CACHE_DIR":
					if pathIsUnderRoot(value, paths.RuntimeRoot) {
						t.Fatalf("%s = %q is under runtime root %q", key, value, paths.RuntimeRoot)
					}
					if pathIsUnderRoot(value, paths.BuildRoot) {
						t.Fatalf("%s = %q is under build root %q", key, value, paths.BuildRoot)
					}
				}
			}
			return CommandResult{}, nil
		},
	)

	if err := manager.installRequirements(context.Background(), paths, python, requirements); err != nil {
		t.Fatalf("install requirements: %v", err)
	}
	runner.assertCommandCount(1)
}
