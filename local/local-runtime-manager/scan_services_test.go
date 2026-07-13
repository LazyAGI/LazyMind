package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanControlPlaneWaitForDatabaseRejectsPostgresInLocalMode(t *testing.T) {
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_DB_DRIVER", "postgres")
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	runner := &fakeRunner{t: t}
	manager := NewScanControlPlaneManager(runner)

	err = manager.waitForDatabase(context.Background(), cfg, paths)
	if err == nil || !strings.Contains(err.Error(), "supports sqlite only") {
		t.Fatalf("expected sqlite-only error, got %v", err)
	}
	runner.assertCommandCount(0)
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

func TestFileWatcherEnvUsesNativeStagingRuntimeRoot(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatalf("runtime config: %v", err)
	}
	env := fileWatcherEnv(cfg, paths)

	assertEnvContains(t, env, "LAZYMIND_FILE_WATCHER_STAGING_RUNTIME_ROOT="+filepath.Join(paths.FileWatcherBaseRoot, "staging"))
}
