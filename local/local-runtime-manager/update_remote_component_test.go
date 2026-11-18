package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestUpdateStatePreservesRemoteComponentEvidenceAcrossRestart(t *testing.T) {
	for _, damage := range []string{"none", "ready", "manifest", "receipt", "missing-receipt", "missing-manifest", "missing-catalog", "never-installed"} {
		t.Run(damage, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			write := func(file string, value any) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(file, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			pinned := pythonComponent{ID: "rag", Revision: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64), BaseFingerprint: strings.Repeat("c", 64), Tag: strings.Repeat("d", 64), Platform: runtime.GOOS, Arch: runtime.GOARCH, PythonABI: "cp311", Packages: map[string]string{"fixture": "1.0"}, CatalogURL: "https://fixture.invalid/components.json"}
			catalogPath := filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json")
			write(catalogPath, pythonComponentCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]pythonComponent{"rag": pinned}})
			selected := pinned
			selected.Revision = strings.Repeat("e", 64)
			selected.SHA256 = strings.Repeat("f", 64)
			root := filepath.Join(f.paths.RuntimeRoot, "deps", "python-components", "rag", selected.Revision)
			receipt := filepath.Join(filepath.Dir(root), "catalog-"+pinned.Tag+".json")
			if damage != "never-installed" {
				if err := os.MkdirAll(filepath.Join(root, "site-packages"), 0700); err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(root, "bundle-manifest.json"), selected)
				write(receipt, selected)
				if err := os.WriteFile(filepath.Join(root, ".ready"), []byte(selected.SHA256), 0600); err != nil {
					t.Fatal(err)
				}
			}
			restart := func() RuntimeConfig {
				t.Helper()
				cfg, _, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", OwnerToken: f.cfg.OwnerToken, RepoRoot: f.cfg.RepoRoot, ResourcesRoot: f.paths.ResourcesRoot, RuntimeRoot: f.paths.RuntimeRoot}, true)
				if err != nil {
					t.Fatal(err)
				}
				state := defaultRuntimeState(cfg, cfg.ProcessComposePort, f.paths.RunDirTokenFile)
				state.OverallStatus = "ready"
				if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
					t.Fatal(err)
				}
				return cfg
			}
			cfg := restart()
			before, err := measureDesktopUpdateState(context.Background(), cfg, f.paths)
			if err != nil || before.DataProfile == "" {
				t.Fatalf("valid starting state rejected: %+v %v", before, err)
			}
			if damage == "never-installed" {
				if before.Components["rag"] != "" || !cfg.Algorithm.RAGDisabled {
					t.Fatal("never installed component became active")
				}
				return
			}
			if before.Components["rag"] != selected.Revision || cfg.Algorithm.RAGDisabled {
				t.Fatalf("actual remote revision not selected: %+v", before)
			}
			if damage == "none" {
				return
			}
			switch damage {
			case "ready":
				_ = os.WriteFile(filepath.Join(root, ".ready"), []byte("corrupt"), 0600)
			case "manifest":
				broken := selected
				broken.BaseFingerprint = strings.Repeat("1", 64)
				write(filepath.Join(root, "bundle-manifest.json"), broken)
			case "receipt":
				_ = os.WriteFile(receipt, []byte("{broken"), 0600)
			case "missing-receipt":
				_ = os.Remove(receipt)
			case "missing-manifest":
				_ = os.Remove(filepath.Join(root, "bundle-manifest.json"))
			case "missing-catalog":
				_ = os.Remove(catalogPath)
			}
			cfg = restart() // The regular startup loader may now silently disable RAG.
			got, err := measureDesktopUpdateState(context.Background(), cfg, f.paths)
			if desktopupdate.ErrorCode(err) != "component_state_unknown" || got.DataProfile != "" {
				t.Fatalf("lost installed evidence after %s: %+v %v", damage, got, err)
			}
		})
	}
}
