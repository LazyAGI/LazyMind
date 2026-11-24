package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// Only the native code-signing provider is substituted. The decision, signed
// envelope, cache, source preflight, install root and target startup are real.
func TestV1ComponentSelectionAcrossNativeHandoff(t *testing.T) {
	helper := []byte("#!/bin/sh\n[ \"$1\" = verify-identity ] || exit 2\ncat \"$0.json\"\n")
	if runtime.GOOS == "windows" {
		dir := t.TempDir()
		code := `package main
import "os"
func main(){ if len(os.Args)!=2 || os.Args[1]!="verify-identity" {os.Exit(2)}; f,_:=os.Executable(); b,e:=os.ReadFile(f+".json"); if e!=nil {panic(e)}; os.Stdout.Write(b) }`
		filename := filepath.Join(dir, "identity.go")
		if err := os.WriteFile(filename, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(dir, "identity.exe")
		if out, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, filename).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		var err error
		helper, err = os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, scenario := range []string{"authorized", "manual", "invalid-cache-at-source", "revoked-before-handoff", "wrong-release", "changed-target-catalog", "changed-selection", "different-installation", "different-runtime", "revoked-after-handoff"} {
		t.Run(scenario, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			root := t.TempDir()
			old := resolveRuntimeRecoveryRoot
			resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
			t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
			write := func(file string, data []byte, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, data, mode); err != nil {
					t.Fatal(err)
				}
			}
			encode := func(value any) []byte {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			installBootstrapFixtureRAG(t, f)
			catalog, err := loadPythonComponentCatalogForUpdate(f.paths)
			if err != nil {
				t.Fatal(err)
			}
			selected := catalog.Components["rag"]
			selected.ID = "cluster"
			selected.Packages = map[string]string{"umap-learn": "0.5.7"}
			selected.Tag, selected.Revision = hexDigest([]byte("cluster-tag")), hexDigest([]byte("selected-cluster"))
			selected.SHA256 = hexDigest([]byte("selected archive"))
			install := func(entry pythonComponent) {
				dir := filepath.Join(f.paths.RuntimeRoot, "deps", "python-components", entry.ID)
				write(filepath.Join(dir, "catalog-"+entry.Tag+".json"), encode(entry), 0600)
				write(filepath.Join(dir, entry.Revision, ".ready"), []byte(entry.SHA256+"\n"), 0600)
				write(filepath.Join(dir, entry.Revision, "bundle-manifest.json"), encode(entry), 0600)
				write(filepath.Join(dir, entry.Revision, "site-packages", "kept.py"), []byte("fixture"), 0600)
			}
			install(selected)
			pin := selected
			pin.Revision, pin.SHA256 = hexDigest([]byte("packaged cluster")), hexDigest([]byte("archived pin"))
			catalog.Components["cluster"] = pin
			currentCatalog := filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json")
			write(currentCatalog, encode(catalog), 0600)
			for _, id := range []string{"rag", "cluster"} {
				write(filepath.Join(f.paths.StateDir, id+"-component-app-version"), []byte("1.0.0\n"), 0600)
			}
			t.Setenv(desktopAppVersionEnvVar, "1.0.0")
			state := bootstrapPolicyLocalState(f.measure(t))
			if state.Components["cluster"] != selected.Revision || state.Components["rag"] == "" {
				t.Fatal("source must use both installed selections")
			}

			objects := map[string][]byte{"/target.zip": []byte("verified native target"), "/feed": []byte("feed"), "/source.zip": []byte("source package")}
			// A distinct packaged pin is compatible when its exact source selection is still valid.
			pin.Revision, pin.SHA256 = hexDigest([]byte("new cluster pin")), hexDigest([]byte("new catalog archive"))
			catalog.Components["cluster"] = pin
			objects["/components.json"] = encode(catalog)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				data, ok := objects[req.URL.Path]
				if !ok {
					http.NotFound(w, req)
					return
				}
				_, _ = w.Write(data)
			}))
			defer server.Close()
			pub, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			platform := runtime.GOOS
			if platform == "linux" {
				platform = "darwin"
			} // Emulate a packaged macOS identity provider on Linux.
			identity := update.Config{TrustDomain: "v1-fixture", Audience: "official", Channel: "stable", Mode: "official", Platform: platform, Arch: runtime.GOARCH, UpdaterProtocol: 1,
				SigningIdentity: "fixture-signer", ReleaseID: "source", Version: "1.0.0", PolicyURL: server.URL + "/policy.json", AllowedOrigins: []string{server.URL},
				PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(pub)}}
			artifact := func(key string) update.Artifact {
				return update.Artifact{URL: server.URL + key, SHA256: hexDigest(objects[key]), Size: int64(len(objects[key]))}
			}
			source := update.Release{ID: "source", Version: "1.0.0", Platform: platform, Arch: runtime.GOARCH, MinimumSystemVersion: "1.0.0", UpdaterProtocol: 1, MinUpdaterProtocol: 1,
				SigningIdentity: identity.SigningIdentity, Package: artifact("/source.zip"), Feed: artifact("/feed"), Components: artifact("/components.json")}
			target := source
			target.ID, target.Version, target.Package = "target", "1.1.0", artifact("/target.zip")
			manifest := []byte(`{"version":1,"profile":"desktop"}`)
			write(filepath.Join(f.paths.ResourcesRoot, "manifest.json"), manifest, 0600)
			edge := update.Edge{SourceID: source.ID, TargetID: target.ID, MigrationProfile: "same-v1", SourceDataProfiles: []string{state.DataProfile}, TargetDataProfile: state.DataProfile,
				RequiredComponents: state.Components, Recovery: update.RecoveryContract{Version: 1, SourcePackageSHA256: source.Package.SHA256, SourceManifestSHA256: hexDigest(manifest), HelperProtocolMin: 1, HelperProtocolMax: 1, Direction: "target-to-source"}}
			now := time.Now().UTC()
			policy := update.Policy{SchemaVersion: 1, MinUpdaterProtocol: 1, TrustDomain: identity.TrustDomain, Audience: identity.Audience, Channel: identity.Channel, Sequence: 1,
				IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), Releases: []update.Release{source, target}, Edges: []update.Edge{edge}}
			sign := func() []byte {
				t.Helper()
				raw, err := update.SignPublication(policy, identity, "fixture", private, now)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			objects["/policy.json"] = sign()
			client := server.Client()
			receipt, err := update.Download(t.Context(), identity, state, root, target.ID, client, now, nil)
			if err != nil {
				t.Fatal(err)
			}
			helperFile := filepath.Join(f.paths.ResourcesRoot, "bin", executableName("lazymind-update-helper"))
			write(helperFile, helper, 0700)
			bind := func(file string, identity update.Config) {
				write(file+".json", encode(map[string]any{"status": "enabled", "config": identity}), 0600)
			}
			bind(helperFile, identity)
			baseArgs := []string{"update-components-check", "--profile", "desktop", "--owner-token", f.cfg.OwnerToken, "--runtime-root", f.paths.RuntimeRoot,
				"--repo-root", f.paths.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--components", receipt.ComponentsPath, "--sha256", target.Components.SHA256, "--size", strconv.FormatInt(target.Components.Size, 10)}
			check := func(args []string) error {
				var out bytes.Buffer
				cli := NewCLI(&out, io.Discard, &fakeRunner{t: t}, "fixture-manager")
				if err := cli.Run(t.Context(), args); err != nil {
					return err
				}
				var response struct{ Status, Reason string }
				if err := json.Unmarshal(out.Bytes(), &response); err != nil {
					return err
				}
				if response.Status != "compatible" {
					return &update.Failure{Code: response.Reason}
				}
				return nil
			}
			if err := check(baseArgs); err != nil {
				t.Fatalf("ordinary component preflight: %v", err)
			}
			revoke := func() {
				t.Helper()
				policy.Sequence++
				policy.Revoked = []string{target.ID}
				if _, err := update.Check(t.Context(), sign(), identity, state, root, true, now); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "revoked-before-handoff" {
				revoke()
			}
			if scenario == "invalid-cache-at-source" {
				write(receipt.PackagePath, []byte("invalid package"), 0600)
			}
			if scenario != "manual" {
				err := check(append(baseArgs, "--handoff", target.ID, "--package", receipt.PackagePath))
				if scenario == "revoked-before-handoff" || scenario == "invalid-cache-at-source" {
					var blocked *update.Failure
					if !errors.As(err, &blocked) || blocked.Code == "" {
						t.Errorf("expected a blocked handoff for invalid evidence, got %v", err)
					}
				} else if err != nil {
					t.Errorf("could not commit source handoff: %v", err)
				}
			}
			if scenario == "revoked-after-handoff" {
				revoke()
			}
			targetIdentity := identity
			targetIdentity.ReleaseID, targetIdentity.Version = target.ID, target.Version
			if scenario == "wrong-release" {
				targetIdentity.ReleaseID = "other-same-version"
			}
			targetConfig := f.cfg
			targetPaths := f.paths
			if scenario == "different-installation" {
				targetPaths.ResourcesRoot = t.TempDir()
				targetConfig.ResourcesRoot = targetPaths.ResourcesRoot
				helperFile = filepath.Join(targetPaths.ResourcesRoot, "bin", executableName("lazymind-update-helper"))
				write(helperFile, helper, 0700)
			}
			if scenario == "different-runtime" {
				targetPaths.RuntimeRoot = t.TempDir()
				targetConfig.RuntimeRoot = targetPaths.RuntimeRoot
				targetPaths.StateDir = filepath.Join(targetPaths.RuntimeRoot, "state")
				for _, sub := range []string{"state", "deps/python-components"} {
					if err := os.CopyFS(filepath.Join(targetPaths.RuntimeRoot, sub), os.DirFS(filepath.Join(f.paths.RuntimeRoot, sub))); err != nil {
						t.Fatal(err)
					}
				}
				targetPaths.StateFile = filepath.Join(targetPaths.StateDir, filepath.Base(f.paths.StateFile))
			}
			bind(helperFile, targetIdentity)
			data := objects["/components.json"]
			if scenario == "changed-target-catalog" {
				data = append(slices.Clone(data), '\n')
			}
			if scenario == "changed-selection" {
				selected.Revision = hexDigest([]byte("different valid selection"))
				install(selected)
			}
			write(filepath.Join(targetPaths.ResourcesRoot, "config", "python-components.json"), data, 0600)
			t.Setenv(desktopAppVersionEnvVar, target.Version)
			err = prepareDesktopPythonComponents(t.Context(), targetConfig, targetPaths)
			sites := pythonComponentSites(targetPaths)
			if scenario == "authorized" {
				if err != nil || len(sites) != 2 {
					t.Fatalf("authorized target lost installed components: %v, %v", sites, err)
				}
				for id, revision := range state.Components {
					if !slices.Contains(sites, filepath.Join(targetPaths.RuntimeRoot, "deps", "python-components", id, revision, "site-packages")) {
						t.Fatalf("target did not activate the exact %s selection", id)
					}
				}
				// Force another version transition; a consumed authorization must not replay.
				for _, id := range []string{"rag", "cluster"} {
					write(filepath.Join(targetPaths.StateDir, id+"-component-app-version"), []byte("1.0.0"), 0600)
				}
				if err := prepareDesktopPythonComponents(t.Context(), targetConfig, targetPaths); err != nil || len(pythonComponentSites(targetPaths)) != 0 {
					t.Fatalf("handoff replayed: %v", err)
				}
			} else if err == nil && len(sites) != 0 {
				t.Fatalf("%s retained selections without valid handoff: %v", scenario, sites)
			}
			if scenario == "manual" && err != nil {
				t.Fatalf("manual upgrade must still start: %v", err)
			}
			if record, err := update.ReadNativeRecord(root); err != nil || record != nil {
				t.Fatalf("v1 created native-v2 state: %+v %v", record, err)
			}
		})
	}
}
