//go:build darwin

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// The ordinary build produces the persisted config that the test-flavor
// manager must reopen. Both processes operate in an isolated account fixture.
func TestInPlacePreviousDesktopConfigWriter(t *testing.T) {
	raw := os.Getenv("LAZYMIND_TEST_INPLACE_PREVIOUS")
	if raw == "" {
		return
	}
	if update.UnsignedTestBuild {
		t.Fatal("prior config must come from the ordinary build")
	}
	var input struct {
		Home            string
		Options         RuntimeConfigOptions
		WithoutSnapshot bool
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	t.Setenv(localHostHomeEnvVar, input.Home)
	t.Setenv("HOME", input.Home)
	cfg, p, err := newRuntimeConfigWithOptions(input.Options, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.StateFile), 0700); err != nil {
		t.Fatal(err)
	}
	saved := defaultRuntimeState(cfg, cfg.ProcessComposePort, p.RunDirTokenFile)
	saved.OverallStatus = "stopped"
	if input.WithoutSnapshot {
		// Early 0.5 state predates these optional fields. Use the ordinary
		// writer for paths and ports, omitting the three newer JSON fields.
		saved.Config.UpdateStorage = nil
		saved.Config.NativeCommittedTx = ""
		saved.Config.NativeCommittedReleaseID = ""
	}
	if err := writeRuntimeState(p.StateFile, saved); err != nil {
		t.Fatal(err)
	}
	key, err := deviceCredentialKey("cloud-oauth")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.StateFile+".test-key", []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInPlaceRuntimeReopensOrdinaryDesktopConfiguration(t *testing.T) {
	if !update.UnsignedTestBuild {
		t.Skip("exercises the test-flavor consumer of the ordinary build's state")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "User")
	t.Setenv("HOME", home)
	t.Setenv(localHostHomeEnvVar, home)
	appSupport := filepath.Join(home, "Library", "Application Support")
	layout := runtimePathLayoutForGOOS("darwin", home, "", "", "", "")
	account := update.UnsignedTestPaths{Home: home, Runtime: layout.DataRoot, Profile: filepath.Join(appSupport, "lazymind-desktop"), Updater: filepath.Join(appSupport, "LazyMindUpdater"), Cache: layout.CacheRoot, Logs: layout.LogsRoot, Documents: layout.LocalImportRoot, CLI: filepath.Join(home, ".lazymind"), Application: filepath.Join(home, "Applications", "LazyMind.app")}
	system := filepath.Join(base, "System", "Applications")
	prev := unsignedRuntimeLocations
	unsignedRuntimeLocations = func() (update.UnsignedTestPaths, string, error) { return account, system, nil }
	t.Cleanup(func() { unsignedRuntimeLocations = prev })
	for _, k := range []string{runtimeRootEnvVar, runtimeResourcesRootEnvVar, localBuildRootEnvVar, localSQLiteDirEnvVar, authServiceDatabaseURLEnvVar, "LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION", "LAZYMIND_UPDATE_BOOTSTRAP_OWNER", "LAZYMIND_UPDATE_BOOTSTRAP_STORAGE"} {
		t.Setenv(k, "")
	}
	ordinary := filepath.Join(base, "ordinary-runtime.test")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-c", "-o", ordinary, ".")
	build.Env = coreFixtureBuildEnv
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build ordinary config writer: %v %s", e, out)
	}
	for _, scenario := range []string{"user default", "system default", "user existing custom runtime and DB", "system existing custom runtime and DB", "uninstalled archive", "unrelated build root"} {
		t.Run(scenario, func(t *testing.T) {
			app := account.Application
			if scenario == "system default" || scenario == "system existing custom runtime and DB" {
				app = filepath.Join(system, "LazyMind.app")
			}
			invalid := scenario == "uninstalled archive" || scenario == "unrelated build root"
			if scenario == "uninstalled archive" {
				app = filepath.Join(base, "unpacked", "LazyMind.app")
			}
			resources := filepath.Join(app, "Contents", "Resources", "runtime")
			repo := filepath.Join(resources, "app")
			if err := os.MkdirAll(repo, 0700); err != nil {
				t.Fatal(err)
			}
			writeComposeFixture(t, repo)
			opts := RuntimeConfigOptions{Profile: "desktop", OwnerToken: "fixture-owner", RepoRoot: repo, ResourcesRoot: resources}
			if scenario == "unrelated build root" {
				opts.BuildRoot = filepath.Join(base, "unrelated")
			}
			runtimeRoot := layout.DataRoot
			sqliteRoot := filepath.Join(runtimeRoot, "data", "stores", "sqlite")
			if scenario == "user existing custom runtime and DB" || scenario == "system existing custom runtime and DB" {
				runtimeRoot = filepath.Join(base, "external", scenario, "existing runtime")
				sqliteRoot = filepath.Join(base, "external", scenario, "existing databases")
				opts.RuntimeRoot = runtimeRoot
				t.Setenv(localSQLiteDirEnvVar, sqliteRoot)
			}
			if invalid {
				if _, _, err := newRuntimeConfigWithOptions(opts, true); err == nil {
					t.Fatalf("accepted unbound resources: %+v", opts)
				}
				return
			}
			core := filepath.Join(sqliteRoot, "core", "core.db")
			if err := os.MkdirAll(filepath.Dir(core), 0700); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", core)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("CREATE TABLE IF NOT EXISTS inplace_sentinel(value TEXT); INSERT OR IGNORE INTO inplace_sentinel(rowid,value) VALUES (1,'previous desktop')"); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			withoutSnapshot := scenario == "user default" || scenario == "user existing custom runtime and DB"
			input, err := json.Marshal(struct {
				Home            string
				Options         RuntimeConfigOptions
				WithoutSnapshot bool
			}{home, opts, withoutSnapshot})
			if err != nil {
				t.Fatal(err)
			}
			seed := exec.CommandContext(t.Context(), ordinary, "-test.run=^TestInPlacePreviousDesktopConfigWriter$")
			seed.Env = append(os.Environ(), "LAZYMIND_TEST_INPLACE_PREVIOUS="+string(input))
			if out, e := seed.CombinedOutput(); e != nil {
				t.Fatalf("ordinary build could not persist this config: %v %s", e, out)
			}
			filename := filepath.Join(runtimeRoot, "state", stateFileName)
			before, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if withoutSnapshot {
				var wire struct{ Config map[string]json.RawMessage }
				if err := json.Unmarshal(before, &wire); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"updateStorage", "nativeCommittedTx", "nativeCommittedReleaseId"} {
					if _, ok := wire.Config[name]; ok {
						t.Fatalf("early 0.5 fixture unexpectedly contains %s", name)
					}
				}
			}
			cfg, p, err := newRuntimeConfigWithOptions(opts, true)
			if err != nil {
				t.Fatalf("test build did not reopen the same valid ordinary config: %v", err)
			}
			if p.RuntimeRoot != runtimeRoot || cfg.UpdateStorage.Databases["core"] != core || p.CoreDBPath != core || p.LogsDir != layout.LogsRoot {
				t.Fatalf("test build substituted storage: runtime=%q DB=%q logs=%q", p.RuntimeRoot, p.CoreDBPath, p.LogsDir)
			}
			key, err := deviceCredentialKey("cloud-oauth")
			previousKey, readErr := os.ReadFile(p.StateFile + ".test-key")
			if err != nil || readErr != nil || key == "" || string(previousKey) != key {
				t.Fatalf("previously enrolled credential device changed (errors %v %v)", err, readErr)
			}
			state, err := readRuntimeState(p.StateFile)
			if err != nil || state.ResourcesRoot != resources || (state.Config.UpdateStorage == nil) != withoutSnapshot {
				t.Fatalf("did not recover previous desktop snapshot: %v", err)
			}
			effective := applyStateConfig(cfg, state)
			if effective.UpdateStorage == nil || effective.UpdateStorage.Databases["core"] != core {
				t.Fatal("ordinary startup did not recover the existing resolved storage")
			}
			after, err := os.ReadFile(p.StateFile)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("configuration read modified the prior runtime state")
			}
			db, err = sql.Open("sqlite", p.CoreDBPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var value string
			if err := db.QueryRow("SELECT value FROM inplace_sentinel WHERE rowid=1").Scan(&value); err != nil || value != "previous desktop" {
				t.Fatalf("previous user data missing: %q %v", value, err)
			}
		})
	}
}
