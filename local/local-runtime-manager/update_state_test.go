package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

type updateStateFixture struct {
	cfg       RuntimeConfig
	paths     RuntimePaths
	databases map[string]*sql.DB
}

func newUpdateStateFixture(t *testing.T) *updateStateFixture {
	t.Helper()
	t.Setenv(ragEnabledEnvVar, "false")
	t.Setenv(localSQLiteDirEnvVar, "")
	t.Setenv(authServiceDatabaseURLEnvVar, "")
	t.Setenv("LAZYMIND_SEGMENT_STORE_TYPE", "SQLiteStore")
	t.Setenv("LAZYMIND_SEGMENT_STORE_URI_OR_PATH", "")
	t.Setenv("LAZYMIND_STATE_SQLITE_PATH", "")
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", OwnerToken: "fixture-owner", RepoRoot: repo, ResourcesRoot: t.TempDir(), RuntimeRoot: t.TempDir()}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureAllDirs(); err != nil {
		t.Fatal(err)
	}
	state := defaultRuntimeState(cfg, cfg.ProcessComposePort, paths.RunDirTokenFile)
	state.OverallStatus = "ready"
	if err := writeRuntimeState(paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	f := &updateStateFixture{cfg: cfg, paths: paths, databases: map[string]*sql.DB{}}
	for name, file := range map[string]string{"core": paths.CoreDBPath, "auth": paths.AuthServiceDBPath, "lazyllm": paths.LazyLLMDBPath, "segments": localSegmentStoreBackingPath(paths),
		"channel": paths.ChannelGatewayDBPath, "scan": paths.ScanDBPath, "core-state": filepath.Join(paths.CoreStateDir, "core_state.db"),
		"auth-state": filepath.Join(paths.AuthServiceStateDir, "auth_state.db"), "scan-state": filepath.Join(paths.ScanControlPlaneStateDir, "scan_state.db")} {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", file)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE fixture_records (id INTEGER PRIMARY KEY, value TEXT); INSERT INTO fixture_records VALUES (1, 'PRIVATE-FIXTURE-VALUE')"); err != nil {
			t.Fatal(err)
		}
		f.databases[name] = db
	}
	if _, err := f.databases["core"].Exec("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, dirty BOOLEAN NOT NULL); INSERT INTO schema_migrations VALUES (101, 0); CREATE TABLE schema_migration_history (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT); INSERT INTO schema_migration_history VALUES (101, 'fixture_baseline', '2026-01-01')"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *updateStateFixture) measure(t *testing.T) desktopupdate.LocalState {
	t.Helper()
	state, err := measureDesktopUpdateState(context.Background(), f.cfg, f.paths)
	if err != nil {
		t.Fatal(err)
	}
	if state.DataProfile == "" || state.Dirty {
		t.Fatalf("invalid state %+v", state)
	}
	return state
}

func TestDesktopUpdateStateMeasuresSchemasAndHistoryNotRowsPathsOrAppVersion(t *testing.T) {
	f := newUpdateStateFixture(t)
	beforeDB, err := os.ReadFile(f.paths.CoreDBPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeWAL, err := os.ReadFile(f.paths.CoreDBPath + "-wal")
	if err != nil || len(beforeWAL) == 0 {
		t.Fatalf("fixture must have committed uncheckpointed WAL: %v", err)
	}
	first := f.measure(t)
	afterDB, _ := os.ReadFile(f.paths.CoreDBPath)
	afterWAL, _ := os.ReadFile(f.paths.CoreDBPath + "-wal")
	if !bytes.Equal(beforeDB, afterDB) || !bytes.Equal(beforeWAL, afterWAL) {
		t.Fatal("inspection mutated durable database bytes")
	}
	t.Setenv(desktopAppVersionEnvVar, "99.0.0")
	if _, err := f.databases["core"].Exec("UPDATE fixture_records SET value='OTHER-PRIVATE'; UPDATE schema_migration_history SET applied_at='2026-02-01'"); err != nil {
		t.Fatal(err)
	}
	if next := f.measure(t); next.DataProfile != first.DataProfile {
		t.Fatal("row contents or app version changed schema profile")
	}
	other := newUpdateStateFixture(t)
	if next := other.measure(t); next.DataProfile != first.DataProfile {
		t.Fatal("absolute paths changed schema profile")
	}
	for _, family := range []string{"core", "auth", "channel", "scan", "lazyllm", "segments", "core-state", "auth-state", "scan-state"} {
		before := f.measure(t).DataProfile
		if _, err := f.databases[family].Exec("ALTER TABLE fixture_records ADD COLUMN new_field TEXT"); err != nil {
			t.Fatal(err)
		}
		if after := f.measure(t).DataProfile; after == before {
			t.Fatalf("missed %s schema change (including committed WAL)", family)
		}
	}
	before := f.measure(t).DataProfile
	if _, err := f.databases["core"].Exec("UPDATE schema_migrations SET version=102; INSERT INTO schema_migration_history VALUES (102,'extra_migration','2026-03-01')"); err != nil {
		t.Fatal(err)
	}
	if after := f.measure(t).DataProfile; after == before {
		t.Fatal("missed migration history change with identical tables")
	}
	for _, statement := range []string{"UPDATE schema_migration_history SET name='renamed_baseline' WHERE version=101", "UPDATE schema_migration_history SET version=100 WHERE version=101"} {
		before := f.measure(t).DataProfile
		if _, err := f.databases["core"].Exec(statement); err != nil {
			t.Fatal(err)
		}
		if after := f.measure(t).DataProfile; after == before {
			t.Fatal("ignored full history while max version and schema remained unchanged")
		}
	}
	raw, _ := json.Marshal(first)
	for _, private := range []string{"PRIVATE-FIXTURE-VALUE", f.paths.RuntimeRoot, f.cfg.OwnerToken} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("local details leaked")
		}
	}
}

func TestDesktopUpdateStateRejectsDirtyMissingAndUnknownInput(t *testing.T) {
	for _, kind := range []string{"dirty", "missing-core", "missing-auth", "missing-segments", "no-history", "bad-state", "foreign-owner", "external-auth", "external-segments", "cancelled", "no-storage-metadata"} {
		t.Run(kind, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			ctx := context.Background()
			switch kind {
			case "dirty":
				_, _ = f.databases["core"].Exec("UPDATE schema_migrations SET dirty=1")
			case "missing-core":
				_ = f.databases["core"].Close()
				_ = os.Remove(f.paths.CoreDBPath)
			case "missing-auth":
				_ = f.databases["auth"].Close()
				_ = os.Remove(f.paths.AuthServiceDBPath)
			case "missing-segments":
				_ = f.databases["segments"].Close()
				_ = os.Remove(localSegmentStoreBackingPath(f.paths))
			case "no-history":
				_, _ = f.databases["core"].Exec("DROP TABLE schema_migration_history")
			case "bad-state":
				_ = os.WriteFile(f.paths.StateFile, []byte("{broken"), 0600)
			case "foreign-owner":
				f.cfg.OwnerToken = "other-instance"
			case "external-auth":
				f.cfg.AuthService.DatabaseURL = "postgresql://fixture.invalid/auth"
				state, _ := readRuntimeState(f.paths.StateFile)
				state.Config.AuthService.DatabaseURL = f.cfg.AuthService.DatabaseURL
				if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
					t.Fatal(err)
				}
			case "external-segments":
				t.Setenv("LAZYMIND_SEGMENT_STORE_TYPE", "OpenSearch")
				state, _ := readRuntimeState(f.paths.StateFile)
				state.Config.UpdateStorage = &UpdateStorageSnapshot{Version: 1, SegmentStore: "OpenSearch"}
				if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
					t.Fatal(err)
				}
			case "no-storage-metadata":
				state, _ := readRuntimeState(f.paths.StateFile)
				state.Config.UpdateStorage = nil
				if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			state, err := measureDesktopUpdateState(ctx, f.cfg, f.paths)
			if err == nil && !state.Dirty {
				t.Fatalf("accepted %s: %+v", kind, state)
			}
			if state.DataProfile != "" {
				t.Fatalf("failure exposed compatible profile: %+v", state)
			}
			if kind == "dirty" && (err != nil || !state.Dirty) {
				t.Fatalf("dirty must remain explicit: %+v %v", state, err)
			}
			if kind == "cancelled" && desktopupdate.ErrorCode(err) != "cancelled" {
				t.Fatalf("cancelled inspection: %v", err)
			}
			if kind == "missing-core" {
				if _, statErr := os.Stat(f.paths.CoreDBPath); !os.IsNotExist(statErr) {
					t.Fatal("inspection created missing database")
				}
			}
		})
	}
}

func TestDesktopUpdateStateReadsActualComponentInstallation(t *testing.T) {
	f := newUpdateStateFixture(t)
	entry := pythonComponent{ID: "rag", Revision: strings.Repeat("a", 64), SHA256: strings.Repeat("b", 64), BaseFingerprint: strings.Repeat("c", 64), Platform: runtime.GOOS, Arch: runtime.GOARCH, PythonABI: "cp311", Packages: map[string]string{"fixture": "1.0"}}
	catalog := pythonComponentCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]pythonComponent{"rag": entry}}
	raw, _ := json.Marshal(catalog)
	dir := filepath.Join(f.paths.ResourcesRoot, "config")
	_ = os.MkdirAll(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, "python-components.json"), raw, 0600)
	if state := f.measure(t); state.Components["rag"] != "" {
		t.Fatal("catalog entry alone counted as installed")
	}
	installed := filepath.Join(f.paths.RuntimeRoot, "deps", "python-components", "rag", entry.Revision)
	_ = os.MkdirAll(filepath.Join(installed, "site-packages"), 0700)
	raw, _ = json.Marshal(entry)
	_ = os.WriteFile(filepath.Join(installed, "bundle-manifest.json"), raw, 0600)
	_ = os.WriteFile(filepath.Join(installed, ".ready"), []byte(entry.SHA256), 0600)
	// It is installed even if currently disabled. Future recovery must preserve it.
	if state := f.measure(t); state.Components["rag"] != entry.Revision {
		t.Fatalf("installed component omitted: %+v", state)
	}
	state, _ := readRuntimeState(f.paths.StateFile)
	state.Config.Algorithm.RAGDisabled = false
	state.Config.ModeProfile.VectorStore.ManagedProcess = true
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	if next := f.measure(t); next.Components["rag"] != entry.Revision {
		t.Fatal("valid enabled RAG rejected")
	}
	for _, kind := range []string{"missing", "revision", "platform", "arch", "abi", "base"} {
		broken := entry
		switch kind {
		case "revision":
			broken.Revision = strings.Repeat("d", 64)
		case "platform":
			broken.Platform = "wrong"
		case "arch":
			broken.Arch = "wrong"
		case "abi":
			broken.PythonABI = "cp999"
		case "base":
			broken.BaseFingerprint = strings.Repeat("e", 64)
		}
		body, _ := json.Marshal(broken)
		if kind == "missing" {
			_ = os.Remove(filepath.Join(installed, "bundle-manifest.json"))
		} else {
			_ = os.WriteFile(filepath.Join(installed, "bundle-manifest.json"), body, 0600)
		}
		result, err := measureDesktopUpdateState(context.Background(), f.cfg, f.paths)
		if err == nil || result.DataProfile != "" {
			t.Fatalf("accepted invalid manifest %s", kind)
		}
	}
	raw, _ = json.Marshal(entry)
	_ = os.WriteFile(filepath.Join(installed, "bundle-manifest.json"), raw, 0600)
	_ = os.WriteFile(filepath.Join(installed, ".ready"), []byte("corrupt"), 0600)
	result, err := measureDesktopUpdateState(context.Background(), f.cfg, f.paths)
	if err == nil || result.DataProfile != "" {
		t.Fatal("corrupt installed component was treated as never installed")
	}
}

func TestDesktopUpdateStateCLIUsesCurrentOwnerWithoutStartingServices(t *testing.T) {
	f := newUpdateStateFixture(t)
	before, err := os.ReadFile(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	runner := &fakeRunner{t: t}
	cli := NewCLI(&out, io.Discard, runner, filepath.Join(t.TempDir(), "manager"))
	err = cli.Run(context.Background(), []string{"update-state", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot, "--owner-token", f.cfg.OwnerToken})
	if err != nil {
		t.Fatal(err)
	}
	var result desktopupdate.LocalState
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.DataProfile != f.measure(t).DataProfile {
		t.Fatalf("CLI not using measurement: %s", out.String())
	}
	runner.assertCommandCount(0)
	after, err := os.ReadFile(f.paths.StateFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only CLI mutated runtime state")
	}
}

func TestDesktopUpdateStateUsesPersistedResolvedStorage(t *testing.T) {
	for _, family := range []string{"auth", "segments"} {
		t.Run(family, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			before := f.measure(t).DataProfile
			actual := filepath.Join(t.TempDir(), "actual.db")
			db, err := sql.Open("sqlite", actual)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err = db.Exec("CREATE TABLE actual_store (id INTEGER PRIMARY KEY, extra TEXT)"); err != nil {
				t.Fatal(err)
			}
			state, err := readRuntimeState(f.paths.StateFile)
			if err != nil {
				t.Fatal(err)
			}
			if state.Config.UpdateStorage == nil {
				t.Fatal("runtime snapshot missing actual storage identity")
			}
			state.Config.UpdateStorage.Databases[family] = actual
			if family == "auth" {
				state.Config.AuthService.DatabaseURL = sqliteURL(actual)
			}
			if err = writeRuntimeState(f.paths.StateFile, state); err != nil {
				t.Fatal(err)
			}
			measured := f.measure(t).DataProfile
			if measured == before {
				t.Fatal("measured default decoy instead of actual runtime store")
			}
			if _, err = f.databases[family].Exec("ALTER TABLE fixture_records ADD COLUMN decoy TEXT"); err != nil {
				t.Fatal(err)
			}
			if f.measure(t).DataProfile != measured {
				t.Fatal("default decoy influenced measured profile")
			}
			if _, err = db.Exec("ALTER TABLE actual_store ADD COLUMN real_change INTEGER"); err != nil {
				t.Fatal(err)
			}
			if f.measure(t).DataProfile == measured {
				t.Fatal("actual store schema change missed")
			}
		})
	}
}

func TestDesktopUpdateStateTracksOptionalDatabasesWithoutRAG(t *testing.T) {
	f := newUpdateStateFixture(t)
	before := f.measure(t).DataProfile
	for _, family := range []string{"scan", "scan-state"} {
		_ = f.databases[family].Close()
		file := f.paths.ScanDBPath
		if family == "scan-state" {
			file = filepath.Join(f.paths.ScanControlPlaneStateDir, "scan_state.db")
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
	without := f.measure(t).DataProfile
	if without == before {
		t.Fatal("absent optional storage was omitted from data identity")
	}
	state, _ := readRuntimeState(f.paths.StateFile)
	state.Config.Algorithm.RAGDisabled = false
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	if state, err := measureDesktopUpdateState(context.Background(), f.cfg, f.paths); err == nil || state.DataProfile != "" {
		t.Fatal("missing enabled RAG stores accepted")
	}
}

func TestDesktopUpdateStateSnapshotCapturesCustomPathsAtStartup(t *testing.T) {
	f := newUpdateStateFixture(t)
	custom := t.TempDir()
	segments := filepath.Join(t.TempDir(), "segments.db")
	t.Setenv(localSQLiteDirEnvVar, custom)
	t.Setenv("LAZYMIND_SEGMENT_STORE_URI_OR_PATH", segments)
	cfg, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", OwnerToken: f.cfg.OwnerToken, RepoRoot: f.cfg.RepoRoot, ResourcesRoot: f.paths.ResourcesRoot, RuntimeRoot: f.paths.RuntimeRoot}, true)
	if err != nil {
		t.Fatal(err)
	}
	storage := snapshotRuntimeConfig(cfg).UpdateStorage
	if storage == nil || storage.Version != 1 || storage.Databases["core"] != paths.CoreDBPath || storage.Databases["segments"] != segments || storage.Databases["auth"] != paths.AuthServiceDBPath {
		t.Fatalf("missing resolved storage: %+v", storage)
	}
	if !strings.HasPrefix(storage.Databases["core"], custom) {
		t.Fatal("custom SQLite root ignored")
	}
}
