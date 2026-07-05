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
			assertCommand(t, cmd, "uv", "venv", "--seed", "--python", "python3", paths.AlgorithmVenv)
			if err := os.MkdirAll(filepath.Dir(paths.AlgorithmPython), 0o755); err != nil {
				t.Fatalf("mkdir algorithm venv bin: %v", err)
			}
			if err := os.WriteFile(paths.AlgorithmPython, []byte("python"), 0o755); err != nil {
				t.Fatalf("write algorithm python: %v", err)
			}
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, paths.AlgorithmPython, "-m", "pip", "--version")
			return CommandResult{Stdout: "pip 25.0"}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, paths.AlgorithmPython, "-m", "pip", "install", "--upgrade", "pip")
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, filepath.Join(paths.AlgorithmVenv, "bin", "pip"), "install", "setuptools<81")
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, filepath.Join(paths.AlgorithmVenv, "bin", "pip"), "install", "lazyllm")
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, filepath.Join(paths.AlgorithmVenv, "bin", "lazyllm"), "install", "rag")
			return CommandResult{}, nil
		},
		func(cmd Command) (CommandResult, error) {
			assertCommand(t, cmd, filepath.Join(paths.AlgorithmVenv, "bin", "pip"), "install", "-r", requirements)
			return CommandResult{}, nil
		},
	)

	if err := manager.preparePython(context.Background(), paths, false); err != nil {
		t.Fatalf("prepare algorithm python: %v", err)
	}
	runner.assertCommandCount(7)
}
