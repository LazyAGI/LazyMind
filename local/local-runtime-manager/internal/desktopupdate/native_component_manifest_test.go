package desktopupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeComponentManifestFixture(t *testing.T) (*nativeStateFixture, ComponentDescriptor, map[string]any, string) {
	t.Helper()
	f := newNativeStateFixture(t)
	target := ComponentDescriptor{ID: "rag", Revision: strings.Repeat("b", 64), SHA256: strings.Repeat("c", 64),
		Tag: strings.Repeat("d", 64), BaseFingerprint: strings.Repeat("e", 64), PythonABI: "cp311",
		Platform: "darwin", Arch: "arm64", Packages: map[string]string{"fixture-package": "1"}}
	source := target
	source.Revision = strings.Repeat("a", 64)
	f.record["sourceHadRAG"], f.record["componentMode"] = true, "sync-target"
	f.record["sourceComponent"], f.record["targetComponent"] = source, target
	f.create(t)
	relative := filepath.Join("deps", "python-components", "rag", target.Revision)
	f.command(t, "prepared", map[string]any{"transactionId": f.tx, "stage": NativeComponentStage{
		Status: "prepared", StagePath: relative, Revision: target.Revision, SHA256: target.SHA256,
		TransactionID: f.tx, ComponentMode: "sync-target",
	}}, "prepared", "")
	f.command(t, "installing", map[string]any{"transactionId": f.tx, "dataProfile": "format-v2-fixture",
		"componentMode": "sync-target", "sourcePaused": true, "sourceActive": 0}, "installing", "")
	dir := filepath.Join(f.runtimeRoot, relative)
	if err := os.MkdirAll(filepath.Join(dir, "site-packages"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ready"), []byte(target.SHA256+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "catalog-"+target.Tag+".json"), nativeJSON(t, target), 0600); err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(nativeJSON(t, target), &manifest); err != nil {
		t.Fatal(err)
	}
	return f, target, manifest, dir
}

func nativeComponentActivation(f *nativeStateFixture, target ComponentDescriptor) map[string]any {
	return map[string]any{"transactionId": f.tx, "releaseId": "target", "dataProfile": "format-v2-fixture",
		"componentMode": "sync-target", "components": map[string]string{"rag": target.Revision}, "owner": "fixture-target-owner"}
}

func TestNativeComponentManifestOwnerMetadataCanComplete(t *testing.T) {
	for _, scenario := range []string{"legacy-semantic", "owner-schema-v1"} {
		t.Run(scenario, func(t *testing.T) {
			f, target, manifest, dir := nativeComponentManifestFixture(t)
			if scenario == "owner-schema-v1" {
				// build-python-components.py owns these fields; they are not part
				// of the protocol ComponentDescriptor or the canonical selection.
				manifest["schemaVersion"], manifest["signingMode"] = 1, "adhoc"
				delete(manifest, "sha256")
				delete(manifest, "tag")
			}
			if err := os.WriteFile(filepath.Join(dir, "bundle-manifest.json"), nativeJSON(t, manifest), 0600); err != nil {
				t.Fatal(err)
			}
			f.command(t, "activated", nativeComponentActivation(f, target), "activated", "")
			record, err := ReadNativeRecord(f.root)
			if err != nil {
				t.Fatal(err)
			}
			if admitted, err := NativeStartupPermission(context.Background(), f.root, record.Scope, "target", f.tx); err != nil || admitted == nil {
				t.Fatalf("real owner manifest blocked sealed startup: %v", err)
			}
			f.command(t, "complete", map[string]any{"transactionId": f.tx, "releaseId": "target",
				"dataProfile": "format-v2-fixture", "components": map[string]string{"rag": target.Revision},
				"owner": "fixture-target-owner", "peers": nativePeerProofs(f.tx)}, "completed", "")
		})
	}
}

func TestNativeComponentManifestRejectsUnknownOrDamagedProof(t *testing.T) {
	for _, scenario := range []string{"unknown-field", "unknown-version", "wrong-platform", "wrong-arch", "wrong-base", "wrong-packages", "bad-marker", "extra-selected-field"} {
		t.Run(scenario, func(t *testing.T) {
			f, target, manifest, dir := nativeComponentManifestFixture(t)
			switch scenario {
			case "unknown-field":
				manifest["schemaVersion"], manifest["unexpected"] = 1, true
			case "unknown-version":
				manifest["schemaVersion"] = 2
			case "wrong-platform":
				manifest["platform"] = "windows"
			case "wrong-arch":
				manifest["arch"] = "amd64"
			case "wrong-base":
				manifest["baseFingerprint"] = strings.Repeat("f", 64)
			case "wrong-packages":
				manifest["packages"] = map[string]string{"fixture-package": "2"}
			case "bad-marker":
				if err := os.WriteFile(filepath.Join(dir, ".ready"), []byte(strings.Repeat("f", 64)), 0600); err != nil {
					t.Fatal(err)
				}
			case "extra-selected-field":
				selected := map[string]any{}
				for k, v := range manifest {
					selected[k] = v
				}
				selected["schemaVersion"] = 1
				if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "catalog-"+target.Tag+".json"), nativeJSON(t, selected), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "bundle-manifest.json"), nativeJSON(t, manifest), 0600); err != nil {
				t.Fatal(err)
			}
			f.command(t, "activated", nativeComponentActivation(f, target), "blocked", "component_state_unknown")
			record, err := ReadNativeRecord(f.root)
			if err != nil || record.Phase != "installing" {
				t.Fatalf("failed component proof advanced transaction: %+v %v", record, err)
			}
		})
	}
}
