package main

import (
	"context"
	"testing"
)

func TestScanControlPlaneWaitForDatabaseUsesPgIsReady(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner := &fakeRunner{t: t}
	manager := NewScanControlPlaneManager(runner)
	runner.handlers = append(runner.handlers, func(cmd Command) (CommandResult, error) {
		assertCommand(t, cmd, "docker",
			"compose",
			"-f", repoComposeFileName,
			"-f", localComposeOverrideName,
			"exec",
			"-T",
			"db",
			"pg_isready",
			"-U", "root",
			"-d", "scan_control_plane",
		)
		if cmd.Dir != repo {
			t.Fatalf("unexpected pg_isready dir %q", cmd.Dir)
		}
		return CommandResult{Stdout: "db:5432 - accepting connections\n"}, nil
	})

	if err := manager.waitForDatabase(context.Background(), cfg, paths); err != nil {
		t.Fatalf("wait database: %v", err)
	}
	runner.assertCommandCount(1)
}
