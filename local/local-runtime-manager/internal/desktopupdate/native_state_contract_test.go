package desktopupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type nativeStateFixture struct {
	root, runtimeRoot, installation, tx, filename string
	record                                        map[string]any
}

func newNativeStateFixture(t *testing.T) *nativeStateFixture {
	t.Helper()
	base := t.TempDir()
	f := &nativeStateFixture{
		root: filepath.Join(base, "updater"), runtimeRoot: filepath.Join(base, "runtime"),
		installation: filepath.Join(base, "application"), tx: strings.Repeat("1", 32),
	}
	for _, dir := range []string{f.root, f.runtimeRoot, f.installation} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.filename = filepath.Join(f.root, "native-v2", "active.json")
	// This boundary receives the already-accepted aggregate. Real paths and
	// immutable artifact bytes are still required; RuntimeRoot cannot replace
	// the storage snapshot when a user selected an external database location.
	custom := filepath.Join(base, "custom-data")
	if err := os.MkdirAll(custom, 0700); err != nil {
		t.Fatal(err)
	}
	databases := map[string]string{}
	for _, family := range []string{"core", "auth", "lazyllm", "segments", "channel", "scan", "core-state", "auth-state", "scan-state"} {
		filename := filepath.Join(custom, family+".db")
		if err := os.WriteFile(filename, []byte("accepted-storage-boundary-fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		databases[family] = filename
	}
	vector := filepath.Join(custom, "indexes")
	if err := os.MkdirAll(vector, 0700); err != nil {
		t.Fatal(err)
	}
	artifacts := map[string]any{}
	for _, name := range []string{"package", "components"} {
		raw := []byte("synthetic-non-executable-" + name)
		filename := filepath.Join(f.root, name+".fixture")
		if err := os.WriteFile(filename, raw, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		artifacts[name+"Path"], artifacts[name+"Size"], artifacts[name+"SHA256"] = filename, len(raw), hex.EncodeToString(sum[:])
	}
	f.record = map[string]any{
		"version": 2, "transactionId": f.tx, "phase": "pending",
		"sourceReleaseId": "source", "sourceVersion": "1.0.0",
		"targetReleaseId": "target", "targetVersion": "1.1.0",
		"trustDomain": "fixture-domain", "audience": "official", "channel": "stable",
		"signingIdentity": "FIXTURE_TEAM", "platform": "darwin", "arch": "arm64",
		"runtimeRoot": f.runtimeRoot, "installationRoot": f.installation,
		"scope":             map[string]any{"installation": f.installation, "dataRoots": []string{f.runtimeRoot, custom}},
		"sourceDataProfile": "format-v2-fixture", "targetDataProfile": "format-v2-fixture",
		"sourceHadRAG": false, "componentMode": "absent",
		"expectedPeers": []string{"core", "scan"},
		"packageSHA256": strings.Repeat("a", 64), "componentsSHA256": strings.Repeat("b", 64),
		"policyDigest": strings.Repeat("c", 64), "policySequence": 42,
		"sourceOwnerDigest": strings.Repeat("d", 64),
		"sourceStorage": map[string]any{"version": 2, "databases": databases,
			"segmentStore": "SQLiteStore", "vectorEngine": "milvus-lite", "vectorPath": vector,
			"layoutSource": "accepted-source-runtime", "optionalAbsent": []string{}},
	}
	for key, value := range artifacts {
		f.record[key] = value
	}
	return f
}

func (f *nativeStateFixture) assertOriginalBindings(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(f.filename)
	var actual map[string]any
	if err != nil || json.Unmarshal(raw, &actual) != nil {
		t.Fatalf("read original bindings: %v", err)
	}
	// Compare through JSON because input sizes are Go ints and persisted values
	// are JSON numbers. No target or repair may reconstruct default paths.
	var expected map[string]any
	if err := json.Unmarshal(nativeJSON(t, f.record), &expected); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sourceStorage", "sourceReleaseId", "targetReleaseId", "packagePath", "packageSize", "packageSHA256", "componentsPath", "componentsSize", "componentsSHA256", "sourceDataProfile"} {
		if !reflect.DeepEqual(actual[key], expected[key]) {
			t.Fatalf("immutable %s changed: got=%v want=%v", key, actual[key], expected[key])
		}
	}
}

func TestNativeStateCannotHandoffWithoutCompleteSourceBindings(t *testing.T) {
	for _, scenario := range []string{"storage", "one-database", "vector", "package-size", "components-size", "duplicate-database", "scope-misses-custom-data"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNativeStateFixture(t)
			storage := f.record["sourceStorage"].(map[string]any)
			switch scenario {
			case "storage":
				delete(f.record, "sourceStorage")
			case "one-database":
				delete(storage["databases"].(map[string]string), "auth")
			case "vector":
				delete(storage, "vectorPath")
			case "package-size":
				delete(f.record, "packageSize")
			case "components-size":
				delete(f.record, "componentsSize")
			case "duplicate-database":
				storage["databases"].(map[string]string)["auth"] = storage["databases"].(map[string]string)["core"]
			case "scope-misses-custom-data":
				f.record["scope"] = map[string]any{"installation": f.installation, "dataRoots": []string{f.runtimeRoot}}
			}
			f.command(t, "create", f.record, "blocked", "update_request_invalid")
			if _, err := os.Stat(f.filename); !os.IsNotExist(err) {
				t.Fatal("incomplete source facts became a live transaction")
			}
		})
	}
}

func nativeJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *nativeStateFixture) command(t *testing.T, action string, payload any, status, reason string) map[string]any {
	t.Helper()
	raw, err := NativeUpdateStateCommand(context.Background(), f.root, action, nativeJSON(t, payload))
	if err != nil {
		t.Fatalf("native state capability unavailable: %v", err)
	}
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil || result["status"] != status || (reason != "" && result["reason"] != reason) {
		t.Fatalf("%s result=%s want %s/%s", action, raw, status, reason)
	}
	return result
}

func (f *nativeStateFixture) create(t *testing.T) {
	f.command(t, "create", f.record, "pending", "")
}

func TestNativeStateCreateIsSingleWriterAndPendingCancelIsSafe(t *testing.T) {
	f := newNativeStateFixture(t)
	f.create(t)
	before, err := os.ReadFile(f.filename)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if json.Unmarshal(before, &stored) != nil || stored["transactionId"] != f.tx {
		t.Fatal("state writer did not persist the accepted transaction")
	}
	other := map[string]any{}
	for key, value := range f.record {
		other[key] = value
	}
	other["transactionId"] = strings.Repeat("2", 32)
	f.command(t, "create", other, "blocked", "update_transaction_conflict")
	f.command(t, "cancel", map[string]any{"transactionId": other["transactionId"]}, "blocked", "update_transaction_conflict")
	after, _ := os.ReadFile(f.filename)
	if !bytes.Equal(before, after) {
		t.Fatal("another writer changed the live transaction")
	}
	f.command(t, "cancel", map[string]any{"transactionId": f.tx}, "cancelled", "")
	if _, err := os.Stat(f.filename); !os.IsNotExist(err) {
		t.Fatal("safe pending cancellation did not end its intent")
	}
}

func nativePeerProofs(tx string) map[string]any {
	peer := func() map[string]any {
		return map[string]any{
			"transactionId": tx, "owner": "fixture-target-owner", "ready": true, "sealed": true,
		}
	}
	return map[string]any{"core": peer(), "scan": peer()}
}

func (f *nativeStateFixture) installing(t *testing.T) {
	f.create(t)
	f.command(t, "installing", map[string]any{
		"transactionId": f.tx, "dataProfile": "format-v2-fixture",
		"componentMode": "absent", "sourcePaused": true, "sourceActive": 0,
	}, "installing", "")
}

func TestNativeStateAfterHandoffCannotCancelOrChangeImmutableTarget(t *testing.T) {
	f := newNativeStateFixture(t)
	f.installing(t)
	before, _ := os.ReadFile(f.filename)
	f.command(t, "cancel", map[string]any{"transactionId": f.tx}, "blocked", "native_handoff_unknown")
	f.command(t, "activated", map[string]any{
		"transactionId": f.tx, "releaseId": "another-target", "dataProfile": "format-v2-fixture",
	}, "blocked", "update_transaction_conflict")
	after, _ := os.ReadFile(f.filename)
	if !bytes.Equal(before, after) {
		t.Fatal("unknown handoff lost its pinned state")
	}
}

func TestNativeStateCompletionRequiresBothPeersAndSameTarget(t *testing.T) {
	for _, scenario := range []string{"scan-not-ready", "scan-wrong-tx", "wrong-format", "complete"} {
		t.Run(scenario, func(t *testing.T) {
			f := newNativeStateFixture(t)
			f.installing(t)
			f.command(t, "activated", map[string]any{
				"transactionId": f.tx, "releaseId": "target", "dataProfile": "format-v2-fixture",
				"componentMode": "absent", "components": map[string]string{},
			}, "activated", "")
			f.assertOriginalBindings(t)
			proof := map[string]any{
				"transactionId": f.tx, "releaseId": "target", "dataProfile": "format-v2-fixture",
				"dirty": false, "components": map[string]string{}, "peers": nativePeerProofs(f.tx),
			}
			peers := proof["peers"].(map[string]any)
			switch scenario {
			case "scan-not-ready":
				peers["scan"].(map[string]any)["ready"] = false
			case "scan-wrong-tx":
				peers["scan"].(map[string]any)["transactionId"] = strings.Repeat("3", 32)
			case "wrong-format":
				proof["dataProfile"] = "other-format"
			}
			if scenario == "complete" {
				f.command(t, "complete", proof, "completed", "")
				if _, err := os.Stat(f.filename); !os.IsNotExist(err) {
					t.Fatal("successful commit left a live intent")
				}
			} else {
				f.command(t, "complete", proof, "blocked", "update_startup_failed")
				if _, err := os.Stat(f.filename); err != nil {
					t.Fatal("failed verification cleared the live intent")
				}
			}
		})
	}
}

func TestNativeComponentLockChild(t *testing.T) {
	filename := os.Getenv("LAZYMIND_UNIT_NATIVE_COMPONENT_LOCK")
	if filename == "" {
		return
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		os.Exit(2)
	}
	locked, err := tryLock(file)
	if err != nil || !locked {
		os.Exit(3)
	}
	fmt.Fprintln(os.Stdout, "locked")
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
	unlock(file)
	_ = file.Close()
	os.Exit(0)
}

func holdNativeComponentWriter(t *testing.T, runtimeRoot string) {
	t.Helper()
	dir := filepath.Join(runtimeRoot, "deps", "python-components")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, executable, "-test.run=^TestNativeComponentLockChild$")
	command.Env = append(os.Environ(), "LAZYMIND_UNIT_NATIVE_COMPONENT_LOCK="+filepath.Join(dir, ".native-update.lock"))
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = input.Write([]byte("x"))
		_ = input.Close()
		if err := command.Wait(); err != nil {
			t.Errorf("component writer child: %v", err)
		}
	})
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("child did not hold real OS lock: %q %v", line, err)
	}
}

func TestNativeLongWriterCannotBlockUnrelatedStartupOrAllowCancellation(t *testing.T) {
	f := newNativeStateFixture(t)
	f.create(t)
	holdNativeComponentWriter(t, f.runtimeRoot)
	before, _ := os.ReadFile(f.filename)
	start := time.Now()
	f.command(t, "cancel", map[string]any{"transactionId": f.tx}, "blocked", "update_transaction_busy")
	foreign := filepath.Join(t.TempDir(), "unrelated")
	if err := os.MkdirAll(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	f.command(t, "inspect", map[string]any{
		"releaseId": "unrelated", "installationRoot": foreign, "runtimeRoot": foreign,
		"scope": map[string]any{"installation": foreign, "dataRoots": []string{foreign}},
	}, "allowed", "")
	actor, err := CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := BeginStartup(context.Background(), f.root,
		UpdateScope{Installation: foreign, DataRoots: []string{foreign}}, "fixture-owner", "writer", actor)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if time.Since(start) > time.Second {
		t.Fatal("long component write occupied a startup/state lock")
	}
	after, _ := os.ReadFile(f.filename)
	if !bytes.Equal(before, after) {
		t.Fatal("busy component writer lost its intent")
	}
}

func TestNativeMalformedV2StateFailsClosed(t *testing.T) {
	f := newNativeStateFixture(t)
	if err := os.MkdirAll(filepath.Dir(f.filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.filename, []byte("{truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	f.command(t, "inspect", map[string]any{"runtimeRoot": f.runtimeRoot, "installationRoot": f.installation},
		"blocked", "update_record_read")
}

func TestNativeManualRepairAdoptsForwardTargetWithoutLosingOriginalFacts(t *testing.T) {
	f := newNativeStateFixture(t)
	f.installing(t)
	f.command(t, "repair", map[string]any{"transactionId": f.tx, "reason": "component_target_verification_failed"}, "repair", "")
	f.command(t, "adopt-repair", map[string]any{
		"transactionId": f.tx, "sourceReleaseId": "source", "failedTargetReleaseId": "target",
		"acceptanceDigest": strings.Repeat("e", 64), "handoffTerminated": true,
		"repairTarget": map[string]any{
			"releaseId": "repair", "version": "1.2.0",
			"packageSHA256": strings.Repeat("f", 64), "componentsSHA256": strings.Repeat("a", 64),
			"dataProfile": "format-v2-fixture",
		},
	}, "repair", "")
	var record map[string]any
	raw, err := os.ReadFile(f.filename)
	if err != nil || json.Unmarshal(raw, &record) != nil {
		t.Fatal("unreadable adopted repair")
	}
	if record["transactionId"] != f.tx || record["sourceReleaseId"] != "source" || record["targetReleaseId"] != "target" {
		t.Fatal("repair discarded original transaction/source/failed target")
	}
	target, _ := record["repairTarget"].(map[string]any)
	if target["releaseId"] != "repair" || record["phase"] != "repair" {
		t.Fatal("forward target was not explicitly bound while admission remained closed")
	}
	f.assertOriginalBindings(t)
	f.command(t, "cancel", map[string]any{"transactionId": f.tx}, "blocked", "native_handoff_unknown")
}
