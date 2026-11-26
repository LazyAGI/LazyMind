//go:build darwin && lazymind_unsigned_test

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestUnsignedRuntimeHonorsOnlyPersistedLegacyAuthAndVector(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "user")
	t.Setenv("HOME", home)
	t.Setenv(localHostHomeEnvVar, home)
	layout := runtimePathLayoutForGOOS("darwin", home, "", "", "", "")
	p := update.UnsignedTestPaths{Home: home, Application: filepath.Join(home, "Applications", "LazyMind.app"), Runtime: layout.DataRoot, Cache: layout.CacheRoot, Logs: layout.LogsRoot, Documents: layout.LocalImportRoot}
	old := unsignedRuntimeLocations
	unsignedRuntimeLocations = func() (update.UnsignedTestPaths, string, error) { return p, filepath.Join(base, "Applications"), nil }
	t.Cleanup(func() { unsignedRuntimeLocations = old })
	resources := filepath.Join(p.Application, "Contents", "Resources", "runtime")
	repo := filepath.Join(resources, "app")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	writeComposeFixture(t, repo)
	opts := RuntimeConfigOptions{Profile: "desktop", OwnerToken: "fixture", RepoRoot: repo, ResourcesRoot: resources}
	for _, key := range []string{authServiceDatabaseURLEnvVar, localMilvusLiteDBPathEnvVar, localMilvusLiteDataDirEnvVar, localSQLiteDirEnvVar} {
		t.Setenv(key, "")
	}
	cfg, paths, err := newRuntimeConfigWithOptions(opts, true)
	if err != nil {
		t.Fatal(err)
	}
	state := defaultRuntimeState(cfg, cfg.ProcessComposePort, paths.RunDirTokenFile)
	external := filepath.Join(base, "custom")
	if err := os.MkdirAll(external, 0700); err != nil {
		t.Fatal(err)
	}
	auth, vector := filepath.Join(external, "configured-auth.sqlite"), filepath.Join(external, "configured-vector")
	state.Config.AuthService.DatabaseURL = sqliteURL(auth)
	state.Config.ModeProfile.VectorStore.DBPath = vector
	state.Config.UpdateStorage = nil
	if err := os.MkdirAll(filepath.Dir(paths.StateFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	watch := filepath.Join(external, "input-documents")
	anotherDB := filepath.Join(external, "old-state.db")
	for _, modern := range []bool{false, true} {
		shape := "early-0.5"
		if modern {
			shape = "with-snapshot"
		}
		t.Run(shape, func(t *testing.T) {
			saved := state
			saved.Config.FileWatcher.WatchHostDir = watch
			saved.Config.FileWatcher.AllowedRoots = []string{watch}
			if modern {
				saved.Config.UpdateStorage = cloneUpdateStorage(cfg.UpdateStorage)
				saved.Config.UpdateStorage.Databases["auth"] = auth
				saved.Config.UpdateStorage.Databases["core-state"] = anotherDB
				saved.Config.UpdateStorage.VectorPath = vector
			}
			if err := writeRuntimeState(paths.StateFile, saved); err != nil {
				t.Fatal(err)
			}
			cases := []struct {
				name, auth, vector, watch string
				valid                     bool
			}{
				{"stored uses", auth, vector, watch, true}, {"unrecorded auth", auth + ".different", vector, watch, false}, {"unrecorded vector", auth, vector + ".different", watch, false},
				{"auth as vector", auth, auth, watch, false}, {"auth WAL as vector", auth, auth + "-wal", watch, false}, {"auth SHM as vector", auth, auth + "-shm", watch, false},
				{"watch as vector", auth, watch, watch, false}, {"auth as watcher", auth, vector, auth, false}, {"auth WAL as watcher", auth, vector, auth + "-wal", false},
				{"auth SHM as watcher", auth, vector, auth + "-shm", false}, {"vector as watcher", auth, vector, vector, false},
				{"watch as database", watch, vector, watch, false}, {"vector as database", vector, vector, watch, false},
			}
			if modern {
				cases = append(cases, struct {
					name, auth, vector, watch string
					valid                     bool
				}{"other database family as auth", anotherDB, vector, watch, false})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Setenv(authServiceDatabaseURLEnvVar, sqliteURL(tc.auth))
					t.Setenv(localMilvusLiteDataDirEnvVar, tc.vector)
					t.Setenv("LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR", tc.watch)
					t.Setenv("LAZYMIND_FILE_WATCHER_EXTRA_ALLOWED_ROOTS_JSON", "")
					effective, _, err := newRuntimeConfigWithOptions(opts, true)
					if tc.valid {
						if err != nil || effective.UpdateStorage.Databases["auth"] != auth || effective.ModeProfile.VectorStore.DBPath != vector || effective.FileWatcher.WatchHostDir != watch {
							t.Fatalf("stored external roles: %v", err)
						}
					} else if err == nil {
						t.Fatalf("accepted %s", tc.name)
					}
				})
			}
		})
	}
}

func TestUnsignedRuntimeKeepsForeignV1HandoffAndMarkers(t *testing.T) {
	for _, phase := range []string{"", "prepared", "authorized"} {
		t.Run("phase-"+phase, func(t *testing.T) {
			base := t.TempDir()
			paths := RuntimePaths{StateDir: filepath.Join(base, "state"), RuntimeRoot: base, ResourcesRoot: filepath.Join(base, "app")}
			if err := os.MkdirAll(paths.StateDir, 0700); err != nil {
				t.Fatal(err)
			}
			digest := strings.Repeat("a", 64)
			r := update.V1ComponentHandoff{SourceID: "official-A", SourceVersion: "1.0.0", TargetID: "official-B", TargetVersion: "1.1.0", SourceOwner: digest, Installation: paths.ResourcesRoot, Runtime: base, PolicyURL: "https://official.example.invalid/policy", DataProfile: "format", EdgeDigest: digest, SelectionDigest: digest, Phase: phase}
			if phase != "" {
				actor, err := update.CurrentProcessIdentity()
				if err != nil {
					t.Fatal(err)
				}
				r.SourceActor = &actor
				r.SourceConfigDigest = digest
				r.AttemptID = strings.Repeat("1", 32)
			}
			if phase == "authorized" {
				r.MarkerChanges = map[string]*string{"rag": nil}
			}
			if err := r.Save(paths.StateDir); err != nil {
				t.Fatal(err)
			}
			if read, err := update.ReadV1ComponentHandoff(paths.StateDir); err != nil || read == nil {
				t.Fatalf("invalid v1 prerequisite: %v", err)
			}
			file := filepath.Join(paths.StateDir, "v1-component-handoff.json")
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(paths.StateDir, "rag-component-app-version")
			if err := os.WriteFile(marker, []byte("official previous marker"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(desktopAppVersionEnvVar, "1.2.0")
			if err := prepareDesktopPythonComponents(t.Context(), RuntimeConfig{Profile: "desktop"}, paths); err == nil {
				t.Fatal("foreign unfinished handoff allowed startup")
			}
			after, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("foreign v1 receipt changed")
			}
			if got, err := os.ReadFile(marker); err != nil || string(got) != "official previous marker" {
				t.Fatal("foreign v1 marker changed")
			}
		})
	}
}
