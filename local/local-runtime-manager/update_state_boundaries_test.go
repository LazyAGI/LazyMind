package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateStorageCaptureMatchesServiceEnvironmentOverrides(t *testing.T) {
	f := newUpdateStateFixture(t)
	override := filepath.Join(t.TempDir(), "override.db")
	t.Setenv("LAZYMIND_STATE_SQLITE_PATH", override)
	captured := captureUpdateStorage(f.paths, f.cfg.AuthService.DatabaseURL)
	if captured.Databases["auth-state"] != override || captured.Databases["core-state"] != override {
		t.Fatal("state override ignored")
	}
	if captured.Databases["scan-state"] != filepath.Join(f.paths.ScanControlPlaneStateDir, "scan_state.db") {
		t.Fatal("scan explicit service override lost")
	}
	f.cfg.UpdateStorage = captured
	stored := snapshotRuntimeConfig(f.cfg)
	captured.Databases["core"] = "mutated"
	if stored.UpdateStorage.Databases["core"] == "mutated" {
		t.Fatal("snapshot aliases mutable configuration")
	}
}

func TestUpdateStateRejectsPartialRuntimeIdentityAndStartup(t *testing.T) {
	for _, field := range []string{"profile", "repo", "resources", "starting", "warmup"} {
		t.Run(field, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			state, err := readRuntimeState(f.paths.StateFile)
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "profile":
				state.Profile = ""
			case "repo":
				state.RepoRoot = ""
			case "resources":
				state.ResourcesRoot = ""
			case "starting":
				state.OverallStatus = "starting"
			case "warmup":
				state.Config.MaintenanceMode = installerWarmupMaintenanceMode
			}
			if err = writeRuntimeState(f.paths.StateFile, state); err != nil {
				t.Fatal(err)
			}
			got, err := measureDesktopUpdateState(context.Background(), f.cfg, f.paths)
			if err == nil || got.DataProfile != "" {
				t.Fatal("partial runtime accepted")
			}
		})
	}
}

func TestUpdateStateAllowsEmptySegmentsButNotMissingSegments(t *testing.T) {
	f := newUpdateStateFixture(t)
	if _, err := f.databases["segments"].Exec("DROP TABLE fixture_records"); err != nil {
		t.Fatal(err)
	}
	f.measure(t) // SQLite server creates this database even before its first use.
	_ = f.databases["segments"].Close()
	if err := os.Remove(localSegmentStoreBackingPath(f.paths)); err != nil {
		t.Fatal(err)
	}
	if state, err := measureDesktopUpdateState(context.Background(), f.cfg, f.paths); err == nil || state.DataProfile != "" {
		t.Fatal("missing segments accepted")
	}
}
