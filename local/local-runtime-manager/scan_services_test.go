package main

import (
	"context"
	"testing"
)

func TestScanControlPlaneWaitForDatabaseUsesPsql(t *testing.T) {
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
			"psql",
			"-U", "root",
			"-d", "scan_control_plane",
			"-c", "SELECT 1",
		)
		if cmd.Dir != repo {
			t.Fatalf("unexpected psql dir %q", cmd.Dir)
		}
		return CommandResult{Stdout: " ?column?\n----------\n        1\n"}, nil
	})

	if err := manager.waitForDatabase(context.Background(), cfg, paths); err != nil {
		t.Fatalf("wait database: %v", err)
	}
	runner.assertCommandCount(1)
}
