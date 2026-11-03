package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRuntimeCustomMilvusScopeRegistersAndBlocksPendingUpdates(t *testing.T) {
	for _, direction := range []string{"registered-scope", "pending-transaction"} {
		t.Run(direction, func(t *testing.T) {
			shared := t.TempDir()
			t.Setenv(localMilvusLiteDataDirEnvVar, shared)
			first, second := newUpdateStateFixture(t), newUpdateStateFixture(t)
			first.cfg.OwnerToken = "first-owner"
			second.cfg.OwnerToken = "second-owner"
			if first.cfg.ModeProfile.VectorStore.DBPath != shared || second.cfg.ModeProfile.VectorStore.DBPath != shared {
				t.Fatal("custom storage was not selected")
			}
			root := t.TempDir()
			if direction == "registered-scope" {
				out, err := admissionCommand(t, first, root, "coordinator").Output()
				if err != nil {
					t.Fatalf("normal source guard: %s %v", out, err)
				}
				requireRegisteredDataRoot(t, root, shared)
			} else {
				writePendingAdmissionJournal(t, root, admissionPlan(t, first, root))
				out, err := admissionCommand(t, second, root, "coordinator").Output()
				var result struct {
					Error string `json:"error"`
				}
				_ = json.Unmarshal(out, &result)
				if err == nil || result.Error != "update_in_progress" {
					t.Fatalf("shared Milvus writer bypassed transaction: %s %v", out, err)
				}
			}
		})
	}
}

func TestRuntimeExternalStorageCanStartButCannotAutoUpdate(t *testing.T) {
	for _, kind := range []string{"postgres-auth", "opensearch"} {
		t.Run(kind, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			if kind == "postgres-auth" {
				t.Setenv(authServiceDatabaseURLEnvVar, "postgresql://fixture.invalid/example")
			} else {
				t.Setenv("LAZYMIND_SEGMENT_STORE_TYPE", "opensearch")
				t.Setenv("LAZYMIND_SEGMENT_STORE_URI_OR_PATH", "https://search.invalid/index")
			}
			out, err := admissionCommand(t, f, t.TempDir(), "coordinator").Output()
			var result struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(out, &result)
			if err != nil || result.Status != "allowed" {
				t.Fatalf("existing non-update startup rejected: %s %v", out, err)
			}
			cfg, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", OwnerToken: f.cfg.OwnerToken, RepoRoot: f.cfg.RepoRoot, ResourcesRoot: f.paths.ResourcesRoot, RuntimeRoot: f.paths.RuntimeRoot}, true)
			if err != nil {
				t.Fatal(err)
			}
			state := defaultRuntimeState(cfg, cfg.ProcessComposePort, paths.RunDirTokenFile)
			state.OverallStatus = "ready"
			if err = writeRuntimeState(paths.StateFile, state); err != nil {
				t.Fatal(err)
			}
			measured, err := measureDesktopUpdateState(context.Background(), cfg, paths)
			if err == nil || measured.DataProfile != "" {
				t.Fatal("unsupported external recovery layout became auto-update compatible")
			}
		})
	}
}
