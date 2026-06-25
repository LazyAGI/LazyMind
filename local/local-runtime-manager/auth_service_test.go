package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAuthServicePreparePythonEnvUsesUV(t *testing.T) {
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
			assertCommand(t, cmd, "uv", "venv", "--python", cfg.AuthService.Python, paths.AuthServiceVenvDir)
			if err := os.MkdirAll(filepath.Dir(authServicePythonPath(paths)), 0o755); err != nil {
				t.Fatalf("mkdir venv bin: %v", err)
			}
			if err := os.WriteFile(authServicePythonPath(paths), []byte("python"), 0o755); err != nil {
				t.Fatalf("write venv python: %v", err)
			}
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, "uv", "pip", "install", "--python", authServicePythonPath(paths), "-r", requirements)
			return CommandResult{}, nil
		},
	)

	if err := manager.preparePythonEnv(context.Background(), cfg, paths); err != nil {
		t.Fatalf("prepare python env: %v", err)
	}
	runner.assertCommandCount(2)
}

func TestAuthServiceInstallRequirementsFallsBackToPip(t *testing.T) {
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
			assertCommand(t, cmd, "uv", "pip", "install", "--python", python, "-r", requirements)
			return CommandResult{Stderr: "uv not found"}, os.ErrNotExist
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, python, "-m", "pip", "install", "-r", requirements)
			return CommandResult{}, nil
		},
	)

	if err := manager.installRequirements(context.Background(), paths, python, requirements); err != nil {
		t.Fatalf("install requirements: %v", err)
	}
	runner.assertCommandCount(2)
}
