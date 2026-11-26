package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func replacementFile(t *testing.T, name string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func replacementJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func replacementCatalog(t *testing.T, release, abi string) (map[string]update.ComponentDescriptor, []byte) {
	t.Helper()
	entries := map[string]update.ComponentDescriptor{}
	for _, id := range []string{"rag", "cluster"} {
		tag := hexDigest([]byte(release + "-" + id + "-" + abi))
		name := "fixture-" + id
		payload := []byte("VALUE = '" + release + "-" + id + "'\n")
		d := update.ComponentDescriptor{ID: id, Tag: tag, Revision: hexDigest([]byte("revision-" + tag)), Platform: runtime.GOOS, Arch: runtime.GOARCH, PythonABI: abi, BaseFingerprint: hexDigest([]byte("base-" + release + "-" + abi)), Packages: map[string]string{name: "1"}, Filename: id + ".zip", CatalogURL: "https://updates.example.invalid/" + tag + "/catalog.json", URL: "https://updates.example.invalid/" + tag + "/" + id + ".zip"}
		var archive bytes.Buffer
		w := zip.NewWriter(&archive)
		manifest := replacementJSON(t, d)
		for path, data := range map[string][]byte{"bundle-manifest.json": manifest, "site-packages/fixture_" + id + ".py": payload} {
			member, err := w.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := member.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		d.SHA256, d.SizeBytes, d.UnpackedBytes = hexDigest(archive.Bytes()), int64(archive.Len()), int64(len(manifest)+len(payload))
		entries[id] = d
	}
	return entries, replacementJSON(t, map[string]any{"schemaVersion": 1, "platform": runtime.GOOS, "arch": runtime.GOARCH, "components": entries})
}

func installedReplacementSource(t *testing.T, p RuntimePaths, wanted []string, pin bool) (map[string]update.ComponentDescriptor, []byte) {
	t.Helper()
	entries, raw := replacementCatalog(t, "A", "cp310")
	replacementFile(t, filepath.Join(p.ResourcesRoot, "config", "python-components.json"), raw)
	for _, id := range wanted {
		d := entries[id]
		root := filepath.Join(p.RuntimeRoot, "deps", "python-components", id)
		site := filepath.Join(root, d.Revision, "site-packages", "fixture_"+id+".py")
		replacementFile(t, site, []byte("VALUE = 'A-"+id+"'\n"))
		replacementFile(t, filepath.Join(root, d.Revision, "bundle-manifest.json"), replacementJSON(t, d))
		replacementFile(t, filepath.Join(root, d.Revision, ".ready"), []byte(d.SHA256+"\n"))
		if !pin {
			replacementFile(t, filepath.Join(root, "catalog-"+d.Tag+".json"), replacementJSON(t, d))
		}
	}
	return entries, raw
}

// Exercise the source consumer and signed-catalog parser on the running host.
// The native handoff below separately checks the signed policy and transaction.
func TestInPlaceReplacementTargetsOnlyPreviouslyInstalledComponents(t *testing.T) {
	for _, tc := range []struct {
		name   string
		wanted []string
		pin    bool
		abi    string
	}{
		{"none", nil, false, "cp311"}, {"RAG", []string{"rag"}, false, "cp311"}, {"cluster", []string{"cluster"}, false, "cp311"}, {"both", []string{"rag", "cluster"}, false, "cp311"}, {"offline packaged pin", []string{"rag"}, true, "cp311"}, {"same ABI newer revision", []string{"rag", "cluster"}, false, "cp310"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := RuntimePaths{RuntimeRoot: t.TempDir(), ResourcesRoot: t.TempDir()}
			a, _ := installedReplacementSource(t, p, tc.wanted, tc.pin)
			have, err := measuredUpdateComponents(p, !slices.Contains(tc.wanted, "rag"))
			if err != nil {
				t.Fatal(err)
			}
			selected, err := fullSourceDescriptors(p, update.LocalState{Components: have})
			if err != nil {
				t.Fatalf("verified installed source was not retained: %v", err)
			}
			b, raw := replacementCatalog(t, "B", tc.abi)
			file := filepath.Join(t.TempDir(), "B-components.json")
			replacementFile(t, file, raw)
			_, catalog, err := readTargetCatalog(t.Context(), file, update.Artifact{SHA256: hexDigest(raw), Size: int64(len(raw))})
			if err != nil {
				t.Fatal(err)
			}
			next, err := selectTargetComponents(selected, catalog)
			if err != nil {
				t.Fatalf("target runtime matches its own descriptor; cannot select B: %v", err)
			}
			wantB := map[string]string{}
			for _, id := range tc.wanted {
				if have[id] != a[id].Revision {
					t.Fatalf("wrong source %s selection", id)
				}
				wantB[id] = b[id].Revision
			}
			if !maps.Equal(componentRevisions(next), wantB) {
				t.Fatalf("target selection %v, expected %v", componentRevisions(next), wantB)
			}
			replacementFile(t, filepath.Join(p.ResourcesRoot, "config", "python-components.json"), raw)
			if sites := pythonComponentSites(p); len(sites) != 0 {
				t.Fatalf("B would load unverified A sites: %v", sites)
			}
		})
	}
}

// Even when Core can only report a download failure, a signed main app must
// retain the user's actual choices and reach native handoff. No synthetic
// prepared/activated reply or ready marker is issued by the failing worker.
func TestInPlaceSignedHandoffSurvivesOptionalDownloadFailure(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("native update platforms")
	}
	if update.UnsignedTestBuild {
		t.Skip("ordinary desktop fixture; test-channel trust has a separate frozen matrix")
	}
	for _, tc := range []struct {
		name, abi string
		missing   bool
	}{
		{"same ABI download offline", "cp310", false}, {"new ABI download offline", "cp311", false}, {"signed catalog has no cluster", "cp310", true},
	} {
		t.Run(tc.name, func(t *testing.T) { checkInPlaceOptionalHandoff(t, tc.abi, tc.missing) })
	}
}

func checkInPlaceOptionalHandoff(t *testing.T, targetABI string, missingCluster bool) {
	t.Helper()
	f := actualUpdateFormatFixture(t, 2)
	replacementFile(t, filepath.Join(f.paths.ResourcesRoot, "config", "update-format-v2.json"), updateFormatRecipe)
	root := t.TempDir()
	previous := resolveRuntimeRecoveryRoot
	resolveRuntimeRecoveryRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { resolveRuntimeRecoveryRoot = previous })
	selection := []string{"rag", "cluster"}
	a, sourceCatalog := installedReplacementSource(t, f.paths, selection, false)
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
	local := bootstrapPolicyLocalState(f.measure(t))
	if !maps.Equal(local.Components, componentRevisions(a)) {
		t.Fatalf("fixture lost a selected component: %v", local.Components)
	}
	b, catalog := replacementCatalog(t, "B", targetABI)
	if missingCluster {
		delete(b, "cluster")
		catalog = replacementJSON(t, map[string]any{"schemaVersion": 1, "platform": runtime.GOOS, "arch": runtime.GOARCH, "components": b})
	}
	catalogFile := filepath.Join(root, "downloads", "components.json")
	replacementFile(t, catalogFile, catalog)
	// These are data-only native-package fixtures. The policy binds the
	// actual, distinct A/B ZIP and catalog bytes. No program is taken from them.
	packageBytes := func(catalog []byte) []byte {
		var archive bytes.Buffer
		zipper := zip.NewWriter(&archive)
		member, e := zipper.Create("Contents/Resources/runtime/config/python-components.json")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = member.Write(catalog); e != nil {
			t.Fatal(e)
		}
		format, e := zipper.Create("Contents/Resources/runtime/config/update-format-v2.json")
		if e != nil {
			t.Fatal(e)
		}
		if _, e := format.Write(updateFormatRecipe); e != nil {
			t.Fatal(e)
		}
		if e = zipper.Close(); e != nil {
			t.Fatal(e)
		}
		return archive.Bytes()
	}
	sourcePackage, targetPackage := packageBytes(sourceCatalog), packageBytes(catalog)
	pkg := filepath.Join(filepath.Dir(catalogFile), "application.zip")
	replacementFile(t, pkg, targetPackage)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := update.Config{Mode: "official", TrustDomain: "fixture-domain", Audience: "official", Channel: "stable", ReleaseID: "source", Version: "1.0.0", Platform: runtime.GOOS, Arch: runtime.GOARCH, SigningIdentity: "fixture", UpdaterProtocol: 2, PolicyURL: "https://updates.example.invalid/policy.json", AllowedOrigins: []string{"https://updates.example.invalid"}, PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(pub)}}
	fields := map[string]any{"sourceDataProfile": local.DataProfile, "targetDataProfile": local.DataProfile, "packageSHA256": hexDigest(targetPackage), "packageSize": len(targetPackage), "componentsSHA256": hexDigest(catalog), "componentsSize": len(catalog), "sourceComponent": map[string]any{"tag": a["rag"].Tag, "pythonAbi": a["rag"].PythonABI, "baseFingerprint": a["rag"].BaseFingerprint, "packages": a["rag"].Packages}}
	now := time.Now().UTC()
	policy := bootstrapPolicyPayload(c, fields, 1, now)
	for _, v := range policy["releases"].([]any) {
		row := v.(map[string]any)
		pack, manifest := targetPackage, catalog
		if row["releaseId"] == "source" {
			pack, manifest = sourcePackage, sourceCatalog
		}
		archive := row["package"].(map[string]any)
		archive["size"], archive["sha256"], archive["unpackedBytes"] = len(pack), hexDigest(pack), len(manifest)+len(updateFormatRecipe)
		descriptor := row["components"].(map[string]any)
		descriptor["size"], descriptor["sha256"] = len(manifest), hexDigest(manifest)
	}
	offer, err := update.Check(t.Context(), signBootstrapPolicy(t, key, policy), c, local, root, false, now)
	if err != nil || offer.Status != "direct" || offer.Next == nil || offer.Next.Components.SHA256 != hexDigest(catalog) {
		t.Fatalf("real signed transition: %+v %v", offer, err)
	}
	// Hold any foreground optional transfer until B has completed. A caller
	// that synchronously waits on it must hit the test deadline, even if it
	// would eventually degrade on a network error.
	network := t.TempDir()
	release := filepath.Join(network, "release")
	t.Setenv("LAZYMIND_UNIT_COMPONENT_HOLD_DIR", network)
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0600) })
	src := filepath.Join(t.TempDir(), "offline.go")
	probeLog := filepath.Join(network, "entered")
	replacementFile(t, src, []byte(`package main
import("bufio"; "fmt"; "os"; "path/filepath"; "time")
func main() {
	if len(os.Args)!=3 || os.Args[1]!="--component-maintenance" || os.Args[2]!="stage" { os.Exit(2) }
	input:=bufio.NewScanner(os.Stdin)
	if !input.Scan() { os.Exit(2) }
	dir:=os.Getenv("LAZYMIND_UNIT_COMPONENT_HOLD_DIR")
	if os.WriteFile(filepath.Join(dir,"entered"),input.Bytes(),0600)!=nil { os.Exit(3) }
	cancelled:=make(chan struct{})
	go func(){input.Scan();close(cancelled)}()
	for {
		if _,e:=os.Stat(filepath.Join(dir,"release"));e==nil {break}
		select {case <-cancelled:return;case <-time.After(25*time.Millisecond):}
	}
	fmt.Print("{\"status\":\"blocked\",\"reason\":\"artifact_hash\"}")
}`))
	if err := os.MkdirAll(filepath.Dir(f.paths.CoreBin), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", f.paths.CoreBin, src).CombinedOutput(); err != nil {
		t.Fatalf("build offline worker: %v %s", err, out)
	}
	cli := NewCLI(io.Discard, io.Discard, &ExecRunner{}, "fixture-manager")
	full := targetABI == "cp310"
	var cleanRuntime, cleanUpdater string
	if full {
		for _, db := range f.databases {
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		}
		cleanRuntime, cleanUpdater = t.TempDir(), t.TempDir()
		if err := os.CopyFS(cleanRuntime, os.DirFS(f.paths.RuntimeRoot)); err != nil {
			t.Fatal(err)
		}
		if err := os.CopyFS(cleanUpdater, os.DirFS(root)); err != nil {
			t.Fatal(err)
		}
	}
	handoffCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	r, err := cli.prepareNativeComponents(handoffCtx, f.cfg, f.paths, c, root, "target", pkg, catalogFile)
	deadlineErr := handoffCtx.Err()
	cancel()
	if deadlineErr != nil {
		t.Fatalf("A waited on the optional download before app handoff: %v", deadlineErr)
	}
	if err != nil || r == nil {
		_, attemptErr := os.Stat(probeLog)
		t.Errorf("signed app handoff stopped (offline worker reached=%v): %v", attemptErr == nil, err)
		r, err = update.ReadNativeRecord(root)
		if !full || err != nil || r == nil {
			return
		}
	}
	reopened, err := update.ReadNativeRecord(root)
	if err != nil || reopened == nil || reopened.TransactionID != r.TransactionID || reopened.ComponentsSHA256 != offer.Next.Components.SHA256 {
		t.Fatalf("no durable target identity after preparation: %v", err)
	}
	if (!missingCluster && !maps.Equal(componentRevisions(reopened.SourceComponents()), componentRevisions(a))) || !maps.Equal(componentRevisions(reopened.EffectiveComponents()), componentRevisions(b)) {
		t.Fatalf("reopened transaction lost source choices or signed B target: %+v", nativeSummary(reopened, f.cfg.OwnerToken))
	}
	if source, err := measuredUpdateComponents(f.paths, false); err != nil || !maps.Equal(source, local.Components) {
		t.Fatalf("offline attempt changed the usable A selection: %v %v", source, err)
	}
	if full {
		checkInPlaceTargetCompletion(t, f, root, r, c, key, *offer.Next, b, catalog, cleanRuntime, cleanUpdater, missingCluster)
		return
	}
	replacementFile(t, filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json"), catalog)
	if sites := pythonComponentSites(f.paths); len(sites) != 0 {
		t.Fatalf("B imported A while replacements were pending: %s", strings.Join(sites, ","))
	}
}

// This fixture supplies the OS signing provider and two live local peers. The
// actual stage/defer/activation, database measurement, permission, verify and
// completion paths run. The peers reject foreign owners/transactions, and
// one is deliberately busy before the allowed proof is obtained.
func checkInPlaceTargetCompletion(t *testing.T, f *updateStateFixture, root string, r *update.NativeRecord, c update.Config, key ed25519.PrivateKey, signedB update.Release, targets map[string]update.ComponentDescriptor, catalog []byte, cleanRuntime, cleanUpdater string, missingCluster bool) {
	t.Helper()
	installCoreMaintenanceEntrypoint(t, f)
	network := os.Getenv("LAZYMIND_UNIT_COMPONENT_HOLD_DIR")
	t.Setenv("LAZYMIND_UNIT_UPDATER_ROOT", root) // no payload.zip: real transport fails
	helper := filepath.Join(f.paths.ResourcesRoot, "bin", executableName("lazymind-update-helper"))
	source := filepath.Join(t.TempDir(), "signer.go")
	replacementFile(t, source, []byte(`package main
import("os")
func main(){if len(os.Args)!=2||os.Args[1]!="verify-identity"{os.Exit(2)};exe,e:=os.Executable();if e!=nil{os.Exit(2)};raw,e:=os.ReadFile(exe+".fixture");if e!=nil{os.Exit(2)};os.Stdout.Write(raw)}`))
	if out, e := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", helper, source).CombinedOutput(); e != nil {
		t.Fatalf("signing provider fixture: %v %s", e, out)
	}
	identity := func() {
		replacementFile(t, helper+".fixture", replacementJSON(t, map[string]any{"status": "enabled", "config": c}))
	}
	identity()
	var busy atomic.Bool
	var released atomic.Int32
	for _, id := range r.ExpectedPeers {
		id := id // each listener owns a distinct participant on Go 1.21
		var sealed atomic.Bool
		sealed.Store(true)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			var ask struct {
				Action, TransactionID, Owner string
				Epoch                        uint64
			}
			if req.URL.Path != "/internal/desktop-update" || req.Header.Get("X-LazyMind-Internal-Token") != internalServiceToken() || json.NewDecoder(req.Body).Decode(&ask) != nil {
				http.Error(w, "invalid request", 400)
				return
			}
			active := 0
			if busy.Load() && id == "core" {
				active = 1
			}
			if ask.TransactionID == "" {
				if ask.Action != "pause" && ask.Action != "resume" && ask.Action != "status" {
					http.Error(w, "invalid source action", 400)
					return
				}
				epoch := uint64(0)
				if ask.Action == "pause" {
					epoch = ask.Epoch
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"active": active, "paused": epoch != 0, "epoch": epoch})
				return
			}
			if ask.Owner != f.cfg.OwnerToken || ask.TransactionID != r.TransactionID || (ask.Action != "status" && ask.Action != "bootstrap-complete") {
				http.Error(w, "wrong bootstrap identity", 409)
				return
			}
			if ask.Action == "bootstrap-complete" && active == 0 && sealed.CompareAndSwap(true, false) {
				released.Add(1)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": active, "bootstrap": map[string]any{"transactionId": ask.TransactionID, "owner": ask.Owner, "sealed": sealed.Load()}})
		}))
		t.Cleanup(server.Close)
		port := server.Listener.Addr().(*net.TCPAddr).Port
		if id == "core" {
			f.cfg.LocalProxy.CoreHostPort = port
		} else {
			f.cfg.LocalProxy.ScanHostPort = port
		}
	}
	state, err := readRuntimeState(f.paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	state.Config.LocalProxy = f.cfg.LocalProxy
	if err := writeRuntimeState(f.paths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	call := func(action string, extra ...string) map[string]any {
		t.Helper()
		var out bytes.Buffer
		cli := NewCLI(&out, &out, &fakeRunner{t: t}, "fixture-manager")
		args := []string{"update-bootstrap", "--action", action, "--transaction-id", r.TransactionID, "--profile", "desktop", "--repo-root", f.cfg.RepoRoot, "--runtime-root", f.paths.RuntimeRoot, "--resources-root", f.paths.ResourcesRoot, "--owner-token", f.cfg.OwnerToken}
		actionCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		if err := cli.Run(actionCtx, append(args, extra...)); err != nil {
			t.Fatalf("%s: %v %s", action, err, out.String())
		}
		if err := actionCtx.Err(); err != nil {
			t.Fatalf("%s waited for the optional download: %v", action, err)
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("%s: %v %s", action, err, out.String())
		}
		return result
	}
	busy.Store(true)
	if v := call("installing", "--epoch", "41"); v["status"] != "blocked" {
		t.Fatalf("source busy gate: %v", v)
	}
	busy.Store(false)
	if v := call("installing", "--epoch", "42"); v["status"] != "installing" {
		t.Fatalf("idle source could not proceed with deferred optional work: %v", v)
	}
	c.ReleaseID, c.Version = r.TargetReleaseID, r.TargetVersion
	f.cfg.OwnerToken = "target-runtime-owner"
	identity()
	replacementFile(t, filepath.Join(f.paths.ResourcesRoot, "config", "python-components.json"), catalog)
	if sites := pythonComponentSites(f.paths); len(sites) != 0 {
		t.Fatalf("B enabled incompatible A sites: %v", sites)
	}
	if v := call("activate"); v["status"] != "activated" {
		t.Fatalf("target did not activate with deferred packages: %v", v)
	}
	scope, err := runtimeUpdateScope(f.cfg, f.paths)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := update.NativeStartupPermission(t.Context(), root, scope, c.ReleaseID, r.TransactionID)
	if err != nil || allowed == nil {
		t.Fatalf("target native startup permission: %v", err)
	}
	targetCfg, targetPaths, err := newRuntimeConfigWithOptions(RuntimeConfigOptions{Profile: "desktop", RepoRoot: f.cfg.RepoRoot, RuntimeRoot: f.paths.RuntimeRoot, ResourcesRoot: f.paths.ResourcesRoot, OwnerToken: f.cfg.OwnerToken}, true)
	if err != nil {
		t.Fatalf("cannot resolve B settings: %v", err)
	}
	targetCfg.LocalProxy.CoreHostPort, targetCfg.LocalProxy.ScanHostPort = f.cfg.LocalProxy.CoreHostPort, f.cfg.LocalProxy.ScanHostPort
	if err := bindNativeStartup(t.Context(), &targetCfg, &targetPaths, r.TransactionID, f.cfg.OwnerToken); err != nil {
		t.Fatalf("target service admission: %v", err)
	}
	if len(pythonComponentSites(targetPaths)) != 0 {
		t.Fatal("admitted B exposed an unverified site")
	}
	for _, id := range r.ExpectedPeers {
		if _, err := bootstrapPeerRequest(t.Context(), targetCfg, id, "status", r.TransactionID, targetCfg.OwnerToken); err != nil {
			t.Fatal(err)
		}
	}
	// Only publish the bound configuration after native admission and live
	// owner/seal checks. The complete command re-reads the databases and peers.
	state = defaultRuntimeState(targetCfg, targetCfg.ProcessComposePort, targetPaths.RunDirTokenFile)
	state.OverallStatus = "ready"
	if err := writeRuntimeState(targetPaths.StateFile, state); err != nil {
		t.Fatal(err)
	}
	busy.Store(true)
	if v := call("complete"); v["status"] != "blocked" || released.Load() != 0 {
		t.Fatalf("busy peer was released: %v", v)
	}
	busy.Store(false)
	proof := call("verify")
	expected := map[string]any{}
	for id, component := range targets {
		expected[id] = component.Revision
	}
	active, actual := proof["components"].(map[string]any)
	pending, _ := proof["pendingComponents"].(map[string]any)
	if proof["status"] != "ready" || !actual || len(active) != 0 || !maps.Equal(pending, expected) {
		t.Fatalf("actual target proof with offline components: %v", proof)
	}
	// A selected ID without a signed descriptor has no revision or URL.
	unavailable, _ := proof["unavailableComponents"].([]any)
	if missingCluster {
		if !slices.Equal(unavailable, []any{"cluster"}) {
			t.Fatalf("missing desired cluster must be unavailable without an invented revision: %v", proof)
		}
	} else if len(unavailable) != 0 {
		t.Fatalf("unexpected unavailable selection: %v", unavailable)
	}
	delete(proof, "status")
	delete(proof, "version")
	reject := func(wire map[string]any) {
		t.Helper()
		raw, e := update.NativeUpdateStateCommand(t.Context(), root, "complete", replacementJSON(t, wire))
		var answer struct{ Status string }
		if e != nil || json.Unmarshal(raw, &answer) != nil || answer.Status != "blocked" {
			t.Fatalf("unsupported completion proof accepted: %s %v", raw, e)
		}
	}
	fake := maps.Clone(proof)
	delete(fake, "pendingComponents")
	fake["components"] = expected
	reject(fake)
	if missingCluster {
		dropped := maps.Clone(proof)
		delete(dropped, "unavailableComponents")
		reject(dropped)
	}
	omitted := maps.Clone(proof)
	delete(omitted, "pendingComponents")
	reject(omitted)
	// Replay the genuine proof with only the authentic active transaction,
	// pre-handoff user storage and B's bound supervisor configuration. None of
	// the optional reconciliation writes survive. Paths and journal bytes are
	// identical; the pending map on the wire cannot authorize itself.
	nativeBytes, err := os.ReadFile(update.NativeRecordPath(root))
	if err != nil {
		t.Fatal(err)
	}
	stateBytes, err := os.ReadFile(targetPaths.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		restore := func(path, snapshot string) {
			t.Helper()
			backup := path + ".saved-by-component-test"
			if err := os.Rename(path, backup); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, e := os.Stat(backup); e == nil {
					_ = os.RemoveAll(path)
					_ = os.Rename(backup, path)
				}
			})
			if err := os.CopyFS(path, os.DirFS(snapshot)); err != nil {
				t.Fatal(err)
			}
		}
		restore(root, cleanUpdater)
		restore(targetPaths.RuntimeRoot, cleanRuntime)
		replacementFile(t, update.NativeRecordPath(root), nativeBytes)
		replacementFile(t, targetPaths.StateFile, stateBytes)
		reject(proof)
		for _, path := range []string{targetPaths.RuntimeRoot, root} {
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+".saved-by-component-test", path); err != nil {
				t.Fatal(err)
			}
		}
	}()
	if v := call("complete"); v["status"] != "completed" || int(released.Load()) != len(r.ExpectedPeers) {
		t.Fatalf("verified target did not complete and unseal both peers: %v", v)
	}
	// The network gate is first released after native complete and both peers.
	replacementFile(t, filepath.Join(network, "release"), nil)
	if remaining, e := update.ReadNativeRecord(root); e != nil || remaining != nil {
		t.Fatalf("active application transaction remains: %+v %v", remaining, e)
	}
	state, err = readRuntimeState(targetPaths.StateFile)
	if err != nil || state.Config.NativeCommittedTx != r.TransactionID || len(pythonComponentSites(targetPaths)) != 0 {
		t.Fatalf("completion lost the trusted receipt or exposed old components: %v", err)
	}
	checkReplacementAfterRestart(t, f, root, targetCfg, targetPaths, c, key, signedB, targets, catalog, missingCluster)

}

// Run the production reconciler from a new process. Its network transport
// records the actual attempted URLs and always fails: observation cannot mark
// a component verified or ready. No previous Go object or active.json exists.
func checkReplacementAfterRestart(t *testing.T, f *updateStateFixture, root string, cfg RuntimeConfig, p RuntimePaths, b update.Config, key ed25519.PrivateKey, signedB update.Release, bTargets map[string]update.ComponentDescriptor, bCatalog []byte, missingCluster bool) {
	t.Helper()
	coreDir, err := filepath.Abs(filepath.Join("..", "..", "backend", "core"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "retry.go")
	worker := filepath.Join(dir, executableName("retry"))
	replacementFile(t, src, []byte(`package main
import("context";"encoding/json";"fmt";"io";"net/http";"os";"sync";"time";"lazymind/core/systemdeps")
type monitor interface{ReconcileSelectedPythonComponents(context.Context,time.Duration)error}
type offline struct{mu sync.Mutex}
func(o *offline)RoundTrip(r *http.Request)(*http.Response,error){o.mu.Lock();defer o.mu.Unlock();json.NewEncoder(os.Stdout).Encode(r.URL.String());return nil,io.ErrUnexpectedEOF}
func main(){env:=systemdeps.PythonComponentMaintenanceContext{RuntimeRoot:os.Args[1],ResourcesRoot:os.Args[2],UpdaterRoot:os.Args[3],ReleaseID:os.Args[4],Python:os.Args[5],AlgorithmSource:os.Args[6],Client:&http.Client{Transport:&offline{}}};worker,ok:=any(&env).(monitor);if !ok{fmt.Fprint(os.Stderr,"target reconciliation unavailable");os.Exit(3)};ctx,cancel:=context.WithTimeout(context.Background(),2*time.Second);defer cancel();worker.ReconcileSelectedPythonComponents(ctx,50*time.Millisecond)}`))
	build := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", worker, src)
	build.Dir = coreDir
	build.Env = coreFixtureBuildEnv
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build fresh target worker: %s %v", out, e)
	}
	reopen := func(identity update.Config) map[string]bool {
		t.Helper()
		helper := filepath.Join(p.ResourcesRoot, "bin", executableName("lazymind-update-helper"))
		replacementFile(t, helper+".fixture", replacementJSON(t, map[string]any{"status": "enabled", "config": identity}))
		cmd := exec.CommandContext(t.Context(), worker, p.RuntimeRoot, p.ResourcesRoot, root, identity.ReleaseID, p.AlgorithmPython, filepath.Join(p.RepoRoot, "algorithm"))
		var out, diagnostics bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &diagnostics
		if e := cmd.Run(); e != nil {
			t.Fatalf("restart %s: %v %s", identity.ReleaseID, e, diagnostics.String())
		}
		attempted := map[string]bool{}
		dec := json.NewDecoder(&out)
		for {
			var url string
			e := dec.Decode(&url)
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			attempted[url] = true
		}
		return attempted
	}
	expected := map[string]bool{}
	for _, d := range bTargets {
		expected[d.URL] = true
	}
	for i := 0; i < 2; i++ {
		tried := reopen(b)
		if !maps.Equal(tried, expected) || len(pythonComponentSites(p)) != 0 {
			t.Fatalf("reopened B lost its bound pending work or exposed A: got %v want %v", tried, expected)
		}
	}
	future := b
	future.ReleaseID, future.Version = "future", "1.2.0"
	cTargets, cCatalog := replacementCatalog(t, "C", "cp311")
	catPath := filepath.Join(p.ResourcesRoot, "config", "python-components.json")
	// Release and catalog are independent bindings; changing just one
	// cannot consume the pending work, even with the same account and keys.
	if tried := reopen(future); len(tried) != 0 {
		t.Fatalf("C with B catalog consumed B work: %v", tried)
	}
	replacementFile(t, catPath, cCatalog)
	if tried := reopen(b); len(tried) != 0 {
		t.Fatalf("B identity with a different catalog consumed pending work: %v", tried)
	}
	if tried := reopen(future); len(tried) != 0 || len(pythonComponentSites(p)) != 0 {
		t.Fatalf("unapproved C consumed or replaced B work: %v", tried)
	}
	// Back on the authentic B, publish a *new* signed B->C decision. Only
	// this transaction may rebind retained choices to C, including a cluster
	// that B could not fetch because its own signed catalog did not list it.
	replacementFile(t, catPath, bCatalog)
	if tried := reopen(b); !maps.Equal(tried, expected) {
		t.Fatalf("opening foreign C erased B's pending work: %v", tried)
	}
	local := bootstrapPolicyLocalState(f.measure(t))
	if len(local.Components) != 0 {
		t.Fatalf("pending is not active on B: %v", local.Components)
	}
	pack := func(catalog []byte) []byte {
		var buffer bytes.Buffer
		z := zip.NewWriter(&buffer)
		for name, data := range map[string][]byte{"Contents/Resources/runtime/config/python-components.json": catalog, "Contents/Resources/runtime/config/update-format-v2.json": updateFormatRecipe} {
			entry, e := z.Create(name)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = entry.Write(data); e != nil {
				t.Fatal(e)
			}
		}
		if e := z.Close(); e != nil {
			t.Fatal(e)
		}
		return buffer.Bytes()
	}
	cPackage := pack(cCatalog)
	loc := filepath.Join(root, "downloads", "C")
	pkg, components := filepath.Join(loc, "application.zip"), filepath.Join(loc, "components.json")
	replacementFile(t, pkg, cPackage)
	replacementFile(t, components, cCatalog)
	rag := bTargets["rag"]
	fields := map[string]any{"sourceDataProfile": local.DataProfile, "targetDataProfile": local.DataProfile, "packageSHA256": hexDigest(cPackage), "packageSize": len(cPackage), "componentsSHA256": hexDigest(cCatalog), "componentsSize": len(cCatalog), "sourceComponent": map[string]any{"tag": rag.Tag, "pythonAbi": rag.PythonABI, "baseFingerprint": rag.BaseFingerprint, "packages": rag.Packages}}
	now := time.Now().UTC()
	policy := bootstrapPolicyPayload(b, fields, 2, now)
	edge := policy["edges"].([]any)[0].(map[string]any)
	edge["sourceReleaseId"], edge["targetReleaseId"] = b.ReleaseID, future.ReleaseID
	releases := policy["releases"].([]any)
	for i, v := range releases {
		row := v.(map[string]any)
		if row["releaseId"] == "source" {
			// Keep B's immutable source record from the previous signed offer.
			releases[i] = signedB
			continue
		}
		row["releaseId"], row["version"] = future.ReleaseID, future.Version
		a := row["package"].(map[string]any)
		a["url"], a["size"], a["sha256"], a["unpackedBytes"] = "https://updates.example.invalid/releases/future/package", len(cPackage), hexDigest(cPackage), len(cCatalog)+len(updateFormatRecipe)
		d := row["components"].(map[string]any)
		d["url"], d["size"], d["sha256"] = "https://updates.example.invalid/releases/future/components", len(cCatalog), hexDigest(cCatalog)
	}
	decision, err := update.Check(t.Context(), signBootstrapPolicy(t, key, policy), b, local, root, false, now)
	if err != nil || decision.Status != "direct" || decision.Next == nil || decision.Next.ID != future.ReleaseID || decision.Next.Components.SHA256 != hexDigest(cCatalog) {
		t.Fatalf("new authorized C transition: %+v %v", decision, err)
	}
	manager := NewCLI(io.Discard, io.Discard, &ExecRunner{}, "fixture-manager")
	issued, err := manager.prepareNativeComponents(t.Context(), cfg, p, b, root, future.ReleaseID, pkg, components)
	if err != nil || issued == nil || issued.TargetReleaseID != future.ReleaseID || !maps.Equal(componentRevisions(issued.EffectiveComponents()), componentRevisions(cTargets)) {
		t.Fatalf("signed C did not receive exactly the previously wanted IDs at its own revisions (B missing cluster=%v): %+v %v", missingCluster, issued, err)
	}
}
