package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// The repair build is installed outside the updater. Its verified identity
// is supplied at the same boundary used by the CLI. Policy fetch, signatures,
// process identity, original storage and persistent adoption are real.
func TestExplicitRepairAfterInterruptedInstalling(t *testing.T) {
	requirePackagedDesktopStorage(t)
	f := actualUpdateFormatFixture(t, 2)
	record := bootstrapRecord(t, f, t.TempDir(), "installing", nil)
	identity, err := readNativeConfiguration(t.Context(), f.paths)
	if err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity.PublicKeys = map[string]string{"fixture": base64.StdEncoding.EncodeToString(pub)}
	identity.ReleaseID, identity.Version = "repair", "1.2.0"
	catalog, err := os.ReadFile(record["componentsPath"].(string))
	if err != nil {
		t.Fatal(err)
	}
	targetCatalog := filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json")
	if err := os.MkdirAll(filepath.Dir(targetCatalog), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetCatalog, catalog, 0600); err != nil {
		t.Fatal(err)
	}
	live, dead := repairTestActors(t)
	boot, err := currentUpdateBootID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scenarios := []string{"accepted-after-exit", "accepted-after-reboot", "already-in-repair", "no-signed-permission", "invalid-signature", "source-still-alive", "installer-still-alive", "same-boot-no-installer"}
	if runtime.GOOS == "darwin" {
		scenarios = append(scenarios, "legacy-observation", "legacy-no-signed-permission", "foreign-session", "foreign-platform", "foreign-transaction")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			var envelope []byte
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(envelope) }))
			defer server.Close()
			prevTransport := http.DefaultTransport
			http.DefaultTransport = server.Client().Transport
			defer func() { http.DefaultTransport = prevTransport }()
			cfg := identity
			cfg.PolicyURL = server.URL + "/policy.json"
			cfg.AllowedOrigins = []string{server.URL, "https://updates.example.invalid"}
			row := maps.Clone(record)
			row["sourceActor"], row["handoffActor"] = dead, dead
			row["componentStage"] = desktopupdate.NativeComponentStage{Status: "prepared", TransactionID: row["transactionId"].(string), ComponentMode: "absent"}
			if scenario == "source-still-alive" {
				row["sourceActor"] = live
			}
			if scenario == "installer-still-alive" {
				row["handoffActor"] = live
			}
			if scenario == "already-in-repair" {
				row["phase"] = "repair"
			}
			newBootProof := scenario == "same-boot-no-installer" || scenario == "accepted-after-reboot"
			if newBootProof {
				delete(row, "handoffActor")
				row["phase"] = "pending"
			} else if scenario == "legacy-observation" || scenario == "legacy-no-signed-permission" || strings.HasPrefix(scenario, "foreign-") {
				delete(row, "handoffActor")
				row["bootId"] = hexDigest([]byte("legacy sysctl display in another timezone"))
			} else if scenario == "accepted-after-exit" {
				row["bootId"] = hexDigest([]byte("legacy sysctl output"))
			}
			now := time.Now().UTC()
			policy := bootstrapPolicyPayload(cfg, row, 42, now)
			oldCfg := cfg
			oldCfg.ReleaseID, oldCfg.Version = "source", "1.0.0"
			before, err := desktopupdate.Check(t.Context(), signBootstrapPolicy(t, private, policy), oldCfg, bootstrapPolicyLocalState(f.measure(t)), root, false, now)
			if err != nil || before.Status != "direct" {
				t.Fatalf("original signed update: %+v %v", before, err)
			}
			row["policyDigest"], row["policySequence"] = before.PolicyDigest, before.PolicySequence
			saveBootstrapRecord(t, root, row)
			tx := row["transactionId"].(string)
			if newBootProof {
				stored := boot
				if scenario == "accepted-after-reboot" {
					stored = previousRepairTestBoot(t, boot)
				}
				if err := nativeCommand(t.Context(), root, "installing", map[string]any{"transactionId": tx, "sourcePaused": true, "sourceActive": 0,
					"dataProfile": row["sourceDataProfile"], "componentMode": "absent", "bootId": stored}); err != nil {
					t.Fatal(err)
				}
			}
			original, err := desktopupdate.ReadNativeRecord(root)
			if err != nil {
				t.Fatal(err)
			}
			releases := policy["releases"].([]any)
			repair := maps.Clone(releases[1].(map[string]any))
			repair["releaseId"], repair["version"] = cfg.ReleaseID, cfg.Version
			policy["policySequence"], policy["releases"] = 43, append(releases, repair)
			if scenario != "no-signed-permission" && scenario != "legacy-no-signed-permission" {
				policy["repairAcceptances"] = []any{map[string]any{"sourceReleaseId": "source", "failedTargetReleaseId": "target", "repairReleaseId": "repair",
					"sourceDataProfile": original.SourceDataProfile, "targetDataProfile": original.SourceDataProfile, "targetComponentsSHA256": original.ComponentsSHA256,
					"componentTransitions": policy["edges"].([]any)[0].(map[string]any)["componentTransitions"]}}
			}
			signingKey := private
			if scenario == "invalid-signature" {
				_, signingKey, err = ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
			}
			envelope = signBootstrapPolicy(t, signingKey, policy)
			fence := filepath.Join(root, "native-v2", tx+".boot-session.json")
			type observation struct {
				TransactionID string `json:"transactionId"`
				BootSession   string `json:"bootSession"`
			}
			writeObservation := func(value observation) {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(fence, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if newBootProof && runtime.GOOS == "darwin" && original.BootID != "" {
				t.Fatal("an older Desktop can misread the new boot proof as a changed legacy hash")
			}
			switch scenario {
			case "foreign-session":
				writeObservation(observation{tx, "darwin-session-v2:8be2b98a-e5ff-402a-8c70-1a4d836f24a3"})
			case "foreign-platform":
				writeObservation(observation{tx, "windows-filetime-v1:131000000000000000"})
			case "foreign-transaction":
				writeObservation(observation{strings.Repeat("f", 32), previousRepairTestBoot(t, boot)})
			case "legacy-observation":
				var first os.FileInfo
				for i := 0; i < 2; i++ {
					got, err := adoptNativeRepair(t.Context(), f.cfg, f.paths, cfg, original, root)
					if got != nil || desktopupdate.ErrorCode(err) != "handoff_restart_required" {
						t.Fatalf("legacy receipt authorized without observing a reboot: %v", err)
					}
					raw, err := os.ReadFile(fence)
					var saved observation
					if err != nil || json.Unmarshal(raw, &saved) != nil || saved != (observation{tx, boot}) {
						t.Fatalf("missing transaction-bound stable boot observation: %v", err)
					}
					info, err := os.Stat(fence)
					if err != nil {
						t.Fatal(err)
					}
					if i == 1 && !os.SameFile(first, info) {
						t.Fatal("same-boot retry replaced its original observation")
					}
					first = info
					reloaded, err := desktopupdate.ReadNativeRecord(root)
					if err != nil || reloaded == nil {
						t.Fatalf("read legacy intent: %v", err)
					}
					expected := *original
					expected.BootID = "" // An older reader must fail closed on a missing process identity.
					if !reflect.DeepEqual(reloaded, &expected) {
						t.Fatal("arming a reboot changed transaction authorization or original bindings")
					}
					original = reloaded
				}
				// Represent an observation written on a genuinely different session:
				// only this historical input is substituted; the current OS probe is real.
				writeObservation(observation{tx, previousRepairTestBoot(t, boot)})
				signed := envelope
				envelope = []byte(`{}`)
				if got, err := adoptNativeRepair(t.Context(), f.cfg, f.paths, cfg, original, root); err == nil || got != nil {
					t.Fatal("a different observed boot bypassed fresh signed repair permission")
				}
				envelope = signed
			}
			got, err := adoptNativeRepair(t.Context(), f.cfg, f.paths, cfg, original, root)
			persisted, readErr := desktopupdate.ReadNativeRecord(root)
			if readErr != nil || persisted == nil {
				t.Fatalf("lost recovery transaction: %v", readErr)
			}
			if persisted.TransactionID != original.TransactionID || persisted.SourceReleaseID != "source" || persisted.TargetReleaseID != "target" {
				t.Fatal("adoption replaced the original transaction")
			}
			if scenario == "accepted-after-exit" || scenario == "accepted-after-reboot" || scenario == "already-in-repair" || scenario == "legacy-observation" {
				if err != nil || got == nil || persisted.Phase != "repair" || persisted.ComponentStage != nil || persisted.RepairTarget == nil || persisted.RepairTarget.ReleaseID != "repair" || persisted.RepairTarget.AcceptanceDigest == "" {
					t.Fatalf("explicit signed repair was not adopted: phase=%s target=%+v err=%v", persisted.Phase, persisted.RepairTarget, err)
				}
				stage := desktopupdate.NativeComponentStage{Status: "prepared", TransactionID: original.TransactionID, ComponentMode: "absent"}
				if err := nativeCommand(t.Context(), root, "prepared", map[string]any{"transactionId": original.TransactionID, "stage": stage}); err != nil {
					t.Fatalf("could not prepare the adopted target: %v", err)
				}
				staged, err := desktopupdate.ReadNativeRecord(root)
				if err != nil || staged == nil || !reflect.DeepEqual(staged.ComponentStage, &stage) {
					t.Fatalf("prepared repair stage did not survive readback: %+v %v", staged, err)
				}
			} else if err == nil || got != nil || !reflect.DeepEqual(persisted, original) {
				t.Fatalf("unsafe repair accepted: %+v %v", got, err)
			}
			if scenario == "legacy-no-signed-permission" {
				if _, err := os.Stat(fence); !os.IsNotExist(err) {
					t.Fatal("unsigned repair armed a legacy reboot observation")
				}
			}
		})
	}
}

func previousRepairTestBoot(t *testing.T, current string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Existing Windows records use SHA-256 of the decimal UTC startup FILETIME.
		return hexDigest([]byte("131000000000000000"))
	}
	const first = "darwin-session-v1:8be2b98a-e5ff-402a-8c70-1a4d836f24a3"
	if current == first {
		return "darwin-session-v1:6a47c786-d793-44f8-8ed0-1fea504f5047"
	}
	return first
}

func TestDesktopBootProofUsesThePlatformSession(t *testing.T) {
	requirePackagedDesktopStorage(t)
	read := func(tz string) string {
		t.Helper()
		t.Setenv("TZ", tz)
		id, err := currentUpdateBootID(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := read("UTC"), read("America/Los_Angeles")
	if a != b {
		t.Fatal("changing timezone altered the boot-session proof without a restart")
	}
	if runtime.GOOS == "windows" {
		cmd := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "(Get-CimInstance Win32_OperatingSystem).LastBootUpTime.ToFileTimeUtc()")
		raw, err := cmd.Output()
		if err != nil || !regexp.MustCompile(`^[0-9]+$`).Match(bytes.TrimSpace(raw)) || a != hexDigest(bytes.TrimSpace(raw)) {
			t.Fatal("Windows proof changed the deployed UTC FILETIME contract")
		}
		return
	}
	raw, err := exec.CommandContext(t.Context(), "/usr/sbin/sysctl", "-n", "kern.bootsessionuuid").Output()
	uuid := strings.ToLower(strings.TrimSpace(string(raw)))
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(uuid) ||
		uuid == "00000000-0000-0000-0000-000000000000" || a != "darwin-session-v1:"+uuid {
		t.Fatal("macOS proof did not identify the current kernel boot session")
	}
}

func repairTestActors(t *testing.T) (desktopupdate.ProcessIdentity, desktopupdate.ProcessIdentity) {
	t.Helper()
	live, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestNativeRepairExitedOwnerChild$")
	child.Env = append(os.Environ(), "LAZYMIND_TEST_NATIVE_REPAIR_OWNER=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	exited, err := desktopupdate.IdentifyProcess(child.Process.Pid)
	_ = child.Process.Kill()
	_ = child.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return live, exited
}

func TestInterruptedInstallingCannotSkipHandoffOrStorageProof(t *testing.T) {
	f := newUpdateStateFixtureLayout(t)
	identity := desktopupdate.Config{TrustDomain: "fixture", Audience: "official", Channel: "stable",
		SigningIdentity: "fixture-team", ReleaseID: "repair", Version: "1.2.0", Platform: "darwin", Arch: "arm64"}
	live, exited := repairTestActors(t)
	for _, phase := range []string{"repair", "installing"} {
		for _, scenario := range []string{"source-alive", "handoff-alive", "storage-mismatch"} {
			t.Run(phase+"/"+scenario, func(t *testing.T) {
				record := &desktopupdate.NativeRecord{Phase: phase, TrustDomain: identity.TrustDomain, Audience: identity.Audience, Channel: identity.Channel,
					SigningIdentity: identity.SigningIdentity, Platform: identity.Platform, Arch: identity.Arch,
					RuntimeRoot: f.paths.RuntimeRoot, InstallationRoot: f.paths.ResourcesRoot, SourceActor: &exited, HandoffActor: &exited}
				want := "handoff_state_unknown"
				switch scenario {
				case "source-alive":
					record.SourceActor = &live
				case "handoff-alive":
					record.HandoffActor = &live
				case "storage-mismatch":
					want = "update_storage_mismatch"
				}
				got, err := adoptNativeRepair(t.Context(), f.cfg, f.paths, identity, record, t.TempDir())
				if got != nil || desktopupdate.ErrorCode(err) != want {
					t.Fatalf("got %v, want %s", err, want)
				}
			})
		}
	}
}

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
	if err := desktopupdate.RegisterCoordinatorScope(t.Context(), root, scope, f.cfg.OwnerToken, actor); err != nil {
		t.Fatal(err)
	}
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

func TestNativeRepairCorruptIntentBlocksInspectAndInternalCore(t *testing.T) {
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
	args := []string{"--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--runtime-root", f.paths.RuntimeRoot, "--resources-root", f.paths.ResourcesRoot, "--owner-token", f.cfg.OwnerToken}
	if err := cli.Run(t.Context(), append([]string{"update-bootstrap", "--action", "inspect"}, args...)); err != nil {
		t.Fatal(err)
	}
	var response struct{ Status, Reason string }
	if err := json.Unmarshal(out.Bytes(), &response); err != nil || response.Status != "blocked" || response.Reason != "update_record_read" {
		t.Fatalf("inspect did not reject the corrupt native intent: %s (%v)", out.String(), err)
	}
	err := cli.Run(t.Context(), append([]string{"internal", "core-run"}, args...))
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
