package main

import (
	"context"
	"errors"
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

func TestAuthServiceGenerateAPIPermissionsUsesRuntimeOutput(t *testing.T) {
	repo := t.TempDir()
	t.Setenv(runtimeRootEnvVar, filepath.Join(repo, "runtime"))
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure runtime dirs: %v", err)
	}

	script := filepath.Join(repo, "backend", "scripts", "extract_api_permissions.py")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatalf("mkdir scripts dir: %v", err)
	}
	if err := os.WriteFile(script, []byte("# fixture\n"), 0o644); err != nil {
		t.Fatalf("write permission extractor: %v", err)
	}

	output := authServicePermissionsPath(paths)
	runner := &fakeRunner{t: t}
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd,
			authServicePythonPath(paths),
			script,
			"--output", output,
			"--exclude", "scripts,core,vendor",
			filepath.Join(repo, "backend", "core"),
			filepath.Join(repo, "backend", "auth-service"),
			filepath.Join(repo, "backend", "scan-control-plane"),
		)
		if err := os.WriteFile(output, []byte("[]\n"), 0o600); err != nil {
			t.Fatalf("write generated permissions: %v", err)
		}
		return CommandResult{}, nil
	})

	manager := NewAuthServiceManager(runner)
	if err := manager.generateAPIPermissions(context.Background(), paths); err != nil {
		t.Fatalf("generate API permissions: %v", err)
	}
	runner.assertCommandCount(1)
	assertEnvContains(t, authServiceEnv(RuntimeConfig{}, paths), authServicePermissionsEnvVar+"="+output)
}

func TestAuthServiceGenerateAPIPermissionsPreservesOutputStatError(t *testing.T) {
	repo := t.TempDir()
	t.Setenv(runtimeRootEnvVar, filepath.Join(repo, "runtime"))
	writeComposeFixture(t, repo)
	_, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatalf("ensure runtime dirs: %v", err)
	}

	runner := &fakeRunner{t: t}
	runner.handlers = append(runner.handlers, func(Command) (CommandResult, error) {
		return CommandResult{}, nil
	})

	manager := NewAuthServiceManager(runner)
	err = manager.generateAPIPermissions(context.Background(), paths)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generate API permissions error = %v, want wrapped os.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), "output file error") {
		t.Fatalf("generate API permissions error = %q, want output file context", err)
	}
}
