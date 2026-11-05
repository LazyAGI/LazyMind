package systemdeps

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type maintenanceHTTP func(*http.Request) (*http.Response, error)

func (fn maintenanceHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

type maintenanceFixture struct {
	runtimeRoot, updaterRoot, sourceBundle, targetBundle, intent, probeLog string
	source, target                                                         PythonComponent
	record                                                                 map[string]any
	archive                                                                []byte
	requests                                                               int
	client                                                                 *http.Client
}

func newMaintenanceFixture(t *testing.T, sameTarget bool) *maintenanceFixture {
	t.Helper()
	return maintenanceFixtureAt(t, t.TempDir(), "", "", sameTarget)
}

func maintenanceFixtureAt(t *testing.T, root, runtimeRoot, updaterRoot string, sameTarget bool) *maintenanceFixture {
	t.Helper()
	f := &maintenanceFixture{
		runtimeRoot: filepath.Join(root, "runtime"), updaterRoot: filepath.Join(root, "updater"),
		sourceBundle: filepath.Join(root, "source-bundle"), targetBundle: filepath.Join(root, "target-bundle"),
		probeLog: filepath.Join(root, "probe.json"),
	}
	if runtimeRoot != "" {
		f.runtimeRoot = runtimeRoot
	}
	if updaterRoot != "" {
		f.updaterRoot = updaterRoot
	}
	f.source = testPythonEntry()
	f.source.Tag = strings.Repeat("c", 64)
	f.source.Packages = map[string]string{"fixture-package": "1"}
	f.source.URL = "https://huggingface.co/fixture/resolve/main/source-rag.zip"
	f.source.Filename = "source-rag.zip"
	f.source.CatalogURL = "https://huggingface.co/fixture/resolve/main/catalog.json"
	f.target = f.source
	f.target.Revision = strings.Repeat("e", 64)
	if sameTarget {
		f.target.Revision = f.source.Revision
	}
	manifest, err := json.Marshal(f.target)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, entry := range []struct{ name, data string }{
		{"bundle-manifest.json", string(manifest)},
		{"site-packages/fixture_module.py", "VALUE = 'synthetic'\n"},
	} {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	f.archive = archive.Bytes()
	sum := sha256.Sum256(f.archive)
	f.target.SHA256 = hex.EncodeToString(sum[:])
	f.target.SizeBytes, f.target.UnpackedBytes = int64(len(f.archive)), 64*1024
	f.target.URL = "https://huggingface.co/fixture/resolve/main/fixed-rag.zip"
	f.target.Filename = "fixed-rag.zip"
	if sameTarget {
		f.source = f.target
	}
	f.client = &http.Client{Transport: maintenanceHTTP(func(request *http.Request) (*http.Response, error) {
		f.requests++
		if request.URL.String() != f.target.URL {
			t.Errorf("maintenance requested unbound/online replacement metadata: %s", request.URL)
			return nil, io.ErrUnexpectedEOF
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(bytes.NewReader(f.archive)), Request: request}, nil
	})}
	writeFixtureComponent(t, f.runtimeRoot, f.source)
	if err := savePythonDescriptor(f.runtimeRoot, f.source); err != nil {
		t.Fatal(err)
	}
	if err := readMaintenanceSelection(f.runtimeRoot, f.source); err != nil {
		t.Fatalf("invalid source receipt fixture: %v", err)
	}
	if got := installedPythonDescriptor(f.runtimeRoot, f.target); got.Revision != f.source.Revision {
		t.Fatalf("invalid source selection fixture: %+v", got)
	}
	catalogPath := filepath.Join(f.updaterRoot, "downloads", "fixed-components.json")
	catalog := pythonCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH,
		Components: map[string]PythonComponent{"rag": f.target}}
	catalogBytes := fixtureJSON(t, catalog)
	writeMaintenanceFile(t, catalogPath, catalogBytes)
	catalogHash := sha256.Sum256(catalogBytes)
	storageFiles := map[string]string{}
	for _, family := range []string{"core", "auth", "lazyllm", "segments", "channel", "scan", "core-state", "auth-state", "scan-state"} {
		filename := filepath.Join(root, "accepted-storage", family+".db")
		writeMaintenanceFile(t, filename, []byte("accepted-storage-boundary-fixture"))
		storageFiles[family] = filename
	}
	vectorPath := filepath.Join(root, "accepted-storage", "indexes")
	if err := os.MkdirAll(vectorPath, 0700); err != nil {
		t.Fatal(err)
	}
	packageBytes := []byte("synthetic-non-executable-application")
	packagePath := filepath.Join(f.updaterRoot, "downloads", "application.fixture")
	writeMaintenanceFile(t, packagePath, packageBytes)
	packageHash := sha256.Sum256(packageBytes)
	f.record = map[string]any{
		"version": 2, "transactionId": strings.Repeat("1", 32), "phase": "pending",
		"sourceReleaseId": "source", "sourceVersion": "1.0.0", "targetReleaseId": "target", "targetVersion": "1.1.0",
		"trustDomain": "fixture-domain", "audience": "official", "signingIdentity": "FIXTURE_TEAM",
		"platform": runtime.GOOS, "arch": runtime.GOARCH,
		"runtimeRoot": f.runtimeRoot, "installationRoot": filepath.Join(root, "application"),
		"channel": "stable", "scope": map[string]any{"installation": filepath.Join(root, "application"), "dataRoots": []string{f.runtimeRoot, filepath.Join(root, "accepted-storage")}},
		"sourceStorage": map[string]any{"version": 2, "databases": storageFiles, "segmentStore": "SQLiteStore", "vectorEngine": "milvus-lite", "vectorPath": vectorPath, "layoutSource": "accepted-source-runtime", "optionalAbsent": []string{}},
		"packagePath":   packagePath, "packageSHA256": hex.EncodeToString(packageHash[:]), "packageSize": len(packageBytes), "expectedPeers": []string{"core", "scan"},
		"sourceDataProfile": "format-v2-fixture", "targetDataProfile": "format-v2-fixture",
		"sourceHadRAG": true, "componentMode": "sync-target",
		"sourceComponent": f.source, "targetComponent": f.target,
		"componentsPath": catalogPath, "componentsSHA256": hex.EncodeToString(catalogHash[:]),
		"componentsSize": len(catalogBytes), "policyDigest": strings.Repeat("a", 64),
	}
	f.intent = filepath.Join(f.updaterRoot, "native-v2", "active.json")
	f.save(t)
	t.Setenv("LAZYMIND_FIXTURE_COMPONENT_PROBE_LOG", f.probeLog)
	for _, bundle := range []string{f.sourceBundle, f.targetBundle} {
		if err := os.MkdirAll(bundle, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestMaintenanceProcessFixtureChild(t *testing.T) {
	root := os.Getenv("LAZYMIND_UNIT_MAINTENANCE_FIXTURE_ROOT")
	if root == "" {
		return
	}
	f := maintenanceFixtureAt(t, root, os.Getenv("LAZYMIND_UNIT_FIXTURE_RUNTIME_ROOT"), os.Getenv("LAZYMIND_UNIT_FIXTURE_UPDATER_ROOT"), false)
	writeMaintenanceFile(t, filepath.Join(root, "payload.zip"), f.archive)
	writeMaintenanceFile(t, filepath.Join(root, "record.json"), fixtureJSON(t, f.record))
	catalog := pythonCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]PythonComponent{"rag": f.source}}
	writeMaintenanceFile(t, filepath.Join(os.Getenv("LAZYMIND_UNIT_FIXTURE_RESOURCES_ROOT"), "config", "python-components.json"), fixtureJSON(t, catalog))
}

func fixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeMaintenanceFile(t *testing.T, filename string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeFixtureComponent(t *testing.T, root string, component PythonComponent) {
	t.Helper()
	dir := pythonComponentDir(root, component)
	if err := os.MkdirAll(filepath.Join(dir, "site-packages"), 0700); err != nil {
		t.Fatal(err)
	}
	writeMaintenanceFile(t, filepath.Join(dir, "bundle-manifest.json"), fixtureJSON(t, component))
	writeMaintenanceFile(t, filepath.Join(dir, ".ready"), []byte(component.SHA256))
}

// Verify the persisted receipt, not installedPythonDescriptor's fallback to
// its pinned argument when a receipt is missing or invalid.
func readMaintenanceSelection(root string, expected PythonComponent) error {
	filename := pythonDescriptorPath(root, expected)
	info, err := os.Lstat(filename)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("selection receipt is not a regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var selected PythonComponent
	if err := decoder.Decode(&selected); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("selection receipt has trailing content")
	}
	if !reflect.DeepEqual(selected, expected) || !pythonComponentInstalled(root, selected) {
		return fmt.Errorf("selection receipt does not bind the exact ready target")
	}
	return nil
}

func TestMaintenanceSelectionEvidenceCannotUsePinnedFallback(t *testing.T) {
	for _, scenario := range []string{"target", "missing", "corrupt", "old-revision", "wrong-hash", "wrong-tag", "wrong-packages"} {
		t.Run(scenario, func(t *testing.T) {
			f := newMaintenanceFixture(t, false)
			writeFixtureComponent(t, f.runtimeRoot, f.target)
			if err := savePythonDescriptor(f.runtimeRoot, f.target); err != nil {
				t.Fatal(err)
			}
			filename := pythonDescriptorPath(f.runtimeRoot, f.target)
			selected := f.target
			switch scenario {
			case "missing":
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				writeMaintenanceFile(t, filename, []byte("{broken"))
			case "old-revision":
				selected = f.source
			case "wrong-hash":
				selected.SHA256 = strings.Repeat("0", 64)
			case "wrong-tag":
				selected.Tag = strings.Repeat("0", 64)
			case "wrong-packages":
				selected.Packages = map[string]string{"fixture-package": "different"}
			}
			if scenario != "missing" && scenario != "corrupt" {
				writeMaintenanceFile(t, filename, fixtureJSON(t, selected))
			}
			err := readMaintenanceSelection(f.runtimeRoot, f.target)
			if (err == nil) != (scenario == "target") {
				t.Fatalf("selection evidence for %s: %v", scenario, err)
			}
			if scenario == "missing" || scenario == "corrupt" {
				if got := installedPythonDescriptor(f.runtimeRoot, f.target); got.Revision != f.target.Revision {
					t.Fatal("counterexample no longer reaches the pinned fallback")
				}
			}
		})
	}
}

func (f *maintenanceFixture) save(t *testing.T) {
	writeMaintenanceFile(t, f.intent, fixtureJSON(t, f.record))
}

func (f *maintenanceFixture) environment(target bool) PythonComponentMaintenanceContext {
	bundle, release := f.sourceBundle, "source"
	if target {
		bundle, release = f.targetBundle, "target"
	}
	return PythonComponentMaintenanceContext{
		RuntimeRoot: f.runtimeRoot, UpdaterRoot: f.updaterRoot, ResourcesRoot: bundle,
		ReleaseID: release, Python: filepath.Join(bundle, executableNameForFixture()),
		AlgorithmSource: filepath.Join(bundle, "app", "algorithm"), Client: f.client,
	}
}

func executableNameForFixture() string {
	if runtime.GOOS == "windows" {
		return "python.exe"
	}
	return "python"
}

func (f *maintenanceFixture) run(t *testing.T, action, status, reason string, target bool) map[string]any {
	t.Helper()
	raw, err := RunPythonComponentMaintenance(context.Background(), action,
		fixtureJSON(t, map[string]any{"transactionId": f.record["transactionId"]}), f.environment(target))
	if err != nil {
		t.Fatalf("maintenance capability unavailable instead of bounded result: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("invalid maintenance result: %v", err)
	}
	if response["status"] != status || (reason != "" && response["reason"] != reason) {
		t.Fatalf("%s result=%+v, want %s/%s", action, response, status, reason)
	}
	return response
}

func TestMaintenanceStageLeavesOldSelectionAndNeverMarksTargetReady(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	before, _ := os.ReadFile(f.intent)
	readyPath := filepath.Join(pythonComponentDir(f.runtimeRoot, f.source), ".ready")
	sourceReady, _ := os.ReadFile(readyPath)
	selectionBefore, err := os.ReadFile(pythonDescriptorPath(f.runtimeRoot, f.source))
	if err != nil {
		t.Fatal(err)
	}
	response := f.run(t, "stage", "prepared", "", false)
	stagePath, _ := response["stagePath"].(string)
	if !filepath.IsLocal(stagePath) {
		t.Fatalf("stage locator escaped its owned RuntimeRoot: %q", stagePath)
	}
	if _, err := os.Stat(filepath.Join(f.runtimeRoot, stagePath, "bundle-manifest.json")); err != nil {
		t.Fatal("stage did not contain the verified payload")
	}
	if _, err := os.Stat(filepath.Join(pythonComponentDir(f.runtimeRoot, f.target), ".ready")); !os.IsNotExist(err) {
		t.Fatal("stage activated target under the old Desktop")
	}
	if _, err := os.Stat(f.probeLog); !os.IsNotExist(err) {
		t.Fatal("stage used the source interpreter to validate the target")
	}
	after, _ := os.ReadFile(f.intent)
	afterSource, _ := os.ReadFile(readyPath)
	selectionAfter, _ := os.ReadFile(pythonDescriptorPath(f.runtimeRoot, f.source))
	if !bytes.Equal(selectionBefore, selectionAfter) || installedPythonDescriptor(f.runtimeRoot, f.target).Revision != f.source.Revision {
		t.Fatal("stage changed the active tag selection")
	}
	if !bytes.Equal(before, after) || !bytes.Equal(sourceReady, afterSource) || f.requests != 1 {
		t.Fatal("Core stage rewrote transaction authority/source or refreshed mutable metadata")
	}
}

func TestMaintenanceAbsentSourceDoesNotDownloadOrInstallRAG(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	if err := os.RemoveAll(filepath.Join(f.runtimeRoot, "deps", "python-components")); err != nil {
		t.Fatal(err)
	}
	f.record["sourceHadRAG"], f.record["componentMode"] = false, "absent"
	delete(f.record, "sourceComponent")
	delete(f.record, "targetComponent")
	f.save(t)
	f.run(t, "stage", "prepared", "", false)
	if f.requests != 0 {
		t.Fatal("lightweight update downloaded an optional component")
	}
	if _, err := os.Stat(filepath.Join(f.runtimeRoot, "deps", "python-components", "rag")); !os.IsNotExist(err) {
		t.Fatal("lightweight update created an RAG installation")
	}
}

func TestMaintenanceMatchingTargetCacheStillRunsTargetProcess(t *testing.T) {
	f := newMaintenanceFixture(t, true)
	response := f.run(t, "stage", "prepared", "", false)
	if f.requests != 0 {
		t.Fatal("exact valid target cache was needlessly downloaded")
	}
	f.record["phase"], f.record["componentStage"] = "installing", response
	f.save(t)
	env := buildMaintenanceProbe(t, f)
	f.run(t, "activate", "activated", "", true)
	if err := readMaintenanceSelection(f.runtimeRoot, f.target); err != nil {
		t.Fatalf("cached target has no valid selection receipt: %v", err)
	}
	var probe struct {
		Executable string
		Args       []string
	}
	raw, err := os.ReadFile(f.probeLog)
	if err != nil || json.Unmarshal(raw, &probe) != nil || probe.Executable != env.Python {
		t.Fatal("target cache skipped target-environment verification")
	}
	if !strings.Contains(strings.Join(probe.Args, "\n"), "site-packages") {
		t.Fatal("target probe did not receive the selected component payload")
	}
}

func TestMaintenanceRejectsBrokenSourceAndUnboundCatalog(t *testing.T) {
	for _, scenario := range []string{"source-ready", "catalog-hash", "catalog-size", "wrong-release", "wrong-phase"} {
		t.Run(scenario, func(t *testing.T) {
			f := newMaintenanceFixture(t, false)
			reason := "component_state_unknown"
			switch scenario {
			case "source-ready":
				writeMaintenanceFile(t, filepath.Join(pythonComponentDir(f.runtimeRoot, f.source), ".ready"), []byte("broken"))
			case "catalog-hash":
				f.record["componentsSHA256"] = strings.Repeat("0", 64)
				reason = "artifact_hash"
			case "catalog-size":
				f.record["componentsSize"] = 1
				reason = "artifact_size"
			case "wrong-release":
				f.record["sourceReleaseId"] = "another-source"
				reason = "update_transaction_conflict"
			case "wrong-phase":
				f.record["phase"] = "installing"
				reason = "update_transaction_conflict"
			}
			f.save(t)
			f.run(t, "stage", "blocked", reason, false)
			if f.requests != 0 {
				t.Fatal("unbound/invalid source reached component download")
			}
		})
	}
}

func TestMaintenanceActivationUsesActualTargetProcessAndKeepsStateWriterSeparate(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	response := f.run(t, "stage", "prepared", "", false)
	f.record["phase"], f.record["componentStage"] = "installing", response
	f.save(t)
	env := buildMaintenanceProbe(t, f)
	before, _ := os.ReadFile(f.intent)
	f.run(t, "activate", "activated", "", true)
	var probe struct {
		Executable string
		Args       []string
	}
	raw, err := os.ReadFile(f.probeLog)
	if err != nil || json.Unmarshal(raw, &probe) != nil || probe.Executable != env.Python {
		t.Fatalf("did not run the target interpreter boundary: %+v %v", probe, err)
	}
	stagePath, _ := response["stagePath"].(string)
	if !strings.Contains(strings.Join(probe.Args, "\n"), filepath.Join(f.runtimeRoot, stagePath, "site-packages")) {
		t.Fatal("target probe did not validate the staged payload")
	}
	if !pythonComponentInstalled(f.runtimeRoot, f.target) {
		t.Fatal("validated target was not atomically made ready")
	}
	if err := readMaintenanceSelection(f.runtimeRoot, f.target); err != nil {
		t.Fatalf("activation did not persist the exact target selection: %v", err)
	}
	after, _ := os.ReadFile(f.intent)
	if !bytes.Equal(before, after) {
		t.Fatal("component owner wrote a second transaction journal")
	}
	if !pythonComponentInstalled(f.runtimeRoot, f.source) {
		t.Fatal("target activation deleted the retained source component")
	}
}

func TestMaintenanceActivationRecoversPayloadReadyButSelectionStillOld(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	response := f.run(t, "stage", "prepared", "", false)
	f.record["phase"], f.record["componentStage"] = "installing", response
	f.save(t)
	buildMaintenanceProbe(t, f)
	// Crash checkpoint: payload and .ready landed, but the atomic tag receipt
	// has not switched. This is not a completed transaction or permission to skip
	// validation under the actual target interpreter.
	writeFixtureComponent(t, f.runtimeRoot, f.target)
	if got := installedPythonDescriptor(f.runtimeRoot, f.target); got.Revision != f.source.Revision {
		t.Fatal("fixture did not retain the old selection")
	}
	f.run(t, "activate", "activated", "", true)
	if err := readMaintenanceSelection(f.runtimeRoot, f.target); err != nil {
		t.Fatalf("same transaction failed to repair tag selection: %v", err)
	}
	if _, err := os.Stat(f.probeLog); err != nil {
		t.Fatal("ready payload skipped actual target verification")
	}
}

func buildMaintenanceProbe(t *testing.T, f *maintenanceFixture) PythonComponentMaintenanceContext {
	t.Helper()
	env := f.environment(true)
	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", env.Python, "./testdata/component-probe")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build portable process boundary: %v\n%s", err, output)
	}
	t.Setenv("LAZYMIND_ALGORITHM_PYTHON", filepath.Join(f.sourceBundle, executableNameForFixture()))
	return env
}

func TestMaintenanceFailedTargetProbeNeverMarksReady(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	response := f.run(t, "stage", "prepared", "", false)
	f.record["phase"], f.record["componentStage"] = "installing", response
	f.save(t)
	buildMaintenanceProbe(t, f)
	t.Setenv("LAZYMIND_FIXTURE_COMPONENT_PROBE_FAIL", "1")
	f.run(t, "activate", "blocked", "component_target_verification_failed", true)
	if _, err := os.Stat(filepath.Join(pythonComponentDir(f.runtimeRoot, f.target), ".ready")); !os.IsNotExist(err) {
		t.Fatal("failed target verification produced a ready marker")
	}
	if !pythonComponentInstalled(f.runtimeRoot, f.source) {
		t.Fatal("failed target activation damaged the original component")
	}
}

func TestMaintenanceProbeCancellationAndDeadlineWaitForActualClose(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			f := newMaintenanceFixture(t, false)
			response := f.run(t, "stage", "prepared", "", false)
			f.record["phase"], f.record["componentStage"] = "installing", response
			f.save(t)
			env := buildMaintenanceProbe(t, f)
			hold := t.TempDir()
			t.Setenv("LAZYMIND_FIXTURE_PROBE_HOLD_DIR", hold)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			request := fixtureJSON(t, map[string]any{"transactionId": f.record["transactionId"]})
			go func() { _, err := RunPythonComponentMaintenance(ctx, "activate", request, env); done <- err }()
			t.Cleanup(func() { cancel(); _ = os.WriteFile(filepath.Join(hold, "release"), nil, 0600) })
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			for {
				if _, err := os.Stat(filepath.Join(hold, "entered")); err == nil {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("probe never reached real child: %v", err)
				case <-deadline.C:
					t.Fatal("probe process did not start")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if mode == "cancel" {
				cancel()
			} else {
				<-ctx.Done()
			}
			// If maintenance returns, an independent OS lock probe must prove that
			// the target child has closed. A kill request alone is not that proof.
			select {
			case <-done:
				probe := exec.Command(env.Python)
				probe.Env = append(os.Environ(), "LAZYMIND_FIXTURE_COMPONENT_LOCK_PATH="+filepath.Join(hold, "alive.lock"))
				output, err := probe.Output()
				if err != nil || strings.TrimSpace(string(output)) != "locked" {
					t.Fatalf("maintenance returned with a target child still alive: %s %v", output, err)
				}
			case <-time.After(50 * time.Millisecond):
				probe := exec.Command(env.Python)
				probe.Env = append(os.Environ(), "LAZYMIND_FIXTURE_COMPONENT_LOCK_PATH="+filepath.Join(f.runtimeRoot, "deps", "python-components", ".native-update.lock"))
				output, err := probe.Output()
				if err != nil || strings.TrimSpace(string(output)) != "busy" {
					t.Fatalf("unclosed probe lost actual writer lock: %s %v", output, err)
				}
				_ = os.WriteFile(filepath.Join(hold, "release"), nil, 0600)
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("maintenance failed to finish after actual close")
				}
			}
			if pythonComponentInstalled(f.runtimeRoot, f.target) || installedPythonDescriptor(f.runtimeRoot, f.target).Revision != f.source.Revision {
				t.Fatal("cancelled/timed-out child or its late output activated the target")
			}
		})
	}
}

func TestMaintenanceAbsentFlagCannotHideExistingInstallationEvidence(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	writeMaintenanceFile(t, filepath.Join(pythonComponentDir(f.runtimeRoot, f.source), ".ready"), []byte("corrupt"))
	f.record["sourceHadRAG"], f.record["componentMode"] = false, "absent"
	delete(f.record, "sourceComponent")
	delete(f.record, "targetComponent")
	f.save(t)
	f.run(t, "stage", "blocked", "component_state_unknown", false)
	if f.requests != 0 {
		t.Fatal("damaged installation was silently treated as lightweight")
	}
}

func TestMaintenanceCancellationKeepsSourceAndIntent(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	before, _ := os.ReadFile(f.intent)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunPythonComponentMaintenance(ctx, "stage",
		fixtureJSON(t, map[string]any{"transactionId": f.record["transactionId"]}), f.environment(false))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stage did not stop: %v", err)
	}
	after, _ := os.ReadFile(f.intent)
	if !bytes.Equal(before, after) || !pythonComponentInstalled(f.runtimeRoot, f.source) || f.requests != 0 {
		t.Fatal("cancelled stage changed the active transaction/source")
	}
}

func TestMaintenanceActualWriterHoldsComponentLockUntilDownloadStops(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	env := buildMaintenanceProbe(t, f)
	entered := make(chan struct{})
	f.client.Transport = maintenanceHTTP(func(request *http.Request) (*http.Response, error) {
		close(entered)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	request := fixtureJSON(t, map[string]any{"transactionId": f.record["transactionId"]})
	go func() {
		_, err := RunPythonComponentMaintenance(ctx, "stage",
			request, f.environment(false))
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("maintenance writer did not stop after cancellation")
		}
	})
	select {
	case <-entered:
	case err := <-done:
		done <- err
		t.Fatalf("stage capability did not reach the controlled transport: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("stage never reached controlled download")
	}
	lockFile := filepath.Join(f.runtimeRoot, "deps", "python-components", ".native-update.lock")
	probe := exec.Command(env.Python)
	probe.Env = append(os.Environ(), "LAZYMIND_FIXTURE_COMPONENT_LOCK_PATH="+lockFile)
	output, err := probe.Output()
	if err != nil || strings.TrimSpace(string(output)) != "busy" {
		t.Fatalf("the actual writing process did not hold the OS lock: %q %v", output, err)
	}
	cancel()
	select {
	case err := <-done:
		done <- err
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled download did not finish as cancelled: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("download kept running after cancellation")
	}
	probe = exec.Command(env.Python)
	probe.Env = append(os.Environ(), "LAZYMIND_FIXTURE_COMPONENT_LOCK_PATH="+lockFile)
	output, err = probe.Output()
	if err != nil || strings.TrimSpace(string(output)) != "locked" {
		t.Fatalf("exited writer leaked its component lock: %q %v", output, err)
	}
}

func TestMaintenanceDownloadedBytesMustMatchFixedHash(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	f.archive = append([]byte(nil), f.archive...)
	f.archive[len(f.archive)-1] ^= 1
	f.run(t, "stage", "blocked", "artifact_hash", false)
	if _, err := os.Stat(filepath.Join(pythonComponentDir(f.runtimeRoot, f.target), ".ready")); !os.IsNotExist(err) {
		t.Fatal("corrupt downloaded bytes became ready")
	}
}

func TestMaintenanceRejectsTraversalEvenWhenArchiveHashIsBound(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	filename := writePythonZIP(t, map[string]string{
		"bundle-manifest.json":       string(fixtureJSON(t, f.target)),
		"site-packages/../../escape": "never write this",
	})
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	f.archive = raw
	hash := sha256.Sum256(raw)
	f.target.SHA256, f.target.SizeBytes = hex.EncodeToString(hash[:]), int64(len(raw))
	catalog := pythonCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH,
		Components: map[string]PythonComponent{"rag": f.target}}
	catalogBytes := fixtureJSON(t, catalog)
	catalogHash := sha256.Sum256(catalogBytes)
	f.record["targetComponent"] = f.target
	f.record["componentsSHA256"], f.record["componentsSize"] = hex.EncodeToString(catalogHash[:]), len(catalogBytes)
	writeMaintenanceFile(t, f.record["componentsPath"].(string), catalogBytes)
	f.save(t)
	f.run(t, "stage", "blocked", "component_archive_invalid", false)
	if _, err := os.Stat(filepath.Join(f.runtimeRoot, "escape")); !os.IsNotExist(err) {
		t.Fatal("archive wrote outside its owned staging location")
	}
	if !pythonComponentInstalled(f.runtimeRoot, f.source) {
		t.Fatal("rejected archive changed the source")
	}
}
