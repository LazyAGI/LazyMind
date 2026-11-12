package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCommittedUpdateRestartKeepsResolvedStorage(t *testing.T) {
	requirePackagedDesktopStorage(t)
	f := actualUpdateFormatFixture(t, 2)
	root := t.TempDir()
	old := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
	// Frozen source layout includes a real external segment DB and vector path.
	oldSegments := f.cfg.UpdateStorage.Databases["segments"]
	if err := f.databases["segments"].Close(); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(t.TempDir(), "custom-segments.db")
	if err := os.Rename(oldSegments, custom); err != nil {
		t.Fatal(err)
	}
	f.cfg.UpdateStorage.Databases["segments"] = custom
	t.Setenv("LAZYMIND_SEGMENT_STORE_URI_OR_PATH", custom)
	if captureUpdateStorage(f.paths, f.cfg.AuthService.DatabaseURL).Databases["segments"] != custom || localSegmentStoreBackingPath(f.paths) != custom {
		t.Fatal("source custom segment layout is not producer-resolved")
	}
	vector := filepath.Join(t.TempDir(), "custom-vector")
	if err := os.MkdirAll(vector, 0700); err != nil {
		t.Fatal(err)
	}
	f.cfg.ModeProfile.VectorStore.DBPath = vector
	t.Setenv(localMilvusLiteDBPathEnvVar, vector)
	sourceState := defaultRuntimeState(f.cfg, f.cfg.ProcessComposePort, f.paths.RunDirTokenFile)
	sourceState.OverallStatus = "ready"
	if err := writeRuntimeState(f.paths.StateFile, sourceState); err != nil {
		t.Fatal(err)
	}
	// Complete the fixed transaction, preserving the normal runtime receipt.
	record := bootstrapRecord(t, f, root, "activated", nil)
	bound, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		t.Fatal(err)
	}
	state := defaultRuntimeState(f.cfg, 0, f.paths.RunDirTokenFile)
	state.Config.UpdateStorage = nativeStorageSnapshot(bound)
	state.OverallStatus = "stopped"
	state.ProcessCompose.APIPort = 0
	state.Config.UpdateBootstrap = nil
	state.Config.NativeCommittedTx = record["transactionId"].(string)
	state.Config.NativeCommittedReleaseID = "target"
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(desktopupdate.NativeRecordPath(root)); err != nil {
		t.Fatal(err)
	}
	// Ordinary relaunch resolves its RuntimeRoot but no longer has source env.
	t.Setenv("LAZYMIND_SEGMENT_STORE_URI_OR_PATH", "")
	t.Setenv(localMilvusLiteDataDirEnvVar, "")
	t.Setenv(localMilvusLiteDBPathEnvVar, "")
	cfg, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", OwnerToken: f.cfg.OwnerToken, RepoRoot: f.cfg.RepoRoot, ResourcesRoot: f.paths.ResourcesRoot, RuntimeRoot: f.paths.RuntimeRoot}, true)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewRuntimeManager(&fakeRunner{t: t}, "fixture-manager")
	no := func(int, time.Duration) bool { return false }
	manager.probeAPI = no
	manager.runtimeReady = func(context.Context, RuntimeConfig, RuntimePaths) bool { return false }
	manager.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
	reached := false
	manager.relocatePythonVenvs = func(actual RuntimeConfig, p RuntimePaths) error {
		reached = true
		t.Logf("restart segments=%s vector=%s; want segments=%s vector=%s", localSegmentStoreBackingPath(p), actual.ModeProfile.VectorStore.DBPath, custom, vector)
		if localSegmentStoreBackingPath(p) != custom || actual.ModeProfile.VectorStore.DBPath != vector {
			t.Error("completed restart reset frozen custom storage to defaults")
		}
		return errors.New("test barrier after fresh startup configuration")
	}
	err = manager.Up(t.Context(), cfg, paths)
	if !reached {
		t.Fatalf("did not reach actual fresh startup configuration: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "test barrier") {
		t.Fatalf("unexpected startup result %v", err)
	}
}

func newCommittedStorageFixture(t *testing.T) (*updateStateFixture, *UpdateStorageSnapshot) {
	requirePackagedDesktopStorage(t)
	t.Helper()
	f := actualUpdateFormatFixture(t, 2)
	root := t.TempDir()
	old := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
	storage := cloneUpdateStorage(f.cfg.UpdateStorage)
	directory := t.TempDir()
	for family, db := range f.databases {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(directory, family, "retained.db")
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(storage.Databases[family], destination); err != nil {
			t.Fatal(err)
		}
		storage.Databases[family] = destination
	}
	storage.VectorEngine = "milvus-lite"
	storage.VectorPath = filepath.Join(directory, "retained-vector")
	storage.LayoutSource = "resolved-runtime"
	if err := os.MkdirAll(storage.VectorPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := restoreRuntimeStorage(&f.cfg, &f.paths, storage); err != nil {
		t.Fatal(err)
	}
	source := defaultRuntimeState(f.cfg, 0, f.paths.RunDirTokenFile)
	source.OverallStatus = "ready"
	if err := writeRuntimeState(f.paths.StateFile, source); err != nil {
		t.Fatal(err)
	}
	record := bootstrapRecord(t, f, root, "activated", nil)
	bound, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		t.Fatal(err)
	}
	storage = nativeStorageSnapshot(bound)
	state := defaultRuntimeState(f.cfg, 0, f.paths.RunDirTokenFile)
	state.Config.UpdateStorage = cloneUpdateStorage(storage)
	state.OverallStatus = "stopped"
	state.ProcessCompose.APIPort = 0
	state.Config.NativeCommittedTx = record["transactionId"].(string)
	state.Config.NativeCommittedReleaseID = "target"
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(desktopupdate.NativeRecordPath(root)); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"LAZYMIND_SEGMENT_STORE_URI_OR_PATH", localSQLiteDirEnvVar, authServiceDatabaseURLEnvVar, localMilvusLiteDataDirEnvVar, localMilvusLiteDBPathEnvVar, "LAZYMIND_STATE_SQLITE_PATH", "LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION", "LAZYMIND_UPDATE_BOOTSTRAP_OWNER", "LAZYMIND_UPDATE_BOOTSTRAP_STORAGE"} {
		t.Setenv(key, "")
	}
	return f, storage
}

func assertCommittedStorage(t *testing.T, cfg RuntimeConfig, paths RuntimePaths, want *UpdateStorageSnapshot) {
	t.Helper()
	actual := map[string]string{"core": paths.CoreDBPath, "auth": updateAuthSQLitePath(cfg.AuthService.DatabaseURL), "lazyllm": paths.LazyLLMDBPath, "segments": localSegmentStoreBackingPath(paths), "channel": paths.ChannelGatewayDBPath, "scan": paths.ScanDBPath}
	for family, path := range actual {
		if path != want.Databases[family] {
			t.Errorf("%s reset to %s; want %s", family, path, want.Databases[family])
		}
	}
	if cfg.UpdateStorage == nil || !reflect.DeepEqual(cfg.UpdateStorage.Databases, want.Databases) || cfg.ModeProfile.VectorStore.DBPath != want.VectorPath {
		t.Errorf("lost durable nine-database/vector snapshot: %+v", cfg.UpdateStorage)
	}
	if cfg.UpdateBootstrap != nil {
		t.Error("completed storage re-sealed bootstrap")
	}
	for family, env := range map[string][]string{"core-state": coreServiceEnv(cfg, paths), "scan-state": scanControlPlaneEnv(cfg, paths)} {
		if got := committedEnvValue(env, "LAZYMIND_STATE_SQLITE_PATH"); got != want.Databases[family] {
			t.Errorf("%s actual service env=%q; want %q", family, got, want.Databases[family])
		}
	}
	auth, err := authServiceEnv(cfg, paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := committedEnvValue(auth, "LAZYMIND_STATE_SQLITE_PATH"); got != want.Databases["auth-state"] {
		t.Errorf("auth-state actual service env=%q; want %q", got, want.Databases["auth-state"])
	}
}
func committedEnvValue(env []string, key string) string {
	value := ""
	for _, item := range env {
		if strings.HasPrefix(item, key+"=") {
			value = strings.TrimPrefix(item, key+"=")
		}
	}
	return value
}

func TestCommittedUpdateNineDatabaseStorageSurvivesStartupRetry(t *testing.T) {
	f, storage := newCommittedStorageFixture(t)
	cfg, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", OwnerToken: f.cfg.OwnerToken, RepoRoot: f.cfg.RepoRoot, ResourcesRoot: f.paths.ResourcesRoot, RuntimeRoot: f.paths.RuntimeRoot}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.ProcessComposeBin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ProcessComposeBin, []byte("unused fake-runner binary"), 0700); err != nil {
		t.Fatal(err)
	}
	upCalls := 0
	runner := &committedStorageRunner{run: func(cmd Command) (CommandResult, error) {
		if !slices.Contains(cmd.Args, "up") {
			return CommandResult{}, nil
		}
		upCalls++
		state, err := readRuntimeState(paths.StateFile)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(state.Config.UpdateStorage.Databases, storage.Databases) || state.Config.ModeProfile.VectorStore.DBPath != storage.VectorPath || state.Config.UpdateBootstrap != nil {
			t.Error("startup attempt overwrote committed storage")
		}
		if upCalls == 1 {
			return CommandResult{}, &startupPortConflictError{Service: processComposeServiceName, Port: cfg.ProcessComposePort, Cause: errors.New("fixture occupied port")}
		}
		return CommandResult{}, errors.New("test barrier at second startup attempt")
	}}
	manager := NewRuntimeManager(runner, "fixture-manager")
	manager.probeAPI = func(int, time.Duration) bool { return false }
	manager.runtimeReady = func(context.Context, RuntimeConfig, RuntimePaths) bool { return false }
	manager.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
	relocated := false
	manager.relocatePythonVenvs = func(actual RuntimeConfig, p RuntimePaths) error {
		relocated = true
		assertCommittedStorage(t, actual, p, storage)
		return nil
	}
	err = manager.Up(t.Context(), cfg, paths)
	if !relocated || upCalls != 2 || err == nil || !strings.Contains(err.Error(), "test barrier at second startup attempt") {
		t.Fatalf("did not verify retry path: relocated=%v attempts=%d err=%v", relocated, upCalls, err)
	}
}

type committedStorageRunner struct {
	run func(Command) (CommandResult, error)
}

func (r *committedStorageRunner) Run(_ context.Context, cmd Command) (CommandResult, error) {
	return r.run(cmd)
}

func TestCommittedUpdateStorageSQLiteChild(t *testing.T) {
	raw := os.Getenv("LAZYMIND_TEST_COMMITTED_STORAGE_ARGS")
	if raw == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("LAZYMIND_TEST_COMMITTED_STORAGE_ROOT")
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	go func() { var b [1]byte; _, _ = os.Stdin.Read(b[:]); cancel() }()
	cli := NewCLI(os.Stdout, os.Stderr, &fakeRunner{t: t}, os.Args[0])
	if err := cli.Run(ctx, args); err != nil {
		t.Fatal(err)
	}
}

func TestCommittedUpdateStorageReachesRealSQLiteChild(t *testing.T) {
	f, storage := newCommittedStorageFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.SQLiteServerPort = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	state, err := readRuntimeState(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	state.Config.SQLiteServerPort = f.cfg.SQLiteServerPort
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.paths.RunDirTokenFile, []byte(sqliteTestToken), 0600); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal([]string{"internal", "sqlite-server-run", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot, "--owner-token", f.cfg.OwnerToken})
	root, err := resolveRuntimeRecoveryRoot()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestCommittedUpdateStorageSQLiteChild$")
	command.Env = append(append(os.Environ(), runtimeCommandEnv(f.paths, f.cfg)...), "LAZYMIND_TEST_COMMITTED_STORAGE_ARGS="+string(args), "LAZYMIND_TEST_COMMITTED_STORAGE_ROOT="+root)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	defer func() {
		input.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("SQLite child failed: %v %s", err, output.String())
			}
		case <-time.After(5 * time.Second):
			command.Process.Kill()
			<-done
			t.Error("SQLite child did not close")
		}
	}()
	base := "http://127.0.0.1:" + strconv.Itoa(f.cfg.SQLiteServerPort)
	if err := waitForHTTPOnly(t.Context(), f.cfg.SQLiteServerPort, sqliteServerHealthPath, "fixture SQLite child", 8*time.Second); err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"core", "lazyllm", "segments"} {
		response := postSQLiteTestRequest(t, base, "/v1/query", sqliteRequest{DB: family, SQL: "SELECT file FROM pragma_database_list WHERE name='main'"})
		if len(response.Rows) != 1 || len(response.Rows[0]) != 1 {
			t.Fatalf("bad database_list for %s: %+v", family, response)
		}
		actual, err1 := os.Stat(response.Rows[0][0].Text)
		want, err2 := os.Stat(storage.Databases[family])
		if err1 != nil || err2 != nil || !os.SameFile(actual, want) {
			t.Errorf("actual child opened wrong %s file: %q; want %q", family, response.Rows[0][0].Text, storage.Databases[family])
		}
	}
}

func TestCommittedUpdateStorageReceiptSurvivesOwnerReplacement(t *testing.T) {
	f, storage := newCommittedStorageFixture(t)
	before, err := readRuntimeState(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", OwnerToken: "fixture-next-owner", RepoRoot: f.cfg.RepoRoot, ResourcesRoot: f.paths.ResourcesRoot, RuntimeRoot: f.paths.RuntimeRoot}, true)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewRuntimeManager(&fakeRunner{t: t}, "fixture-manager")
	manager.probeAPI = func(int, time.Duration) bool { return false }
	manager.runtimeReady = func(context.Context, RuntimeConfig, RuntimePaths) bool { return false }
	manager.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
	manager.relocatePythonVenvs = func(actual RuntimeConfig, p RuntimePaths) error {
		assertCommittedStorage(t, actual, p, storage)
		state, err := readRuntimeState(p.StateFile)
		if err != nil {
			t.Fatal(err)
		}
		if state.OwnerToken != cfg.OwnerToken || state.Config.NativeCommittedTx != before.Config.NativeCommittedTx || state.Config.NativeCommittedReleaseID != before.Config.NativeCommittedReleaseID {
			t.Error("owner replacement erased committed storage authority")
		}
		return errors.New("test barrier after owner replacement")
	}
	if err := manager.Up(t.Context(), cfg, paths); err == nil || !strings.Contains(err.Error(), "test barrier") {
		t.Fatal(err)
	}
}

func TestCommittedUpdateStorageRejectsConflictsBeforeStartup(t *testing.T) {
	for _, scenario := range []string{"auth-override", "segment-override", "vector-override", "missing-family", "duplicate-family", "relative-vector", "wrong-scope"} {
		t.Run(scenario, func(t *testing.T) {
			f, _ := newCommittedStorageFixture(t)
			state, err := readRuntimeState(f.paths.StateFile)
			if err != nil {
				t.Fatal(err)
			}
			want := "update_storage_mismatch"
			switch scenario {
			case "auth-override":
				t.Setenv(authServiceDatabaseURLEnvVar, "sqlite:///"+filepath.ToSlash(filepath.Join(t.TempDir(), "wrong.db")))
			case "segment-override":
				t.Setenv("LAZYMIND_SEGMENT_STORE_URI_OR_PATH", filepath.Join(t.TempDir(), "wrong.db"))
			case "vector-override":
				t.Setenv(localMilvusLiteDBPathEnvVar, t.TempDir())
			case "missing-family":
				delete(state.Config.UpdateStorage.Databases, "auth")
			case "duplicate-family":
				state.Config.UpdateStorage.Databases["auth"] = state.Config.UpdateStorage.Databases["core"]
				want = "unsupported_storage"
			case "relative-vector":
				state.Config.UpdateStorage.VectorPath = "relative-index"
			case "wrong-scope":
				state.ResourcesRoot = t.TempDir()
				want = "runtime_owner_mismatch"
			}
			if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(f.paths.StateFile)
			if err != nil {
				t.Fatal(err)
			}
			cfg, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", OwnerToken: f.cfg.OwnerToken, RepoRoot: f.cfg.RepoRoot, ResourcesRoot: f.paths.ResourcesRoot, RuntimeRoot: f.paths.RuntimeRoot}, true)
			if err != nil {
				t.Fatal(err)
			}
			m := NewRuntimeManager(&fakeRunner{t: t}, "fixture-manager")
			probed := false
			m.probeAPI = func(int, time.Duration) bool { probed = true; return false }
			err = m.Up(t.Context(), cfg, paths)
			if desktopupdate.ErrorCode(err) != want || probed {
				t.Fatalf("conflict crossed startup: err=%v probed=%v", err, probed)
			}
			after, err := os.ReadFile(f.paths.StateFile)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("conflict rewrote persisted configuration")
			}
		})
	}
}

func TestCommittedUpdateStorageChildRejectsForeignOwner(t *testing.T) {
	f, _ := newCommittedStorageFixture(t)
	cfg, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", OwnerToken: "foreign-fixture-owner", RepoRoot: f.cfg.RepoRoot, ResourcesRoot: f.paths.ResourcesRoot, RuntimeRoot: f.paths.RuntimeRoot}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := inheritCommittedRuntimeStorage(&cfg, &paths); desktopupdate.ErrorCode(err) != "runtime_owner_mismatch" {
		t.Fatalf("foreign child acquired committed paths: %v", err)
	}
}

func requirePackagedDesktopStorage(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("signed packaged Desktop fixtures support macOS and Windows")
	}
}
