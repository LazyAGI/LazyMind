package desktopupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func newV2PolicyFixture(t *testing.T, ragInstalled bool) *policyFixture {
	t.Helper()
	f := newPolicyFixture(t)
	f.config.UpdaterProtocol = 2
	f.config.PolicyURL = "https://updates.example.invalid/v2/stable.json"
	f.policy["schemaVersion"], f.policy["minUpdaterProtocol"] = 2, 2
	f.policy["releases"] = []any{fixtureRelease("r1", "1.0.0"), fixtureRelease("r2", "1.1.0"), fixtureRelease("r3", "1.2.0")}
	for _, value := range f.policy["releases"].([]any) {
		r := value.(map[string]any)
		r["updaterProtocol"], r["minUpdaterProtocol"] = 2, 2
	}
	f.policy["edges"] = []any{map[string]any{
		"sourceReleaseId": "r1", "targetReleaseId": "r2", "priority": 0,
		"migrationProfile": "format-v2", "sourceDataProfiles": []string{"format-v2-fixture"},
		"targetDataProfile": "format-v2-fixture",
		"componentTransitions": map[string]any{"rag": map[string]any{
			"sourceClasses": []any{map[string]any{
				"tag": strings.Repeat("a", 64), "pythonAbi": "cp311",
				"baseFingerprint": strings.Repeat("b", 64), "packages": map[string]string{"fixture-package": "1"},
			}},
			"targetComponentsSHA256": fixtureArtifact("r2-components")["sha256"],
		}},
	}}
	f.policy["repairAcceptances"] = []any{}
	setV2ComponentState(t, f, ragInstalled)
	return f
}

func setV2ComponentState(t *testing.T, f *policyFixture, installed bool) {
	t.Helper()
	state := map[string]any{
		"systemVersion": "14.0.0", "dataProfile": "format-v2-fixture", "dirty": false,
		"components": map[string]string{}, "componentMetadata": map[string]any{},
	}
	if installed {
		state["components"] = map[string]string{"rag": strings.Repeat("c", 64)}
		state["componentMetadata"] = map[string]any{"rag": map[string]any{
			"id": "rag", "revision": strings.Repeat("c", 64), "sha256": strings.Repeat("d", 64),
			"tag": strings.Repeat("a", 64), "pythonAbi": "cp311",
			"platform": "darwin", "arch": "arm64",
			"baseFingerprint": strings.Repeat("b", 64), "packages": map[string]string{"fixture-package": "1"},
		}}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var next LocalState
	if err := json.Unmarshal(raw, &next); err != nil {
		t.Fatal(err)
	}
	f.state = next
}

func changeV2Metadata(t *testing.T, f *policyFixture, change func(map[string]any)) {
	t.Helper()
	var state map[string]any
	raw, err := json.Marshal(f.state)
	if err != nil || json.Unmarshal(raw, &state) != nil {
		t.Fatal(err)
	}
	metadata, _ := state["componentMetadata"].(map[string]any)
	// The old DTO has no metadata field yet. Build the requested new wire state
	// independently so baseline failures represent missing v2, not a panic.
	if metadata == nil {
		metadata = map[string]any{"rag": map[string]any{
			"id": "rag", "revision": strings.Repeat("c", 64), "sha256": strings.Repeat("d", 64),
			"tag": strings.Repeat("a", 64), "pythonAbi": "cp311", "platform": "darwin", "arch": "arm64",
			"baseFingerprint": strings.Repeat("b", 64), "packages": map[string]string{"fixture-package": "1"},
		}}
		state["componentMetadata"] = metadata
	}
	change(state)
	raw, err = json.Marshal(state)
	var next LocalState
	if err != nil || json.Unmarshal(raw, &next) != nil {
		t.Fatal(err)
	}
	f.state = next
}

func TestV2OneSignedPolicyCoversLightweightAndInstalledRAG(t *testing.T) {
	f := newV2PolicyFixture(t, false)
	envelope := f.envelope(t)
	for _, installed := range []bool{false, true} {
		setV2ComponentState(t, f, installed)
		d, err := Check(t.Context(), envelope, f.config, f.state, t.TempDir(), false, policyTime)
		if err != nil || d.Status != "direct" || d.Next == nil || d.Next.ID != "r2" {
			t.Fatalf("one policy must cover installed=%v: decision=%+v err=%v", installed, d, err)
		}
		if d.Next.Components.SHA256 != fixtureArtifact("r2-components")["sha256"] {
			t.Fatal("decision changed the fixed target component artifact")
		}
	}
}

func TestV2ComponentMetadataMustProveSourceFormatClass(t *testing.T) {
	for _, scenario := range []string{"missing", "unknown-tag", "wrong-abi", "wrong-base", "wrong-packages", "revision-disagrees"} {
		t.Run(scenario, func(t *testing.T) {
			f := newV2PolicyFixture(t, true)
			changeV2Metadata(t, f, func(state map[string]any) {
				if scenario == "missing" {
					state["componentMetadata"] = map[string]any{}
					return
				}
				rag := state["componentMetadata"].(map[string]any)["rag"].(map[string]any)
				switch scenario {
				case "unknown-tag":
					rag["tag"] = strings.Repeat("e", 64)
				case "wrong-abi":
					rag["pythonAbi"] = "cp312"
				case "wrong-base":
					rag["baseFingerprint"] = strings.Repeat("e", 64)
				case "wrong-packages":
					rag["packages"] = map[string]string{"fixture-package": "2"}
				case "revision-disagrees":
					rag["revision"] = strings.Repeat("e", 64)
				}
			})
			d, err := f.check(t)
			if err != nil {
				t.Fatalf("source incompatibility needs a bounded decision: %v", err)
			}
			if d.Status != "blocked" || (d.Reason != "component_state_unknown" && d.Reason != "component_update_incompatible") {
				t.Fatalf("unproved source component class authorized: %+v", d)
			}
		})
	}
}

func TestV2RejectsInvalidFormatAndTargetBindingBeforeTrustWrite(t *testing.T) {
	for _, scenario := range []string{"different-data-format", "unbound-target-components", "empty-source-class", "unexpected-field", "bad-signature"} {
		t.Run(scenario, func(t *testing.T) {
			f := newV2PolicyFixture(t, true)
			edge := f.policy["edges"].([]any)[0].(map[string]any)
			rag := edge["componentTransitions"].(map[string]any)["rag"].(map[string]any)
			switch scenario {
			case "different-data-format":
				edge["targetDataProfile"] = "different-format"
			case "unbound-target-components":
				rag["targetComponentsSHA256"] = strings.Repeat("e", 64)
			case "empty-source-class":
				rag["sourceClasses"] = []any{}
			case "unexpected-field":
				edge["untrustedMigrationScript"] = "never execute"
			case "bad-signature":
				_, f.private, _ = ed25519.GenerateKey(rand.Reader)
			}
			if _, err := f.check(t); err == nil {
				t.Fatal("invalid v2 publication accepted")
			}
			if entries, err := os.ReadDir(f.root); err != nil || len(entries) != 0 {
				t.Fatalf("invalid publication wrote trust state: entries=%d err=%v", len(entries), err)
			}
		})
	}
}

func TestV2DirtyUnknownAndRevokedSourcesRemainBlocked(t *testing.T) {
	for _, scenario := range []string{"dirty", "unknown", "revoked"} {
		t.Run(scenario, func(t *testing.T) {
			f := newV2PolicyFixture(t, true)
			switch scenario {
			case "dirty":
				f.state.Dirty = true
			case "unknown":
				f.state.DataProfile = ""
			case "revoked":
				f.policy["revoked"] = []string{"r1"}
			}
			d, err := f.check(t)
			if err != nil || d.Status != "blocked" {
				t.Fatalf("invalid source did not remain blocked: %+v %v", d, err)
			}
		})
	}
}

func TestV1ClientMustNotConsumeV2Policy(t *testing.T) {
	f := newV2PolicyFixture(t, false)
	f.config.UpdaterProtocol = 1
	if _, err := f.check(t); err == nil {
		t.Fatal("old strict client consumed new policy semantics")
	}
}
