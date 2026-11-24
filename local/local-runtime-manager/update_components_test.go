package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestUpdateV2TargetCatalogBindsExternalCluster(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"dual", ""},
		{"invalid-cluster", "component_update_incompatible"},
		{"unknown-component", "component_update_incompatible"},
		{"modified-after-signing", "artifact_hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rag := desktopupdate.ComponentDescriptor{
				ID: "rag", Platform: runtime.GOOS, Arch: runtime.GOARCH, PythonABI: "cp311",
				Revision: strings.Repeat("a", 64), Tag: strings.Repeat("b", 64),
				SHA256: strings.Repeat("c", 64), BaseFingerprint: strings.Repeat("d", 64),
				SizeBytes: 10, UnpackedBytes: 100, Filename: "rag.zip", Packages: map[string]string{"pymilvus": "2.0"},
				URL: "https://fixture.invalid/rag.zip",
			}
			cluster := rag
			cluster.ID, cluster.Filename, cluster.URL = "cluster", "cluster.zip", "https://fixture.invalid/cluster.zip"
			cluster.Revision, cluster.Tag, cluster.SHA256 = strings.Repeat("e", 64), strings.Repeat("f", 64), strings.Repeat("1", 64)
			cluster.Packages = map[string]string{"umap-learn": "0.5.7"}
			if tc.name == "invalid-cluster" {
				cluster.PythonABI = ""
			}
			if tc.name == "unknown-component" {
				cluster.ID = "unknown"
			}
			raw, err := json.Marshal(map[string]any{
				"schemaVersion": 1, "platform": runtime.GOOS, "arch": runtime.GOARCH,
				"components": map[string]desktopupdate.ComponentDescriptor{"rag": rag, cluster.ID: cluster},
			})
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(raw)
			if tc.name == "modified-after-signing" {
				raw = bytes.Replace(raw, []byte("0.5.7"), []byte("0.5.8"), 1)
			}
			filename := filepath.Join(t.TempDir(), "components.json")
			if err := os.WriteFile(filename, raw, 0600); err != nil {
				t.Fatal(err)
			}
			artifact := desktopupdate.Artifact{Size: int64(len(raw)), SHA256: hex.EncodeToString(hash[:])}
			got, _, err := readTargetCatalog(t.Context(), filename, artifact)
			if tc.want == "" {
				if err != nil || !bytes.Equal(got, raw) {
					t.Fatalf("valid signed dual-component catalog rejected: %v", err)
				}
			} else if desktopupdate.ErrorCode(err) != tc.want {
				t.Fatalf("target check error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestUpdateComponentPreflightCLI(t *testing.T) {
	for _, scenario := range []string{"same", "same-tag-new-pin", "new-tag", "new-tag-installed", "changed-base", "changed-abi", "changed-packages", "changed-url", "missing-component", "never-installed", "bad-hash", "bad-size", "invalid-json", "directory", "dirty", "wrong-owner"} {
		t.Run(scenario, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			writeJSON := func(file string, value any) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(file, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			a := pythonComponent{ID: "rag", Revision: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64),
				BaseFingerprint: strings.Repeat("c", 64), Platform: runtime.GOOS, Arch: runtime.GOARCH,
				PythonABI: "cp311", Packages: map[string]string{"fixture": "1"}, Tag: strings.Repeat("d", 64), CatalogURL: "https://fixture.invalid/tag-a/catalog.json"}
			catalog := func(entry pythonComponent) pythonComponentCatalog {
				return pythonComponentCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]pythonComponent{"rag": entry}}
			}
			install := func(entry pythonComponent) {
				t.Helper()
				dir := filepath.Join(f.paths.RuntimeRoot, "deps", "python-components", "rag", entry.Revision)
				if err := os.MkdirAll(filepath.Join(dir, "site-packages"), 0700); err != nil {
					t.Fatal(err)
				}
				writeJSON(filepath.Join(dir, "bundle-manifest.json"), entry)
				if err := os.WriteFile(filepath.Join(dir, ".ready"), []byte(entry.SHA256), 0600); err != nil {
					t.Fatal(err)
				}
				writeJSON(filepath.Join(filepath.Dir(dir), "catalog-"+entry.Tag+".json"), entry)
			}
			writeJSON(filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json"), catalog(a))
			if scenario != "never-installed" {
				install(a)
				state, err := readRuntimeState(f.paths.StateFile)
				if err != nil {
					t.Fatal(err)
				}
				state.Config.Algorithm.RAGDisabled = false
				if err = writeRuntimeState(f.paths.StateFile, state); err != nil {
					t.Fatal(err)
				}
			}
			profile := f.measure(t).DataProfile
			target := a
			want := "component_update_incompatible"
			switch scenario {
			case "same":
				want = ""
			case "same-tag-new-pin", "never-installed":
				target.Revision, target.SHA256 = strings.Repeat("e", 64), strings.Repeat("f", 64)
				want = ""
			case "new-tag", "new-tag-installed":
				target.Revision, target.SHA256, target.Tag = strings.Repeat("e", 64), strings.Repeat("f", 64), strings.Repeat("1", 64)
				target.CatalogURL = "https://fixture.invalid/tag-b/catalog.json"
				if scenario == "new-tag-installed" {
					install(target)
				}
			case "changed-base":
				target.BaseFingerprint = strings.Repeat("2", 64)
			case "changed-abi":
				target.PythonABI = "cp999"
			case "changed-packages":
				target.Packages = map[string]string{"fixture": "2"}
			case "changed-url":
				target.CatalogURL = "https://fixture.invalid/other/catalog.json"
			case "dirty":
				if _, err := f.databases["core"].Exec("UPDATE schema_migrations SET dirty=1"); err != nil {
					t.Fatal(err)
				}
				want = "data_dirty"
			case "wrong-owner":
				want = "runtime_owner_mismatch"
			case "bad-hash":
				want = "artifact_hash"
			case "bad-size":
				want = "artifact_size"
			case "directory":
				want = "component_update_incompatible"
			}
			value := catalog(target)
			if scenario == "missing-component" {
				value.Components = map[string]pythonComponent{}
			}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "invalid-json" {
				data = []byte("{broken")
			}
			file := filepath.Join(t.TempDir(), "components.json")
			if scenario == "directory" {
				err = os.Mkdir(file, 0700)
			} else {
				err = os.WriteFile(file, data, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(data)
			hash, size := hex.EncodeToString(digest[:]), len(data)
			if scenario == "bad-hash" {
				hash = strings.Repeat("0", 64)
			}
			if scenario == "bad-size" {
				size++
			}
			owner := f.cfg.OwnerToken
			if scenario == "wrong-owner" {
				owner = "other-fixture-owner"
			}
			before, _ := os.ReadFile(f.paths.StateFile)
			var out bytes.Buffer
			runner := &fakeRunner{t: t}
			cli := NewCLI(&out, io.Discard, runner, filepath.Join(t.TempDir(), "manager"))
			err = cli.Run(context.Background(), []string{"update-components-check", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot,
				"--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot, "--owner-token", owner,
				"--components", file, "--sha256", hash, "--size", strconv.Itoa(size)})
			if err != nil {
				t.Fatalf("preflight must return a bounded decision: %v", err)
			}
			var result struct{ Status, Reason, DataProfile string }
			if err = json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if want == "" {
				if result.Status != "compatible" || result.DataProfile != profile {
					t.Fatalf("safe update rejected: %s", out.String())
				}
			} else if result.Status != "blocked" || result.Reason != want || result.DataProfile != "" {
				t.Fatalf("unsafe update not rejected as %s: %s", want, out.String())
			}
			runner.assertCommandCount(0)
			after, _ := os.ReadFile(f.paths.StateFile)
			if !bytes.Equal(before, after) || strings.Contains(out.String(), f.paths.RuntimeRoot) || strings.Contains(out.String(), owner) {
				t.Fatal("preflight mutated state or exposed local details")
			}
		})
	}
}

func TestV1UpdateMeasuresAndChecksOptionalComponents(t *testing.T) {
	for _, tc := range []struct {
		name         string
		installed    []string
		legacySource bool
		blocked      bool
	}{
		{name: "older-rag-catalog-to-dual", legacySource: true},
		{name: "neither-installed"},
		{name: "rag-only", installed: []string{"rag"}},
		{name: "cluster-only", installed: []string{"cluster"}},
		{name: "both-installed", installed: []string{"rag", "cluster"}},
		{name: "target-removes-installed-cluster", installed: []string{"rag", "cluster"}, blocked: true},
		{name: "target-changes-installed-cluster-abi", installed: []string{"cluster"}, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			if f.cfg.UpdateStorage.Version != 1 {
				t.Fatal("expected a desktop without the v2 format recipe")
			}
			writeJSON := func(filename string, value any) []byte {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filename, raw, 0600); err != nil {
					t.Fatal(err)
				}
				return raw
			}
			rag := pythonComponent{ID: "rag", Revision: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64), Tag: strings.Repeat("c", 64),
				BaseFingerprint: strings.Repeat("d", 64), Platform: runtime.GOOS, Arch: runtime.GOARCH, PythonABI: "cp311",
				Packages: map[string]string{"pymilvus": "2.6.0"}, CatalogURL: "https://fixture.invalid/rag/catalog.json"}
			cluster := rag
			cluster.ID, cluster.Revision, cluster.SHA256 = "cluster", strings.Repeat("e", 64), strings.Repeat("f", 64)
			cluster.CatalogURL, cluster.Packages = "https://fixture.invalid/cluster/catalog.json", map[string]string{"umap-learn": "0.5.7"}
			all := map[string]pythonComponent{"rag": rag, "cluster": cluster}
			source := pythonComponentCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]pythonComponent{"rag": rag}}
			sourcePath := filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json")
			writeJSON(sourcePath, source)
			want := map[string]string{}
			for _, id := range tc.installed {
				selected := all[id]
				// The runtime uses the selected same-tag revision, which can differ from the packaged pin.
				selected.Revision = hexDigest([]byte(id + "-selected"))
				selected.SHA256 = hexDigest([]byte(id + "-payload"))
				want[id] = selected.Revision
				dir := filepath.Join(f.paths.RuntimeRoot, "deps", "python-components", id, selected.Revision)
				if err := os.MkdirAll(filepath.Join(dir, "site-packages"), 0700); err != nil {
					t.Fatal(err)
				}
				writeJSON(filepath.Join(dir, "bundle-manifest.json"), selected)
				writeJSON(filepath.Join(filepath.Dir(dir), "catalog-"+selected.Tag+".json"), selected)
				if err := os.WriteFile(filepath.Join(dir, ".ready"), []byte(selected.SHA256), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var previousProfile string
			if _, installedCluster := want["cluster"]; !installedCluster {
				previousProfile = f.measure(t).DataProfile
			}
			if !tc.legacySource {
				source.Components = all
				writeJSON(sourcePath, source)
			}
			measured, err := measureDesktopUpdateState(t.Context(), f.cfg, f.paths)
			if err != nil || measured.Dirty || !strings.HasPrefix(measured.DataProfile, "sqlite-v1-") || measured.Components == nil || !maps.Equal(measured.Components, want) {
				t.Errorf("v1 measurement = %+v, %v; want installed selections %v", measured, err, want)
			}
			if previousProfile != "" && measured.DataProfile != previousProfile {
				t.Errorf("adding an uninstalled catalog entry changed the previous v1 data profile")
			}
			target := source
			target.Components = maps.Clone(all)
			switch tc.name {
			case "target-removes-installed-cluster":
				delete(target.Components, "cluster")
			case "target-changes-installed-cluster-abi":
				cluster.PythonABI = "cp312"
				target.Components["cluster"] = cluster
			}
			file := filepath.Join(t.TempDir(), "target-components.json")
			raw := writeJSON(file, target)
			var out bytes.Buffer
			runner := &fakeRunner{t: t}
			cli := NewCLI(&out, io.Discard, runner, "fixture-manager")
			if err := cli.Run(t.Context(), []string{"update-components-check", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot,
				"--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot, "--owner-token", f.cfg.OwnerToken,
				"--components", file, "--sha256", hexDigest(raw), "--size", strconv.Itoa(len(raw))}); err != nil {
				t.Fatal(err)
			}
			var result struct{ Status, Reason, DataProfile string }
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if tc.blocked {
				if result.Status != "blocked" || result.Reason != "component_update_incompatible" || result.DataProfile != "" {
					t.Errorf("incompatible target returned %s", out.String())
				}
			} else if result.Status != "compatible" || result.DataProfile == "" || result.DataProfile != measured.DataProfile {
				t.Errorf("compatible target returned %s, want measured source profile %q", out.String(), measured.DataProfile)
			}
			runner.assertCommandCount(0)
		})
	}
}
