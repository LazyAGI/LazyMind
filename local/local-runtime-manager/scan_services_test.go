package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestScanControlPlaneWaitForDatabaseUsesPsql(t *testing.T) {
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_DB_DRIVER", "postgres")
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

func TestScanControlPlaneWaitForDatabasePreparesSQLiteDirs(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner := &fakeRunner{t: t}
	manager := NewScanControlPlaneManager(runner)

	if err := manager.waitForDatabase(context.Background(), cfg, paths); err != nil {
		t.Fatalf("wait database: %v", err)
	}
	runner.assertCommandCount(0)
	for _, dir := range []string{filepath.Dir(paths.ScanDBPath), paths.ScanControlPlaneStateDir} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("expected sqlite dir %s: %v", dir, err)
		}
	}
}

func TestScanControlPlaneEnvUsesSQLite(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	env := scanControlPlaneEnv(cfg, paths)

	assertEnvContains(t, env, "LAZYMIND_SCAN_CONTROL_PLANE_DB_DRIVER=sqlite")
	assertEnvContains(t, env, "LAZYMIND_SCAN_CONTROL_PLANE_DB_DSN="+paths.ScanDBPath)
	assertEnvContains(t, env, "LAZYMIND_STATE_BACKEND=sqlite")
	assertEnvContains(t, env, "LAZYMIND_STATE_SQLITE_PATH="+filepath.Join(paths.ScanControlPlaneStateDir, "scan_state.db"))
}
