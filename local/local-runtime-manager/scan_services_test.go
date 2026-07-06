package main

import (
	"context"
	"net"
	"strconv"
	"testing"
)

func TestScanControlPlaneWaitForDatabaseUsesPsql(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	cfg.Algorithm.PostgresPort = port
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
