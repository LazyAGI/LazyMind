//go:build darwin && lazymind_unsigned_test

package main

import (
	"database/sql"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// v0.5.0 advertises only RAG and does not write component-app-version markers.
// The target adds a deferred cluster component and can ship a different RAG pin
// in the same tag. Its ordinary startup must retain a usable online selection.
func TestInPlaceFirstStartRetainsPreviousComponents(t *testing.T) {
	for _, scenario := range []string{"not-installed", "installed-pin", "selected-online", "offline-new-revision", "missing-ready", "damaged-selection", "missing-target-catalog", "invalid-source-marker"} {
		t.Run(scenario, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home := filepath.Join(base, "home")
			t.Setenv("HOME", home)
			t.Setenv(localHostHomeEnvVar, home)
			t.Setenv(desktopAppVersionEnvVar, "0.5.301")
			layout := runtimePathLayoutForGOOS("darwin", home, "", "", "", "")
			account := update.UnsignedTestPaths{Home: home, Application: filepath.Join(home, "Applications", "LazyMind.app"),
				Runtime: layout.DataRoot, Cache: layout.CacheRoot, Logs: layout.LogsRoot, Documents: layout.LocalImportRoot,
				Profile: filepath.Join(home, "Library/Application Support/lazymind-desktop"),
				Updater: filepath.Join(home, "Library/Application Support/LazyMindUpdater"), CLI: filepath.Join(home, ".lazymind")}
			previous := unsignedRuntimeLocations
			unsignedRuntimeLocations = func() (update.UnsignedTestPaths, string, error) {
				return account, filepath.Join(base, "Applications"), nil
			}
			t.Cleanup(func() { unsignedRuntimeLocations = previous })
			resources := filepath.Join(account.Application, "Contents/Resources/runtime")
			opts := RuntimeConfigOptions{Profile: "desktop", OwnerToken: "fixture-owner", RepoRoot: filepath.Join(resources, "app"),
				ResourcesRoot: resources, RuntimeRoot: account.Runtime}
			if err := os.MkdirAll(opts.RepoRoot, 0700); err != nil {
				t.Fatal(err)
			}
			writeComposeFixture(t, opts.RepoRoot)
			cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := paths.EnsureAllDirs(); err != nil {
				t.Fatal(err)
			}
			write := func(file string, raw []byte) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			tag := hexDigest([]byte("rag tag"))
			repo := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/updates/unsigned-test/demo/in-place/round-41/RAG/") {
					t.Errorf("the new app requested the previous channel: %s", r.URL.Path)
				}
				http.Error(w, "component mirror offline", http.StatusServiceUnavailable)
			}))
			t.Cleanup(repo.Close)
			originalTransport := http.DefaultTransport
			http.DefaultTransport = repo.Client().Transport
			t.Cleanup(func() { http.DefaultTransport = originalTransport })
			oldBase := repo.URL + "/RAG/tags/" + tag + "/"
			newBase := repo.URL + "/updates/unsigned-test/demo/in-place/round-41/RAG/tags/" + tag + "/"
			rag := map[string]any{"id": "rag", "revision": hexDigest([]byte("pinned")), "sha256": hexDigest([]byte("pinned payload")),
				"tag": tag, "baseFingerprint": hexDigest([]byte("python base")), "pythonAbi": "cp311",
				"platform": runtime.GOOS, "arch": runtime.GOARCH, "packages": map[string]string{"rag-fixture": "1.0"},
				"catalogUrl": oldBase + "python-components.json", "url": oldBase + "rag.zip",
				"filename": "rag.zip", "sizeBytes": 64, "unpackedBytes": 256}
			catalogFile := filepath.Join(resources, "config/python-components.json")
			catalog := func(entries map[string]any) {
				t.Helper()
				raw, err := json.Marshal(map[string]any{"schemaVersion": 1, "platform": runtime.GOOS, "arch": runtime.GOARCH, "components": entries})
				if err != nil {
					t.Fatal(err)
				}
				write(catalogFile, raw)
			}
			selected := maps.Clone(rag)
			if scenario != "installed-pin" {
				selected["revision"], selected["sha256"] = hexDigest([]byte("online")), hexDigest([]byte("online payload"))
			}
			component := filepath.Join(paths.RuntimeRoot, "deps/python-components/rag")
			payload := filepath.Join(component, selected["revision"].(string), "site-packages", "rag_user_fixture.py")
			installed := scenario != "not-installed" && scenario != "invalid-source-marker"
			if installed {
				writeBootstrapFixtureRAG(t, &updateStateFixture{cfg: cfg, paths: paths}, selected)
				write(payload, []byte("selection = 'previously installed'\n"))
				if scenario == "installed-pin" {
					// The packaged pin also works offline before an online selection exists.
					if err := os.Remove(filepath.Join(component, "catalog-"+selected["tag"].(string)+".json")); err != nil {
						t.Fatal(err)
					}
				}
			}
			catalog(map[string]any{"rag": rag})
			assertSelection := func() {
				t.Helper()
				current, _, err := newRuntimeConfigWithOptions(opts, true)
				if err != nil {
					t.Fatal(err)
				}
				env := pythonComponentEnvironment(paths, current.Algorithm.RAGDisabled)
				sites := filepath.SplitList(committedEnvValue(env, "LAZYMIND_PYTHON_COMPONENT_PATHS"))
				if !installed {
					if committedEnvValue(env, ragEnabledEnvVar) != "0" {
						t.Fatal("normal startup enabled uninstalled RAG")
					}
					return
				}
				if committedEnvValue(env, ragEnabledEnvVar) != "1" {
					t.Fatal("the previously installed RAG is disabled")
				}
				args := []string{"-I", "-S", "-B", "-c",
					"import sys; sys.path[:0] = sys.argv[1:]; from rag_user_fixture import selection; print(selection)"}
				if out, err := exec.CommandContext(t.Context(), "/usr/bin/python3", append(args, sites...)...).CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "previously installed" {
					t.Fatalf("previous overlay not importable: %v %s", err, out)
				}
			}
			assertSelection()
			state := defaultRuntimeState(cfg, cfg.ProcessComposePort, paths.RunDirTokenFile)
			state.OverallStatus = "stopped"
			state.Config.UpdateStorage = nil
			state.Config.Algorithm.RAGDisabled = !installed
			if err := writeRuntimeState(paths.StateFile, state); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", paths.CoreDBPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("CREATE TABLE documents(content TEXT); INSERT INTO documents VALUES ('existing document')"); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			assertData := func() {
				t.Helper()
				reopened, err := sql.Open("sqlite", "file:"+paths.CoreDBPath+"?mode=ro")
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				var document string
				if err := reopened.QueryRow("SELECT content FROM documents").Scan(&document); err != nil || document != "existing document" {
					t.Fatalf("old user data changed: %q %v", document, err)
				}
			}

			cluster := maps.Clone(rag)
			cluster["id"], cluster["tag"] = "cluster", hexDigest([]byte("cluster tag"))
			// A now pins the revision selected under the old online tag. It
			// authorizes those same bytes, but under its own catalog and download URLs.
			targetRAG := maps.Clone(selected)
			targetRAG["catalogUrl"], targetRAG["url"] = newBase+"python-components.json", newBase+"rag.zip"
			if scenario == "offline-new-revision" {
				targetTag := hexDigest([]byte("A Python class"))
				base := strings.Replace(newBase, tag, targetTag, 1)
				targetRAG["tag"], targetRAG["revision"], targetRAG["sha256"] = targetTag, hexDigest([]byte("A revision")), hexDigest([]byte("A archive"))
				targetRAG["pythonAbi"], targetRAG["baseFingerprint"] = "cp312", hexDigest([]byte("A Python base"))
				targetRAG["catalogUrl"], targetRAG["url"] = base+"python-components.json", base+"new-rag.zip"
			}
			cluster["catalogUrl"], cluster["url"] = newBase+"cluster.json", newBase+"cluster.zip"
			catalog(map[string]any{"rag": targetRAG, "cluster": cluster})
			var damaged string
			switch scenario {
			case "missing-ready":
				damaged = filepath.Join(filepath.Dir(filepath.Dir(payload)), ".ready")
			case "damaged-selection":
				damaged = filepath.Join(component, "catalog-"+selected["tag"].(string)+".json")
			case "missing-target-catalog":
				damaged = catalogFile
			case "invalid-source-marker":
				damaged = filepath.Join(component, "source.ready")
			}
			var original []byte
			if damaged != "" {
				if scenario != "invalid-source-marker" {
					original, err = os.ReadFile(damaged)
					if err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "missing-ready" || scenario == "missing-target-catalog" {
					if err := os.Remove(damaged); err != nil {
						t.Fatal(err)
					}
				} else {
					write(damaged, []byte("invalid previous selection"))
				}
			}
			prepare := func() error {
				current, currentPaths, err := newRuntimeConfigWithOptions(opts, true)
				if err != nil {
					return err
				}
				return prepareDesktopPythonComponents(t.Context(), current, currentPaths)
			}
			if scenario == "offline-new-revision" {
				old := make(map[string]string)
				for _, file := range []string{payload, filepath.Join(filepath.Dir(filepath.Dir(payload)), ".ready"),
					filepath.Join(component, "catalog-"+tag+".json")} {
					raw, err := os.ReadFile(file)
					if err != nil {
						t.Fatal(err)
					}
					old[file] = string(raw)
				}
				for start := 1; start <= 2; start++ {
					if err := prepare(); err != nil {
						t.Fatalf("offline replacement blocked desktop startup %d: %v", start, err)
					}
					current, _, err := newRuntimeConfigWithOptions(opts, true)
					if err != nil {
						t.Fatal(err)
					}
					env := pythonComponentEnvironment(paths, current.Algorithm.RAGDisabled)
					sites := filepath.SplitList(committedEnvValue(env, "LAZYMIND_PYTHON_COMPONENT_PATHS"))
					if committedEnvValue(env, ragEnabledEnvVar) == "1" {
						t.Fatal("incompatible RAG was activated")
					}
					args := []string{"-I", "-S", "-B", "-c", "import importlib.util,sys;sys.path[:0]=sys.argv[1:];assert importlib.util.find_spec('rag_user_fixture') is None"}
					if out, err := exec.CommandContext(t.Context(), "/usr/bin/python3", append(args, sites...)...).CombinedOutput(); err != nil {
						t.Fatalf("incompatible module reached the target Python: %v %s", err, out)
					}
					for file, want := range old {
						if raw, err := os.ReadFile(file); err != nil || string(raw) != want {
							t.Fatalf("startup %d lost previous component evidence %s: %v", start, file, err)
						}
					}
					assertData()
				}
				return
			}
			if damaged != "" {
				if err := prepare(); err != nil {
					t.Fatalf("unavailable optional component blocked ordinary desktop startup: %v", err)
				}
				current, _, err := newRuntimeConfigWithOptions(opts, true)
				if err != nil {
					t.Fatal(err)
				}
				env := pythonComponentEnvironment(paths, current.Algorithm.RAGDisabled)
				if committedEnvValue(env, ragEnabledEnvVar) == "1" || committedEnvValue(env, "LAZYMIND_PYTHON_COMPONENT_PATHS") != "" {
					t.Fatal("unavailable RAG was exposed to the target service")
				}
				assertData()
				if scenario == "invalid-source-marker" {
					if err := os.Remove(damaged); err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
				} else {
					write(damaged, original)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				if err := prepare(); err != nil {
					t.Fatalf("normal start %d: %v", attempt+1, err)
				}
				assertSelection()
			}
			assertData()
		})
	}
}
