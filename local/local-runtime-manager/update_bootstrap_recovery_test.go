package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestNativeRepairCommittedReceiptReachedFromNormalUp(t *testing.T) {
	f := newUpdateStateFixture(t)
	root := t.TempDir()
	old := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
	state, err := readRuntimeState(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	state.Config.UpdateBootstrap = &RuntimeUpdateBootstrap{TransactionID: strings.Repeat("a", 32), Owner: f.cfg.OwnerToken, ReleaseID: "target", Storage: cloneUpdateStorage(f.cfg.UpdateStorage), ExpectedPeers: []string{"core", "scan"}}
	state.Config.NativeCommittedTx = state.Config.UpdateBootstrap.TransactionID
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{t: t}
	manager := NewRuntimeManager(runner, "fixture-manager")
	manager.probeAPI = func(int, time.Duration) bool { return true }
	manager.runtimeReady = func(context.Context, RuntimeConfig, RuntimePaths) bool { return true }
	manager.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
	err = manager.Up(t.Context(), f.cfg, f.paths)
	state, readErr := readRuntimeState(f.paths.StateFile)
	t.Logf("normal Up returned %v; committedTx=%s bootstrapPresent=%v", err, state.Config.NativeCommittedTx, state.Config.UpdateBootstrap != nil)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err == nil && state.Config.UpdateBootstrap != nil {
		t.Fatal("normal Up reports ready without attempting outstanding committed bootstrap release")
	}
}

func TestNativeRepairCommittedBootstrapRecoversThroughActualInspect(t *testing.T) {
	requirePackagedDesktopStorage(t)
	f := actualUpdateFormatFixture(t, 2)
	root := t.TempDir()
	old := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
	record := bootstrapRecord(t, f, root, "activated", nil)
	bound, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := readRuntimeState(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	tx := record["transactionId"].(string)
	state.Config.UpdateBootstrap = &RuntimeUpdateBootstrap{TransactionID: tx, Owner: f.cfg.OwnerToken, ReleaseID: "target", DataProfile: record["sourceDataProfile"].(string), Storage: nativeStorageSnapshot(bound), ExpectedPeers: []string{"core"}}
	state.Config.NativeCommittedTx, state.Config.NativeCommittedReleaseID = tx, "target"
	var releases int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Action, TransactionID, Owner string }
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Action != "bootstrap-complete" || request.TransactionID != tx || request.Owner != f.cfg.OwnerToken {
			t.Error("wrong committed peer release")
		}
		releases++
		if releases == 1 {
			// The peer may have released before its response was lost.
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"active": 0})
	}))
	defer server.Close()
	port, _ := strconv.Atoi(strings.Split(server.URL, ":")[2])
	state.Config.LocalProxy.CoreHostPort = port
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(desktopupdate.NativeRecordPath(root)); err != nil {
		t.Fatal(err)
	}
	scope, err := runtimeUpdateScope(f.cfg, f.paths)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := desktopupdate.BeginStartup(t.Context(), root, scope, f.cfg.OwnerToken, "coordinator", actor)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	t.Setenv(runtimeRootEnvVar, "")
	var output bytes.Buffer
	cli := NewCLI(&output, &output, &fakeRunner{t: t}, "fixture-manager")
	args := []string{"update-bootstrap", "--action", "inspect", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--owner-token", f.cfg.OwnerToken}
	for attempt := 0; attempt < 2; attempt++ {
		if attempt == 1 {
			manager := NewRuntimeManager(&fakeRunner{t: t}, "fixture-manager")
			manager.probeAPI = func(int, time.Duration) bool { return true }
			manager.runtimeReady = func(context.Context, RuntimeConfig, RuntimePaths) bool { return true }
			manager.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
			if err := manager.Up(t.Context(), f.cfg, f.paths); err != nil {
				t.Fatal(err)
			}
		}
		output.Reset()
		if err := cli.Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
		var response struct{ Status, RuntimeRoot string }
		if json.Unmarshal(output.Bytes(), &response) != nil {
			t.Fatalf("invalid inspect: %s", output.String())
		}
		if attempt == 0 && response.Status != "blocked" || attempt == 1 && (response.Status != "allowed" || response.RuntimeRoot != f.paths.RuntimeRoot) {
			t.Fatalf("lost-response recovery: %+v", response)
		}
		got, err := readRuntimeState(f.paths.StateFile)
		if err != nil || (got.Config.UpdateBootstrap != nil) != (attempt == 0) {
			t.Fatalf("wrong durable bootstrap after release: %+v %v", got.Config.UpdateBootstrap, err)
		}
	}
	if releases != 2 {
		t.Fatalf("peer releases=%d", releases)
	}
	// Completed custom roots remain discoverable on the next normal relaunch.
	opts, err := nativeRuntimeOptions(t.Context(), RuntimeConfigOptions{Profile: "desktop", ResourcesRoot: f.paths.ResourcesRoot})
	if err != nil || opts.RuntimeRoot != f.paths.RuntimeRoot {
		t.Fatalf("lost committed custom root: %+v %v", opts, err)
	}
}

func TestNativeRepairExitedOwnerChild(t *testing.T) {
	if os.Getenv("LAZYMIND_TEST_NATIVE_REPAIR_OWNER") == "1" {
		time.Sleep(30 * time.Second)
	}
}

func TestNativeRepairCommittedBootstrapCanStopDeadOwner(t *testing.T) {
	requirePackagedDesktopStorage(t)
	f := actualUpdateFormatFixture(t, 2)
	root := t.TempDir()
	old := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
	record := bootstrapRecord(t, f, root, "activated", nil)
	bound, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestNativeRepairExitedOwnerChild$")
	child.Env = append(os.Environ(), "LAZYMIND_TEST_NATIVE_REPAIR_OWNER=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	actor, err := desktopupdate.IdentifyProcess(child.Process.Pid)
	_ = child.Process.Kill()
	_ = child.Wait()
	if err != nil {
		t.Fatal(err)
	}
	state, err := readRuntimeState(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	tx := record["transactionId"].(string)
	state.Config.UpdateBootstrap = &RuntimeUpdateBootstrap{TransactionID: tx, Owner: f.cfg.OwnerToken, ReleaseID: "target", DataProfile: record["sourceDataProfile"].(string), Storage: nativeStorageSnapshot(bound), ExpectedPeers: []string{"core"}, Actor: &actor}
	state.Config.NativeCommittedTx, state.Config.NativeCommittedReleaseID = tx, "target"
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(desktopupdate.NativeRecordPath(root)); err != nil {
		t.Fatal(err)
	}
	f.cfg.OwnerToken = "fixture-restarted-owner"
	manager := NewRuntimeManager(&fakeRunner{t: t}, "fixture-manager")
	no := func(int, time.Duration) bool { return false }
	manager.probeAPI, manager.probeCore, manager.probeScan, manager.probeAuth = no, no, no, no
	manager.probeFrontend, manager.probeLocalProxy, manager.probeChannelGateway = no, no, no
	manager.probeFileWatch, manager.probeSQLiteServer = no, no
	manager.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
	if err := recoverCommittedBootstrap(t.Context(), manager, &f.cfg, &f.paths); err != nil {
		t.Fatal(err)
	}
	got, err := readRuntimeState(f.paths.StateFile)
	if err != nil || got.Config.UpdateBootstrap != nil || got.OverallStatus != "stopped" {
		t.Fatalf("dead-owner recovery did not stop sealed runtime: %+v %v", got, err)
	}
}

func TestNativeRepairInternalCoreCannotIgnoreCorruptNativeIntent(t *testing.T) {
	f := newUpdateStateFixture(t)
	root := t.TempDir()
	old := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
	filename := filepath.Join(root, "native-v2", "active.json")
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cli := NewCLI(&out, &out, &fakeRunner{t: t}, "fixture-manager")
	err := cli.Run(t.Context(), []string{"internal", "core-run", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--runtime-root", f.paths.RuntimeRoot, "--resources-root", f.paths.ResourcesRoot, "--owner-token", f.cfg.OwnerToken})
	t.Logf("internal core-run returned %v", err)
	if desktopupdate.ErrorCode(err) != "update_record_read" {
		t.Fatalf("internal Core did not consume the corrupt native intent: %v", err)
	}
}

func TestNativeRepairInternalCorePreservesVerifiedBootstrap(t *testing.T) {
	requirePackagedDesktopStorage(t)
	f := actualUpdateFormatFixture(t, 2)
	root := t.TempDir()
	old := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
	record := bootstrapRecord(t, f, root, "activated", nil)
	record["owner"] = f.cfg.OwnerToken
	saveBootstrapRecord(t, root, record)
	bound, err := desktopupdate.ReadNativeRecord(root)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := json.Marshal(nativeStorageSnapshot(bound))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION", bound.TransactionID)
	t.Setenv("LAZYMIND_UPDATE_BOOTSTRAP_OWNER", f.cfg.OwnerToken)
	t.Setenv("LAZYMIND_UPDATE_BOOTSTRAP_STORAGE", string(storage))
	var output bytes.Buffer
	cli := NewCLI(&output, &output, &fakeRunner{t: t}, "fixture-manager")
	err = cli.Run(t.Context(), []string{"internal", "core-run", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot, "--owner-token", f.cfg.OwnerToken})
	if err == nil || !strings.Contains(err.Error(), "desktop core binary not found") {
		t.Fatalf("verified child failed before actual Core boundary: %v", err)
	}
}

func TestNativeRepairDirectServiceActionsCheckIntentBeforeWriting(t *testing.T) {
	for _, action := range []string{"start", "build"} {
		t.Run(action, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			root := t.TempDir()
			old := resolveRuntimeRecoveryRoot
			resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
			t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
			filename := desktopupdate.NativeRecordPath(root)
			if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, []byte("{broken"), 0600); err != nil {
				t.Fatal(err)
			}
			runner := &fakeRunner{t: t}
			manager := NewRuntimeManager(runner, "fixture-manager")
			err := manager.RunServiceAction(t.Context(), f.cfg, f.paths, fileWatcherProcessName, action)
			if desktopupdate.ErrorCode(err) != "update_record_read" || len(runner.calls) != 0 {
				t.Fatalf("direct service %s crossed intent: err=%v commands=%d", action, err, len(runner.calls))
			}
		})
	}
}

func TestNativeRepairResetAndWarmupDoNotActOnCorruptIntent(t *testing.T) {
	for _, action := range []string{"reset", "warmup"} {
		t.Run(action, func(t *testing.T) {
			f := newUpdateStateFixture(t)
			root := t.TempDir()
			old := resolveRuntimeRecoveryRoot
			resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
			t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
			filename := desktopupdate.NativeRecordPath(root)
			_ = os.MkdirAll(filepath.Dir(filename), 0700)
			if err := os.WriteFile(filename, []byte("{broken"), 0600); err != nil {
				t.Fatal(err)
			}
			m := NewRuntimeManager(&fakeRunner{t: t}, "fixture-manager")
			probed := false
			m.probeAPI = func(int, time.Duration) bool { probed = true; panic("test barrier: unexpected runtime shutdown") }
			var err error
			func() {
				defer func() { _ = recover() }()
				if action == "reset" {
					err = m.Reset(t.Context(), f.cfg, f.paths, ResetScopeAll)
				} else {
					f.cfg.MaintenanceMode = installerWarmupMaintenanceMode
					err = m.Warmup(t.Context(), f.cfg, f.paths)
				}
			}()
			if probed || desktopupdate.ErrorCode(err) != "update_record_read" {
				t.Fatalf("%s crossed corrupt intent: probed=%v err=%v", action, probed, err)
			}
		})
	}
}

func TestNativeRepairNativeGuardMustRecognizeSymlinkAliases(t *testing.T) {
	requirePackagedDesktopStorage(t)
	f := actualUpdateFormatFixture(t, 2)
	root := t.TempDir()
	old := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = old })
	bootstrapRecord(t, f, root, "installing", nil)
	aliasRoot := t.TempDir()
	resourcesAlias, runtimeAlias := filepath.Join(aliasRoot, "application-alias"), filepath.Join(aliasRoot, "data-alias")
	if err := os.Symlink(f.paths.ResourcesRoot, resourcesAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.paths.RuntimeRoot, runtimeAlias); err != nil {
		t.Fatal(err)
	}
	cfg, paths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", OwnerToken: f.cfg.OwnerToken, RepoRoot: f.cfg.RepoRoot, ResourcesRoot: resourcesAlias, RuntimeRoot: runtimeAlias}, true)
	if err != nil {
		t.Fatal(err)
	}
	// Both paths resolve to the existing installing transaction's actual scope.
	if !sameDirectory(t, paths.RuntimeRoot, f.paths.RuntimeRoot) || !sameDirectory(t, paths.ResourcesRoot, f.paths.ResourcesRoot) {
		t.Fatal("bad alias fixture")
	}
	err = bindNativeStartup(t.Context(), &cfg, &paths, "", cfg.OwnerToken)
	t.Logf("normal startup permission through same-installation and same-data aliases returned %v", err)
	if err == nil {
		t.Fatal("symlink aliases bypassed the active installing intent")
	}
}
