package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

// manager_test.go's TestMain isolates HOME. Preserve the existing tool cache
// before that change so cross-module fixture builds reuse the caller's cache.
var bootstrapGoModuleCache, bootstrapGoModuleCacheError = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "env", "GOMODCACHE").Output()
	return strings.TrimSpace(string(raw)), err
}()

// This fixture supplies the trusted helper's config boundary, not a successful
// signature or packaged-app identity claim. Real policy signatures have their
// own tests; the CLI, path binding, service env and OS processes remain real.
func bootstrapRecord(t *testing.T, f *updateStateFixture, root, phase string, targetCatalog []byte) map[string]any {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	configuration := desktopupdate.Config{TrustDomain: "fixture-domain", Audience: "official", Channel: "stable", Mode: "official",
		ReleaseID: "target", Version: "1.1.0", Platform: runtime.GOOS, Arch: runtime.GOARCH, SigningIdentity: "fixture", UpdaterProtocol: 2,
		PolicyURL: "https://updates.example.invalid/policy.json", AllowedOrigins: []string{"https://updates.example.invalid"}, PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(pub)}}
	if phase == "pending" {
		configuration.ReleaseID, configuration.Version = "source", "1.0.0"
	}
	configJSON, _ := json.Marshal(map[string]any{"status": "enabled", "config": configuration})
	helper := filepath.Join(f.paths.ResourcesRoot, "bin", executableName("lazymind-update-helper"))
	if err := os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "helper.go")
	if err := os.WriteFile(source, []byte("package main\nimport \"os\"\nfunc main(){os.Stdout.Write([]byte("+strconv.Quote(string(configJSON))+"))}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", helper, source).CombinedOutput(); err != nil {
		t.Fatalf("build helper boundary: %v %s", err, output)
	}
	scope, err := runtimeUpdateScope(f.cfg, f.paths)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := desktopupdate.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	state := bootstrapPolicyLocalState(f.measure(t))
	sourceComponent := bootstrapSelectedComponent(t, f, state.Components["rag"])
	storageRaw, _ := json.Marshal(f.cfg.UpdateStorage)
	var storage map[string]any
	_ = json.Unmarshal(storageRaw, &storage)
	storage["vectorEngine"], storage["vectorPath"], storage["layoutSource"], storage["optionalAbsent"] = "milvus-lite", f.cfg.ModeProfile.VectorStore.DBPath, "accepted-source-runtime", []string{}
	record := map[string]any{"version": 2, "transactionId": strings.Repeat("1", 32), "phase": phase,
		"sourceReleaseId": "source", "sourceVersion": "1.0.0", "targetReleaseId": "target", "targetVersion": "1.1.0",
		"trustDomain": configuration.TrustDomain, "audience": configuration.Audience, "channel": "stable", "signingIdentity": configuration.SigningIdentity,
		"platform": runtime.GOOS, "arch": runtime.GOARCH, "runtimeRoot": f.paths.RuntimeRoot, "installationRoot": f.paths.ResourcesRoot, "scope": scope,
		"sourceDataProfile": state.DataProfile, "targetDataProfile": state.DataProfile, "sourceStorage": storage,
		"sourceHadRAG": sourceComponent != nil, "componentMode": "absent", "expectedPeers": bootstrapPeerNames(f.cfg),
		"policyDigest": strings.Repeat("c", 64), "policySequence": 42, "sourceOwnerDigest": hexDigest([]byte(f.cfg.OwnerToken)), "sourceActor": actor, "handoffActor": actor}
	if targetCatalog == nil {
		if sourceComponent != nil {
			targetCatalog, err = os.ReadFile(filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json"))
		} else {
			targetCatalog, err = json.Marshal(map[string]any{"schemaVersion": 1, "platform": runtime.GOOS, "arch": runtime.GOARCH, "components": map[string]any{"rag": map[string]any{
				"id": "rag", "revision": strings.Repeat("a", 64), "sha256": strings.Repeat("d", 64), "baseFingerprint": strings.Repeat("b", 64), "platform": runtime.GOOS, "arch": runtime.GOARCH, "pythonAbi": "cp311",
				"tag": strings.Repeat("a", 64), "packages": map[string]string{"fixture-package": "1"}, "url": "https://updates.example.invalid/fixed-rag.zip", "filename": "fixed-rag.zip", "sizeBytes": 4, "unpackedBytes": 4,
			}}})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if sourceComponent != nil {
		var catalog struct{ Components map[string]map[string]any }
		if err := json.Unmarshal(targetCatalog, &catalog); err != nil || catalog.Components["rag"] == nil {
			t.Fatal("fixture has no fixed target RAG descriptor")
		}
		record["sourceComponent"], record["targetComponent"], record["componentMode"] = sourceComponent, catalog.Components["rag"], "sync-target"
	}
	for name, raw := range map[string][]byte{"package": []byte("non-executable-package"), "components": targetCatalog} {
		filename := filepath.Join(root, name+".fixture")
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, raw, 0600); err != nil {
			t.Fatal(err)
		}
		record[name+"Path"], record[name+"SHA256"], record[name+"Size"] = filename, hexDigest(raw), len(raw)
	}
	now := time.Now().UTC()
	policy := bootstrapPolicyPayload(configuration, record, 42, now)
	envelope := signBootstrapPolicy(t, private, policy)
	decision, err := desktopupdate.Check(t.Context(), envelope, configuration, state, root, false, now)
	if err != nil || (decision.Status != "direct" && decision.Status != "up_to_date") {
		t.Fatalf("actual signed acceptance fixture: %+v %v", decision, err)
	}
	record["policyDigest"], record["policySequence"] = decision.PolicyDigest, decision.PolicySequence
	saveBootstrapRecord(t, root, record)
	return record
}

// Electron supplies OS version after update-state measurement. This synthetic
// OS fact belongs to the fixture; the production measure contract is unchanged.
func bootstrapPolicyLocalState(measured desktopupdate.LocalState) desktopupdate.LocalState {
	measured.SystemVersion = "99.0.0"
	return measured
}

func bootstrapPeerNames(cfg RuntimeConfig) []string {
	peers := []string{"core"}
	if buildRuntimeProcessPlan(cfg).includes(scanControlPlaneProcessName) {
		peers = append(peers, "scan")
	}
	return peers
}

func bootstrapSelectedComponent(t *testing.T, f *updateStateFixture, revision string) map[string]any {
	t.Helper()
	if revision == "" {
		return nil
	}
	catalog, err := loadPythonComponentCatalogForUpdate(f.paths)
	if err != nil || catalog == nil || catalog.Components["rag"].Revision != revision {
		t.Fatalf("invalid installed RAG fixture: %v", err)
	}
	entry := catalog.Components["rag"]
	if entry.CatalogURL == "" || !componentDigestPattern.MatchString(entry.Tag) {
		t.Fatal("bootstrap fixture must have an exact tag selection")
	}
	filename := filepath.Join(f.paths.RuntimeRoot, "deps", "python-components", "rag", "catalog-"+entry.Tag+".json")
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var selected map[string]any
	if err := json.Unmarshal(raw, &selected); err != nil || selected["revision"] != revision {
		t.Fatalf("invalid selected descriptor fixture: %v", err)
	}
	return selected
}

func installBootstrapFixtureRAG(t *testing.T, f *updateStateFixture) {
	t.Helper()
	entry := map[string]any{"id": "rag", "revision": strings.Repeat("a", 64), "sha256": hexDigest([]byte("test")), "baseFingerprint": strings.Repeat("b", 64),
		"platform": runtime.GOOS, "arch": runtime.GOARCH, "pythonAbi": "cp311", "packages": map[string]string{"fixture-package": "1"}, "tag": strings.Repeat("c", 64),
		"catalogUrl": "https://updates.example.invalid/catalog.json", "url": "https://updates.example.invalid/fixed-rag.zip", "filename": "fixed-rag.zip", "sizeBytes": 4, "unpackedBytes": 4}
	write := func(filename string, value any) {
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
	}
	write(filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json"), map[string]any{"schemaVersion": 1, "platform": runtime.GOOS, "arch": runtime.GOARCH, "components": map[string]any{"rag": entry}})
	dir := filepath.Join(f.paths.RuntimeRoot, "deps", "python-components", "rag", entry["revision"].(string))
	if err := os.MkdirAll(filepath.Join(dir, "site-packages"), 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(dir, "bundle-manifest.json"), entry)
	if err := os.WriteFile(filepath.Join(dir, ".ready"), []byte(entry["sha256"].(string)), 0600); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(filepath.Dir(dir), "catalog-"+entry["tag"].(string)+".json"), entry)
	f.cfg.Algorithm.RAGDisabled = false
	state, err := readRuntimeState(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	state.Config.Algorithm.RAGDisabled = false
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ragEnabledEnvVar, "true")
}

func TestUpdateBootstrapRuntimePlanFixtures(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(strconv.FormatBool(installed), func(t *testing.T) {
			// v1 measurement is deliberate here: fixture validity must be checked
			// independently of the unimplemented v2 format prerequisite.
			f := actualUpdateFormatFixture(t, 1)
			if installed {
				installBootstrapFixtureRAG(t, f)
			}
			local := f.measure(t)
			component := bootstrapSelectedComponent(t, f, local.Components["rag"])
			want := []string{"core"}
			if installed {
				want = append(want, "scan")
			}
			if !reflect.DeepEqual(bootstrapPeerNames(f.cfg), want) || (component != nil) != installed {
				t.Fatalf("fixture mode=%v plan=%v component=%v", installed, bootstrapPeerNames(f.cfg), component)
			}
			if buildRuntimeProcessPlan(f.cfg).includes(scanControlPlaneProcessName) != installed {
				t.Fatal("fixture incorrectly starts or omits Scan")
			}
			if installed && (component["revision"] != local.Components["rag"] || component["tag"] == "") {
				t.Fatal("installed fixture lacks exact source component evidence")
			}
		})
	}
}

func bootstrapPolicyPayload(configuration desktopupdate.Config, record map[string]any, sequence uint64, now time.Time) map[string]any {
	artifact := func(name string) map[string]any {
		return map[string]any{"url": "https://updates.example.invalid/" + name, "sha256": record[name+"SHA256"], "size": record[name+"Size"]}
	}
	release := func(id, version string) map[string]any {
		return map[string]any{"releaseId": id, "version": version, "platform": configuration.Platform, "arch": configuration.Arch, "minimumSystemVersion": "0.0.0", "minUpdaterProtocol": 2, "updaterProtocol": 2, "signingIdentity": configuration.SigningIdentity, "package": artifact("package"), "feed": artifact("package"), "components": artifact("components")}
	}
	class := map[string]any{"tag": strings.Repeat("a", 64), "pythonAbi": "cp311", "baseFingerprint": strings.Repeat("b", 64), "packages": map[string]string{"fixture-package": "1"}}
	if source, ok := record["sourceComponent"].(map[string]any); ok {
		for _, key := range []string{"tag", "pythonAbi", "baseFingerprint", "packages"} {
			class[key] = source[key]
		}
	}
	policy := map[string]any{"schemaVersion": 2, "minUpdaterProtocol": 2, "trustDomain": configuration.TrustDomain, "audience": configuration.Audience, "channel": configuration.Channel, "policySequence": sequence, "issuedAt": now.Add(-time.Minute).Format(time.RFC3339), "expiresAt": now.Add(time.Hour).Format(time.RFC3339),
		"releases": []any{release("source", "1.0.0"), release("target", "1.1.0")}, "edges": []any{map[string]any{"sourceReleaseId": "source", "targetReleaseId": "target", "priority": 0, "migrationProfile": "format-v2", "sourceDataProfiles": []string{record["sourceDataProfile"].(string)}, "targetDataProfile": record["targetDataProfile"], "componentTransitions": map[string]any{"rag": map[string]any{"sourceClasses": []any{class}, "targetComponentsSHA256": record["componentsSHA256"]}}}}, "repairAcceptances": []any{}, "revoked": []string{}}
	return policy
}

func signBootstrapPolicy(t *testing.T, private ed25519.PrivateKey, policy map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]string{"keyId": "fixture", "payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestUpdateBootstrapSignedPolicyFixtureControls(t *testing.T) {
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	configuration := desktopupdate.Config{TrustDomain: "fixture-domain", Audience: "official", Channel: "stable", Mode: "official", ReleaseID: "source", Version: "1.0.0", Platform: "darwin", Arch: "arm64", SigningIdentity: "fixture", UpdaterProtocol: 2,
		PolicyURL: "https://updates.example.invalid/policy.json", AllowedOrigins: []string{"https://updates.example.invalid"}, PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(pub)}}
	record := map[string]any{"sourceDataProfile": "format-v2-fixture", "targetDataProfile": "format-v2-fixture", "packageSHA256": strings.Repeat("a", 64), "packageSize": 1, "componentsSHA256": strings.Repeat("b", 64), "componentsSize": 2}
	measured := desktopupdate.LocalState{DataProfile: "format-v2-fixture", Components: map[string]string{}}
	for _, protocol := range []int{1, 2} {
		t.Run(strconv.Itoa(protocol), func(t *testing.T) {
			cfg := configuration
			cfg.UpdaterProtocol = protocol
			now := time.Now().UTC()
			policy := bootstrapPolicyPayload(cfg, record, 42, now)
			if protocol == 1 {
				// This legacy control exercises the same signature construction and
				// complete-state input on today's supported parser. It does not relax
				// v1 to accept new fields or claim v2 capability is implemented.
				cfg.ReleaseID, cfg.Version = "target", "1.1.0"
				policy["schemaVersion"], policy["minUpdaterProtocol"] = 1, 1
				policy["edges"] = []any{}
				delete(policy, "repairAcceptances")
				for _, value := range policy["releases"].([]any) {
					release := value.(map[string]any)
					release["minUpdaterProtocol"], release["updaterProtocol"] = 1, 1
				}
			}
			envelope := signBootstrapPolicy(t, private, policy)
			state := bootstrapPolicyLocalState(measured)
			if state.SystemVersion == "" || state.DataProfile != measured.DataProfile || state.Dirty != measured.Dirty || !reflect.DeepEqual(state.Components, measured.Components) {
				t.Fatal("fixture did not supply only the consumer-owned OS fact")
			}
			decision, err := desktopupdate.Check(t.Context(), envelope, cfg, state, t.TempDir(), false, now)
			want := "direct"
			if protocol == 1 {
				want = "up_to_date"
			}
			if err != nil || decision.Status != want || decision.PolicySequence != 42 || len(decision.PolicyDigest) != 64 {
				t.Fatalf("signed fixture cannot reach policy acceptance: %+v %v", decision, err)
			}
			if protocol == 1 {
				withoutOS, err := desktopupdate.Check(t.Context(), envelope, cfg, measured, t.TempDir(), false, now)
				if err != nil || withoutOS.Status != "blocked" || withoutOS.Reason != "system_unknown" {
					t.Fatalf("missing OS counterexample changed: %+v %v", withoutOS, err)
				}
			}
		})
	}
}

func hexDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func saveBootstrapRecord(t *testing.T, root string, record map[string]any) {
	t.Helper()
	filename := filepath.Join(root, "native-v2", "active.json")
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateBootstrapValidIntentBindsActualCLIToCustomStorageAndSealedPeers(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("packaged Desktop startup supports macOS and Windows")
	}
	for _, scenario := range []struct {
		installed bool
		phase     string
	}{
		{false, "installing"}, {false, "activated"}, {true, "installing"}, {true, "activated"},
	} {
		installed, phase := scenario.installed, scenario.phase
		mode := "absent"
		if installed {
			mode = "installed"
		}
		t.Run(mode+"/"+phase, func(t *testing.T) {
			f := actualUpdateFormatFixture(t, 2)
			if installed {
				installBootstrapFixtureRAG(t, f)
			}
			custom := filepath.Join(t.TempDir(), "custom-auth.db")
			if err := f.databases["auth"].Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(f.cfg.UpdateStorage.Databases["auth"], custom); err != nil {
				t.Fatal(err)
			}
			f.cfg.UpdateStorage.Databases["auth"] = custom
			f.cfg.AuthService.DatabaseURL = "sqlite:///" + filepath.ToSlash(custom)
			t.Setenv(authServiceDatabaseURLEnvVar, f.cfg.AuthService.DatabaseURL)
			state := defaultRuntimeState(f.cfg, f.cfg.ProcessComposePort, f.paths.RunDirTokenFile)
			state.OverallStatus = "stopped"
			state.ProcessCompose.APIPort = 0
			if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			old := resolveRuntimeRecoveryRoot
			resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
			defer func() { resolveRuntimeRecoveryRoot = old }()
			record := bootstrapRecord(t, f, root, phase, nil)
			if phase == "activated" {
				record["activation"] = map[string]any{"transactionId": record["transactionId"], "releaseId": "target", "componentMode": record["componentMode"], "components": f.measure(t).Components, "verified": true}
				saveBootstrapRecord(t, root, record)
			}
			before, err := os.ReadFile(f.paths.StateFile)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			runner := &fakeRunner{t: t}
			cli := NewCLI(&output, &output, runner, "fixture-manager")
			probed := false
			const targetOwner = "fixture-target-owner"
			ready := make(chan struct{}, 1)
			cli.newManager = func(r CommandRunner, exe string) *RuntimeManager {
				m := NewRuntimeManager(r, exe)
				yes := func(int, time.Duration) bool { return true }
				m.probeAPI = func(int, time.Duration) bool { probed = true; return true }
				m.probeLocalProxy = yes
				m.probeFrontend = yes
				m.probeAuth = yes
				m.probeChannelGateway = yes
				m.probeScan = yes
				m.probeFileWatch = yes
				m.probeSQLiteServer = yes
				m.waitHostReady = func(context.Context, RuntimeConfig, []AlgorithmServiceSpec) error { return nil }
				m.processScanner = func(RuntimePaths) ([]LocalProcessRecord, error) { return nil, nil }
				m.relocatePythonVenvs = func(RuntimeConfig, RuntimePaths) error { return nil }
				m.probeCore = func(int, time.Duration) bool {
					raw, err := os.ReadFile(f.paths.GeneratedConfig)
					var generated processComposeConfig
					if err != nil || yaml.Unmarshal(raw, &generated) != nil {
						t.Errorf("actual startup generated no process config: %v", err)
						return false
					}
					for _, name := range []string{coreProcessName, scanControlPlaneProcessName} {
						process, ok := generated.Processes[name]
						if name == scanControlPlaneProcessName && !installed {
							if ok {
								t.Error("lightweight target unexpectedly starts Scan")
							}
							continue
						}
						if !ok {
							t.Errorf("sealed startup omitted peer %s", name)
							continue
						}
						joined := "\n" + strings.Join(process.Environment, "\n") + "\n"
						for _, item := range []string{"LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION=" + strings.Repeat("1", 32), "LAZYMIND_UPDATE_BOOTSTRAP_OWNER=" + targetOwner} {
							if !strings.Contains(joined, "\n"+item+"\n") {
								t.Errorf("%s lost initial fence: %s", name, item)
							}
						}
					}
					persisted, err := readRuntimeState(f.paths.StateFile)
					if err != nil || persisted.Config.UpdateStorage == nil || persisted.Config.UpdateStorage.Databases["auth"] != custom {
						t.Errorf("target replaced custom source path: %v", err)
					}
					select {
					case ready <- struct{}{}:
					default:
					}
					return true
				}
				return m
			}
			args := []string{"up", "--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot, "--owner-token", targetOwner}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if phase == "installing" {
				if err := cli.Run(ctx, args); err == nil || probed {
					t.Fatal("ordinary up crossed an unactivated valid intent")
				}
			} else {
				args = append(args, "--update-bootstrap", strings.Repeat("1", 32), "--bootstrap-owner", targetOwner)
				done := make(chan error, 1)
				go func() { done <- cli.Run(ctx, args) }()
				select {
				case <-ready:
					cancel()
				case err := <-done:
					t.Fatalf("sealed up never generated peer configuration: %v %s", err, output.String())
				case <-ctx.Done():
					t.Fatal("sealed up did not reach peer startup")
				}
				select {
				case err := <-done:
					if err != nil && !errors.Is(err, context.Canceled) {
						t.Fatalf("startup monitor cleanup: %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("startup monitor did not close")
				}
			}
			after, _ := os.ReadFile(f.paths.StateFile)
			if phase == "installing" && !bytes.Equal(before, after) {
				t.Fatal("unactivated target rewrote source runtime state")
			}
		})
	}
}

// Child executes the actual CLI. The only override keeps OS-account discovery
// in the test directory; no production trust-root flag is introduced.
func TestUpdateMaintenanceManagerChild(t *testing.T) {
	if os.Getenv("LAZYMIND_UNIT_MANAGER_ENTRY") != "1" {
		return
	}
	resolveRuntimeRecoveryRoot = func() (string, error) { return os.Getenv("LAZYMIND_UNIT_UPDATER_ROOT"), nil }
	var args []string
	if json.Unmarshal([]byte(os.Getenv("LAZYMIND_UNIT_MANAGER_ARGS")), &args) != nil {
		os.Exit(4)
	}
	cli := NewCLI(os.Stdout, os.Stderr, &ExecRunner{}, os.Args[0])
	if err := cli.Run(context.Background(), args); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(5)
	}
	os.Exit(0)
}

func waitBoundaryFile(t *testing.T, path string, exited <-chan error) {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case err := <-exited:
			t.Fatalf("actual entry exited before controlled boundary: %v", err)
		case <-deadline.C:
			t.Fatal("actual entry never reached controlled boundary")
		case <-tick.C:
		}
	}
}

// Build the actual Core main-package test executable. Its test-only launcher
// calls main(); HTTP is replaced at the transport boundary, not maintenance.
func installCoreMaintenanceEntrypoint(t *testing.T, f *updateStateFixture) string {
	t.Helper()
	if bootstrapGoModuleCacheError != nil || !filepath.IsAbs(bootstrapGoModuleCache) {
		t.Fatalf("cannot resolve inherited Go module cache: %v", bootstrapGoModuleCacheError)
	}
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	core := filepath.Clean(filepath.Join(repo, "..", "..", "backend", "core"))
	binary := filepath.Join(t.TempDir(), executableName("core-tests"))
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-c", "-o", binary, ".")
	cmd.Dir = core
	cmd.Env = append(os.Environ(), "GOMODCACHE="+bootstrapGoModuleCache)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build actual Core entry: %v\n%s", err, output)
	}
	fixtureBuild := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-c", "-o", binary+"-fixtures", "./systemdeps")
	fixtureBuild.Dir = core
	fixtureBuild.Env = append(os.Environ(), "GOMODCACHE="+bootstrapGoModuleCache)
	if output, err := fixtureBuild.CombinedOutput(); err != nil {
		t.Fatalf("build Core fixture owner: %v\n%s", err, output)
	}
	// Portable wrapper translates only the test runner argv. The maintenance
	// request and all trusted runtime arguments are passed unchanged to main().
	wrapper := filepath.Join(t.TempDir(), "wrapper.go")
	code := "package main\nimport(\"os\";\"os/exec\";\"encoding/json\")\nfunc main(){args,_:=json.Marshal(os.Args[1:]);c:=exec.Command(" + strconv.Quote(binary) + ",\"-test.run=^TestComponentMaintenanceEntrypointChild$\");c.Env=append(os.Environ(),\"LAZYMIND_UNIT_COMPONENT_ENTRY=1\",\"LAZYMIND_UNIT_COMPONENT_ARGS=\"+string(args));c.Stdin=os.Stdin;c.Stdout=os.Stdout;c.Stderr=os.Stderr;if c.Run()!=nil {os.Exit(1)}}\n"
	if err := os.WriteFile(wrapper, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.paths.CoreBin), 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", f.paths.CoreBin, wrapper).CombinedOutput(); err != nil {
		t.Fatalf("build argv wrapper: %v %s", err, output)
	}
	return binary
}

func TestUpdateMaintenanceActualChildOwnsLockAfterManagerDeath(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("packaged Desktop maintenance supports macOS and Windows")
	}
	f := actualUpdateFormatFixture(t, 2)
	root := t.TempDir()
	coreBinary := installCoreMaintenanceEntrypoint(t, f)
	hold := t.TempDir()
	// Complete R1 selection, fixed R2 ZIP and descriptors come from the same
	// real Core fixture owner used by the direct maintenance contracts.
	generate := exec.Command(coreBinary+"-fixtures", "-test.run=^TestMaintenanceProcessFixtureChild$")
	generate.Env = append(os.Environ(), "LAZYMIND_UNIT_MAINTENANCE_FIXTURE_ROOT="+hold, "LAZYMIND_UNIT_FIXTURE_RUNTIME_ROOT="+f.paths.RuntimeRoot, "LAZYMIND_UNIT_FIXTURE_UPDATER_ROOT="+root, "LAZYMIND_UNIT_FIXTURE_RESOURCES_ROOT="+f.paths.ResourcesRoot)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate actual component fixture: %v %s", err, output)
	}
	rawFixture, err := os.ReadFile(filepath.Join(hold, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var component map[string]any
	if err := json.Unmarshal(rawFixture, &component); err != nil {
		t.Fatal(err)
	}
	f.cfg.Algorithm.RAGDisabled = false
	state := defaultRuntimeState(f.cfg, f.cfg.ProcessComposePort, f.paths.RunDirTokenFile)
	state.OverallStatus = "ready"
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	targetCatalog, err := os.ReadFile(component["componentsPath"].(string))
	if err != nil {
		t.Fatal(err)
	}
	record := bootstrapRecord(t, f, root, "pending", targetCatalog)
	t.Setenv(ragEnabledEnvVar, "true")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	managerWrapper := filepath.Join(f.paths.BuildRoot, "bin", executableName("local-runtime-manager"))
	managerSource := filepath.Join(t.TempDir(), "manager-wrapper.go")
	managerCode := "package main\nimport(\"os\";\"os/exec\";\"encoding/json\")\nfunc main(){args,_:=json.Marshal(os.Args[1:]);c:=exec.Command(" + strconv.Quote(exe) + ",\"-test.run=^TestUpdateMaintenanceManagerChild$\");c.Env=append(os.Environ(),\"LAZYMIND_UNIT_MANAGER_ENTRY=1\",\"LAZYMIND_UNIT_MANAGER_ARGS=\"+string(args));c.Stdin=os.Stdin;c.Stdout=os.Stdout;c.Stderr=os.Stderr;if c.Run()!=nil{os.Exit(1)}}\n"
	if err := os.WriteFile(managerSource, []byte(managerCode), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", managerWrapper, managerSource).CombinedOutput(); err != nil {
		t.Fatalf("build independent installer CLI boundary: %v %s", err, output)
	}
	args := []string{"update-bootstrap", "--action", "prepare-components", "--transaction-id", strings.Repeat("1", 32), "--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--resources-root", f.paths.ResourcesRoot, "--runtime-root", f.paths.RuntimeRoot, "--owner-token", f.cfg.OwnerToken}
	argsJSON, _ := json.Marshal(args)
	command := exec.Command(exe, "-test.run=^TestUpdateMaintenanceManagerChild$")
	command.Env = append(os.Environ(), "LAZYMIND_UNIT_MANAGER_ENTRY=1", "LAZYMIND_UNIT_UPDATER_ROOT="+root, "LAZYMIND_UNIT_MANAGER_ARGS="+string(argsJSON), "LAZYMIND_UNIT_COMPONENT_HOLD_DIR="+hold,
		"LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE="+filepath.Join(hold, "never-a-real-token"), "ACL_DB_DRIVER=invalid-fixture-driver", "ACL_DB_DSN=invalid://fixture")
	output, err := os.Create(filepath.Join(hold, "manager.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(hold, "release"), nil, 0600); _ = command.Process.Kill() })
	waitBoundaryFile(t, filepath.Join(hold, "entered"), done)
	pidRaw, err := os.ReadFile(filepath.Join(hold, "entered"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(pidRaw))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Kill() })
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-done
	before, err := os.ReadFile(filepath.Join(root, "native-v2", "active.json"))
	if err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]any{"transactionId": record["transactionId"]})
	raw, err := desktopupdate.NativeUpdateStateCommand(context.Background(), root, "cancel", request)
	if err != nil || !bytes.Contains(raw, []byte("update_transaction_busy")) {
		t.Fatalf("manager death released a live Core writer: %s %v", raw, err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "native-v2", "active.json"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("live child lost its transaction")
	}
	foreign := newUpdateStateFixture(t)
	oldRoot := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	defer func() { resolveRuntimeRecoveryRoot = oldRoot }()
	var guardOutput bytes.Buffer
	guard := NewCLI(&guardOutput, &guardOutput, &fakeRunner{t: t}, "fixture-manager")
	started := time.Now()
	if err := guard.Run(context.Background(), []string{"startup-guard", "--profile", "desktop", "--repo-root", foreign.cfg.RepoRoot, "--resources-root", foreign.paths.ResourcesRoot, "--runtime-root", foreign.paths.RuntimeRoot, "--owner-token", foreign.cfg.OwnerToken}); err != nil || time.Since(started) > 15*time.Second {
		t.Fatalf("actual long Core writer blocked unrelated guard: %v %s", err, guardOutput.String())
	}
	// The existing HTTP install handler is an independent entry and must not
	// report already-installed or write while the OTA Core process owns the lock.
	independent := exec.Command(coreBinary, "-test.run=^TestComponentHTTPInstallEntrypointChild$")
	independent.Env = append(os.Environ(), "LAZYMIND_UNIT_COMPONENT_ENTRY=1", "LAZYMIND_RUNTIME_MODE=local", "LAZYMIND_RUNTIME_ROOT="+f.paths.RuntimeRoot, "LAZYMIND_PYTHON_COMPONENT_CATALOG="+filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json"), "LAZYMIND_UNIT_UPDATER_ROOT="+root, "LAZYMIND_SOURCE_COMPONENT_INSTALLER="+managerWrapper, "LAZYMIND_ALGORITHM_SOURCE="+filepath.Join(f.paths.RepoRoot, "algorithm"))
	if output, err := independent.CombinedOutput(); err != nil || !bytes.Contains(output, []byte("update_transaction_busy")) {
		t.Fatalf("independent HTTP installer bypassed live writer: %s %v", output, err)
	}
	// Killing the actual writer, rather than its parent, releases the OS lock.
	if err := writer.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		raw, err = desktopupdate.NativeUpdateStateCommand(context.Background(), root, "cancel", request)
		if err == nil && bytes.Contains(raw, []byte("cancelled")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dead Core prevented same-transaction recovery: %s %v", raw, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
