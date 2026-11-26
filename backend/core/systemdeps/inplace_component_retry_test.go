package systemdeps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The target process runs reconciliation independently of opening the app.
// The shorter retry delay is a test clock setting, not a release input.
type componentReconciler interface {
	ReconcileSelectedPythonComponents(context.Context, time.Duration) error
}

func TestComponentRetrySurvivesTargetRestart(t *testing.T) {
	f := newMaintenanceFixture(t, false)
	_, py := replacementInterpreter(t)
	f.target, f.archive = replacementPayload(t, "rag", "B", py)
	cache := fixtureJSON(t, map[string]any{"schemaVersion": 1, "platform": runtime.GOOS, "arch": runtime.GOARCH, "runtimeBaseFingerprint": py.Base, "components": map[string]PythonComponent{"rag": f.target}})
	f.record["targetComponent"] = f.target
	f.record["componentsSHA256"], f.record["componentsSize"] = fmt.Sprintf("%x", sha256.Sum256(cache)), len(cache)
	writeMaintenanceFile(t, f.record["componentsPath"].(string), cache)
	writeMaintenanceFile(t, filepath.Join(f.targetBundle, "config", "python-components.json"), cache)
	// Simulate a hash-bound target archive whose native dependency cannot load
	// on B. The target interpreter records the failed import even if the worker
	// catches its exit status. Restoring the dependency permits a later retry.
	purelib, err := exec.CommandContext(t.Context(), py.Exe, "-I", "-c", "import sysconfig; print(sysconfig.get_path('purelib'))").Output()
	if err != nil {
		t.Fatal(err)
	}
	siteDir := strings.TrimSpace(string(purelib))
	blocker := filepath.Join(siteDir, "lazymind_fixture_import_guard.pth")
	importGuard := fmt.Sprintf(`import os, sys
class UnavailableRAG:
    def find_spec(self, name, path=None, target=None):
        if name == "pymilvus":
            with open(%q, "a") as log:
                log.write(str(os.getpid()) + "\n")
            raise ImportError("unavailable native dependency")
sys.meta_path.insert(0, UnavailableRAG())
`, f.probeLog)
	writeMaintenanceFile(t, filepath.Join(siteDir, "lazymind_fixture_import_guard.py"), []byte(importGuard))
	writeMaintenanceFile(t, blocker, []byte("import lazymind_fixture_import_guard\n"))
	f.record["phase"] = "installing"
	f.record["installationRoot"] = f.targetBundle
	f.record["scope"].(map[string]any)["installation"] = f.targetBundle
	f.save(t)
	// B persists the source user's choice before the main transaction commits.
	// An unreachable component server cannot delay this handoff.
	f.client.Transport = maintenanceHTTP(func(*http.Request) (*http.Response, error) {
		t.Error("saving the pending selection unexpectedly attempted a download")
		return nil, io.ErrUnexpectedEOF
	})
	f.run(t, "defer", "deferred", "", true)
	if err := os.Remove(f.intent); err != nil {
		t.Fatal(err)
	}

	run := func(transport http.RoundTripper) (context.CancelFunc, <-chan error) {
		t.Helper()
		env := f.environment(true)
		env.Python = py.Exe
		env.Client = &http.Client{Transport: transport}
		worker, ok := any(&env).(componentReconciler)
		if !ok {
			t.Fatal("target has no background component reconciler")
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- worker.ReconcileSelectedPythonComponents(ctx, 50*time.Millisecond) }()
		return cancel, done
	}
	waitExit := func(done <-chan error) {
		t.Helper()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("component worker outlived the target process")
		}
	}
	failed := make(chan struct{}, 1)
	var failures atomic.Int32
	stop, stopped := run(maintenanceHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != f.target.URL && r.URL.String() != f.target.CatalogURL {
			t.Errorf("worker requested an unselected asset %s", r.URL.Path)
		}
		failures.Add(1)
		select {
		case failed <- struct{}{}:
		default:
		}
		return nil, io.ErrUnexpectedEOF
	}))
	defer stop()
	select {
	case <-failed:
	case <-time.After(5 * time.Second):
		t.Fatal("offline worker did not attempt the selected update")
	}
	time.Sleep(150 * time.Millisecond)
	stop()
	waitExit(stopped)
	if n := failures.Load(); n > 10 {
		t.Fatalf("%d attempts in 150 ms: offline retries must back off", n)
	}
	if pythonComponentInstalled(f.runtimeRoot, f.target) {
		t.Fatal("failed download marked target as installed")
	}
	if !pythonComponentInstalled(f.runtimeRoot, f.source) {
		t.Fatal("failed download deleted the previous payload")
	}

	// No new install request: reopen the same disk state and let the worker
	// retry on its own. The first response is damaged. Later responses have
	// the signed hash, but B must also verify its native imports.
	var attempts atomic.Int32
	stop, stopped = run(maintenanceHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == f.target.CatalogURL {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r,
				Body: io.NopCloser(bytes.NewReader(cache))}, nil
		}
		if r.URL.String() != f.target.URL {
			t.Errorf("worker requested an unselected asset %s", r.URL.Path)
			return nil, io.ErrUnexpectedEOF
		}
		if attempts.Add(1) == 1 {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r,
				Body: io.NopCloser(bytes.NewReader([]byte("damaged component")))}, nil
		}
		if pythonComponentInstalled(f.runtimeRoot, f.target) {
			t.Error("worker activated bytes whose digest did not match")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r,
			Body: io.NopCloser(bytes.NewReader(f.archive))}, nil
	}))
	defer stop()
	deadline := time.Now().Add(15 * time.Second)
	for {
		raw, _ := os.ReadFile(f.probeLog)
		pids := strings.Fields(string(raw))
		if len(pids) >= 2 && pids[0] != pids[len(pids)-1] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("B did not retry after the first target interpreter exited unsuccessfully")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pythonComponentInstalled(f.runtimeRoot, f.target) || readMaintenanceSelection(f.runtimeRoot, f.source) != nil {
		t.Fatal("live worker accepted the failed import or lost the previous selection")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && readMaintenanceSelection(f.runtimeRoot, f.target) != nil {
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	waitExit(stopped)
	if err := readMaintenanceSelection(f.runtimeRoot, f.target); err != nil {
		t.Fatal("restarted worker did not install and select the target revision", err)
	}
	if _, err := os.Stat(f.probeLog); err != nil {
		t.Fatal("target interpreter never verified the downloaded payload", err)
	}
	checkReplacementImport(t, py, filepath.Join(pythonComponentDir(f.runtimeRoot, f.target), "site-packages"), "rag", "B", true)
	if attempts.Load() < 2 {
		t.Fatal("target revision was selected before the successful retry")
	}
}

// This check starts main() in independent OS processes. No test code invokes
// the background reconciler. A separate native contract checks that complete
// only removes the active transaction after the user's selections are durable.
func TestComponentRetryIsRegisteredInDesktopCore(t *testing.T) {
	root := t.TempDir()
	f := maintenanceFixtureAt(t, root, "", "", false)
	// A selected only RAG; B publishes an equally available Cluster.
	_, py := replacementInterpreter(t)
	cluster, clusterArchive := replacementPayload(t, "cluster", "B", py)
	writeMaintenanceFile(t, filepath.Join(root, "cluster.zip"), clusterArchive)
	cache := fixtureJSON(t, pythonCatalog{SchemaVersion: 1, Platform: runtime.GOOS, Arch: runtime.GOARCH, Components: map[string]PythonComponent{"rag": f.target, "cluster": cluster}})
	f.record["componentsSHA256"], f.record["componentsSize"] = fmt.Sprintf("%x", sha256.Sum256(cache)), len(cache)
	writeMaintenanceFile(t, f.record["componentsPath"].(string), cache)
	writeMaintenanceFile(t, filepath.Join(root, "catalog.json"), cache)
	writeMaintenanceFile(t, filepath.Join(f.targetBundle, "config", "python-components.json"), cache)
	f.record["phase"], f.record["installationRoot"] = "installing", f.targetBundle
	f.record["scope"].(map[string]any)["installation"] = f.targetBundle
	f.save(t)
	nativeName := func(s string) string {
		if runtime.GOOS == "windows" {
			return s + ".exe"
		}
		return s
	}
	core := filepath.Join(root, nativeName("core-test"))
	build := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-c", "-o", core, ".")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Core: %v %s", err, out)
	}
	helper := filepath.Join(f.targetBundle, "bin", nativeName("lazymind-update-helper"))
	source := filepath.Join(root, "identity.go")
	writeMaintenanceFile(t, source, []byte(`package main
import("os";"encoding/json")
func main(){ if len(os.Args)!=2 || os.Args[1]!="verify-identity" { os.Exit(2) }; json.NewEncoder(os.Stdout).Encode(map[string]any{"status":"enabled","config":map[string]string{"releaseId":"target"}}) }`))
	if out, err := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", helper, source).CombinedOutput(); err != nil {
		t.Fatalf("build identity fixture: %v %s", err, out)
	}
	python := filepath.Join(f.targetBundle, "deps", "python", "algorithm", "bin", "python")
	if runtime.GOOS == "windows" {
		python = filepath.Join(f.targetBundle, "deps", "python", "algorithm", "Scripts", "python.exe")
	}
	if out, err := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", python, "./testdata/component-probe").CombinedOutput(); err != nil {
		t.Fatalf("build target probe: %v %s", err, out)
	}
	coreDB, stateDB := filepath.Join(root, "core.db"), filepath.Join(root, "state.db")
	base := []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "LAZYMIND_UNIT_COMPONENT_ENTRY=1", "LAZYMIND_UNIT_RETRY_DIR=" + root,
		"LAZYMIND_UNIT_CLUSTER_URL=" + cluster.URL, "LAZYMIND_UNIT_CLUSTER_CATALOG_URL=" + cluster.CatalogURL, "LAZYMIND_UNIT_RAG_URL=" + f.target.URL, "LAZYMIND_UNIT_CATALOG_URL=" + f.target.CatalogURL,
		"LAZYMIND_RUNTIME_MODE=local", "LAZYMIND_RUNTIME_ROOT=" + f.runtimeRoot, "LAZYMIND_RUNTIME_RESOURCES_ROOT=" + f.targetBundle,
		"LAZYMIND_ALGORITHM_PYTHON=" + python, "LAZYMIND_PYTHON_COMPONENT_CATALOG=" + filepath.Join(f.targetBundle, "config", "python-components.json"),
		"LAZYMIND_FIXTURE_COMPONENT_PROBE_LOG=" + f.probeLog, "LAZYMIND_ALGORITHM_SOURCE=" + filepath.Join(f.targetBundle, "app", "algorithm"),
		"ACL_DB_DRIVER=sqlite", "ACL_DB_DSN=" + coreDB, "LAZYMIND_READONLY_DB_DRIVER=sqlite", "LAZYMIND_READONLY_DB_DSN=" + filepath.Join(root, "docs.db"),
		"LAZYMIND_READONLY_SCHEMA=", "LAZYMIND_STATE_BACKEND=sqlite", "LAZYMIND_STATE_SQLITE_PATH=" + stateDB,
		"LAZYMIND_CORE_HOST=127.0.0.1", "LAZYMIND_CORE_PORT=0", "LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN=fixture-token", "LAZYMIND_OPENAPI_ARTIFACT_EXPORT_ENABLED=false",
		"LAZYMIND_HISTORY_INJECTION_ENABLED=false", "LAZYMIND_RAG_ENABLED=false", "LAZYMIND_CLUSTER_ENABLED=0", "LAZYMIND_WORKFLOW_BUILTIN_ROOT=" + t.TempDir()}
	child := func(ctx context.Context, env ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, core, "-test.run=^TestInPlaceComponentRetryCoreChild$")
		cmd.Dir = ".."
		cmd.Env = append(slices.Clone(base), env...)
		return cmd
	}
	tx := f.record["transactionId"].(string)
	start := func(sealed bool) (string, func()) {
		env := []string{"LAZYMIND_UNIT_CORE_BOOTSTRAP_ENTRY=1"}
		if sealed {
			storage := fixtureJSON(t, map[string]any{"version": 2, "databases": map[string]string{"core": coreDB, "core-state": stateDB}})
			env = append(env, "LAZYMIND_UPDATE_BOOTSTRAP_TRANSACTION="+tx, "LAZYMIND_UPDATE_BOOTSTRAP_OWNER=fixture-owner", "LAZYMIND_UPDATE_BOOTSTRAP_STORAGE="+string(storage))
		}
		logfile, err := os.CreateTemp(root, "core-*.log")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		cmd := child(ctx, env...)
		cmd.Stderr = logfile
		cmd.Stdout = logfile
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		stop := func() { cancel(); <-done; logfile.Close() }
		t.Cleanup(stop)
		listening := regexp.MustCompile(`Core listening[^\n]*?(127\.0\.0\.1:[0-9]+)`)
		for {
			raw, _ := os.ReadFile(logfile.Name())
			if match := listening.FindSubmatch(raw); match != nil {
				return string(match[1]), stop
			}
			select {
			case <-done:
				t.Fatalf("Core exited before opening: %s", raw)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	// Keep stdin open as the real manager does while it waits for the ACK.
	reader, writer := io.Pipe()
	defer writer.Close()
	request := append(fixtureJSON(t, map[string]string{"transactionId": tx}), '\n')
	go func() { _, _ = writer.Write(request) }()
	cmd := child(t.Context(), `LAZYMIND_UNIT_COMPONENT_ARGS=["--component-maintenance","defer"]`)
	cmd.Stdin = reader
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var ack struct{ Status, Reason string }
	err = json.NewDecoder(output).Decode(&ack)
	writer.Close()
	if exitErr := cmd.Wait(); err != nil || exitErr != nil || ack.Status != "deferred" {
		t.Fatalf("Core maintenance did not persist a retry: %+v %v %v", ack, err, exitErr)
	}
	if err := os.Remove(f.intent); err != nil {
		t.Fatal(err)
	}
	addr, stop := start(true)
	asked := filepath.Join(root, "requested.payload.zip")
	waitFor := func(label string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatal(label)
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	time.Sleep(250 * time.Millisecond)
	for _, file := range []string{asked, filepath.Join(root, "requested.catalog.json"), f.probeLog} {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("sealed Core started optional work %s", file)
		}
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/internal/desktop-update", bytes.NewReader(fixtureJSON(t, map[string]string{"action": "bootstrap-complete", "transactionId": tx, "owner": "fixture-owner"})))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-LazyMind-Internal-Token", "fixture-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("bootstrap release returned %d", resp.StatusCode)
	}
	waitFor("releasing the seal never started the pending download", func() bool { _, e := os.Stat(asked); return e == nil })
	stop()
	if !pythonComponentInstalled(f.runtimeRoot, f.source) || pythonComponentInstalled(f.runtimeRoot, f.target) {
		t.Fatal("offline Core lost or prematurely activated the selection")
	}
	writeMaintenanceFile(t, filepath.Join(root, "payload.zip"), f.archive)
	_, stop = start(false)
	waitFor("reopened Core did not restore the selected B component", func() bool { return readMaintenanceSelection(f.runtimeRoot, f.target) == nil })
	stop()
	if _, err := os.Stat(f.probeLog); err != nil {
		t.Fatal("background install skipped target verification", err)
	}
	for _, name := range []string{"requested.cluster.zip", "requested.cluster-catalog.json", pythonDescriptorPath(f.runtimeRoot, cluster)} {
		file := name
		if !filepath.IsAbs(file) {
			file = filepath.Join(root, name)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("unselected Cluster accessed or selected: %s (%v)", file, err)
		}
	}
	t.Setenv("LAZYMIND_CLUSTER_ENABLED", "0")
	status := pythonComponentStatus(f.runtimeRoot, &pythonCatalog{Components: map[string]PythonComponent{"cluster": cluster}}, "cluster")
	if status.Installed || status.Active {
		t.Fatalf("unselected Cluster was made available: %+v", status)
	}
}
