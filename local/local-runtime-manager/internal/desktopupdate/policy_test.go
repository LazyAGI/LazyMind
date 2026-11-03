package desktopupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var policyTime = time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)

type policyFixture struct {
	config  Config
	state   LocalState
	private ed25519.PrivateKey
	policy  map[string]any
	root    string
}

func newPolicyFixture(t *testing.T) *policyFixture {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &policyFixture{
		config:  Config{TrustDomain: "fixture-domain", Audience: "official", Channel: "stable", Mode: "official", ReleaseID: "r1", Version: "1.0.0", Platform: "darwin", Arch: "arm64", SigningIdentity: "FIXTURE_TEAM", UpdaterProtocol: 1, PolicyURL: "https://updates.example.invalid/stable.json", AllowedOrigins: []string{"https://updates.example.invalid"}, PublicKeys: map[string]string{"key1": base64.StdEncoding.EncodeToString(pub)}},
		state:   LocalState{SystemVersion: "14.0.0", DataProfile: "known-v1", Components: map[string]string{"rag": "python311-revision1"}},
		private: private, root: t.TempDir(),
		policy: map[string]any{"schemaVersion": 1, "minUpdaterProtocol": 1, "trustDomain": "fixture-domain", "audience": "official", "channel": "stable", "policySequence": 42, "issuedAt": policyTime.Add(-time.Hour).Format(time.RFC3339), "expiresAt": policyTime.Add(time.Hour).Format(time.RFC3339), "releases": []any{fixtureRelease("r1", "1.0.0"), fixtureRelease("r2", "1.1.0"), fixtureRelease("r3", "2.0.0")}, "edges": []any{fixtureEdge("r1", "r2"), fixtureEdge("r2", "r3")}, "revoked": []string{}},
	}
}

func fixtureArtifact(id string) map[string]any {
	digest := sha256.Sum256([]byte("fixture-artifact:" + id))
	return map[string]any{"url": "https://updates.example.invalid/releases/" + id + "/app.zip", "sha256": hex.EncodeToString(digest[:]), "size": 1024}
}

func fixtureRelease(id, version string) map[string]any {
	return map[string]any{"releaseId": id, "version": version, "platform": "darwin", "arch": "arm64", "minimumSystemVersion": "12.0.0", "minUpdaterProtocol": 1, "updaterProtocol": 1, "signingIdentity": "FIXTURE_TEAM", "package": fixtureArtifact(id), "feed": fixtureArtifact(id + "-feed"), "components": fixtureArtifact(id + "-components")}
}

func fixtureEdge(from, to string) map[string]any {
	return map[string]any{"sourceReleaseId": from, "targetReleaseId": to, "priority": 0, "migrationProfile": "same-v1", "sourceDataProfiles": []string{"known-v1"}, "targetDataProfile": "known-v1", "requiredComponents": map[string]string{"rag": "python311-revision1"}, "recoveryContract": map[string]any{"version": 1, "sourcePackageSHA256": fixtureArtifact(from)["sha256"], "sourceManifestSHA256": fixtureArtifact(from + "-manifest")["sha256"], "helperProtocolMin": 1, "helperProtocolMax": 1, "direction": "target-to-source"}}
}

func (f *policyFixture) envelope(t *testing.T) []byte {
	t.Helper()
	payload, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"keyId": "key1", "payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(f.private, payload))})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *policyFixture) check(t *testing.T) (Decision, error) {
	t.Helper()
	return Check(context.Background(), f.envelope(t), f.config, f.state, f.root, false, policyTime)
}

func TestSignedPolicySelectsDirectAndBridge(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "bridge", true: "direct"}[direct], func(t *testing.T) {
			f := newPolicyFixture(t)
			want := []string{"r2", "r3"}
			status := "bridge"
			if direct {
				f.policy["edges"] = append(f.policy["edges"].([]any), fixtureEdge("r1", "r3"))
				want = []string{"r3"}
				status = "direct"
			}
			d, err := f.check(t)
			if err != nil || d.Status != status || !reflect.DeepEqual(d.Route, want) || d.PolicySequence != 42 || len(d.PolicyDigest) != 64 {
				t.Fatalf("decision=%+v err=%v", d, err)
			}
		})
	}
}

func TestSignedPolicyRejectsUntrustedOrMalformedInputs(t *testing.T) {
	cases := map[string]func(*policyFixture){
		"wrong key":       func(f *policyFixture) { _, f.private, _ = ed25519.GenerateKey(rand.Reader) },
		"wrong audience":  func(f *policyFixture) { f.policy["audience"] = "personal-test" },
		"wrong domain":    func(f *policyFixture) { f.policy["trustDomain"] = "other-domain" },
		"wrong channel":   func(f *policyFixture) { f.policy["channel"] = "beta" },
		"unknown schema":  func(f *policyFixture) { f.policy["schemaVersion"] = 2 },
		"expired":         func(f *policyFixture) { f.policy["expiresAt"] = policyTime.Format(time.RFC3339) },
		"future":          func(f *policyFixture) { f.policy["issuedAt"] = policyTime.Add(time.Hour).Format(time.RFC3339) },
		"zero sequence":   func(f *policyFixture) { f.policy["policySequence"] = 0 },
		"invalid version": func(f *policyFixture) { f.policy["releases"].([]any)[1].(map[string]any)["version"] = "1.1.0-alpha.01" },
		"duplicate release": func(f *policyFixture) {
			f.policy["releases"] = append(f.policy["releases"].([]any), fixtureRelease("r2", "1.5.0"))
		},
		"untrusted URL": func(f *policyFixture) {
			f.policy["releases"].([]any)[1].(map[string]any)["package"].(map[string]any)["url"] = "https://attacker.example.invalid/app.zip"
		},
		"production HTTP": func(f *policyFixture) {
			f.config.AllowedOrigins = []string{"http://127.0.0.1:8000"}
			f.config.PolicyURL = "http://127.0.0.1:8000/policy.json"
		},
		"source version lie": func(f *policyFixture) { f.policy["releases"].([]any)[0].(map[string]any)["version"] = "0.9.0" },
		"bad hash": func(f *policyFixture) {
			f.policy["releases"].([]any)[1].(map[string]any)["package"].(map[string]any)["sha256"] = "bad"
		},
		"negative size": func(f *policyFixture) {
			f.policy["releases"].([]any)[1].(map[string]any)["package"].(map[string]any)["size"] = -1
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPolicyFixture(t)
			change(f)
			if _, err := f.check(t); err == nil {
				t.Fatal("invalid policy accepted")
			}
			entries, _ := os.ReadDir(f.root)
			if len(entries) > 0 {
				t.Fatal("invalid input created trust state")
			}
		})
	}
}

func TestPolicyDecisionsFailClosedOnLocalIncompatibility(t *testing.T) {
	cases := []struct {
		name, status, reason string
		change               func(*policyFixture)
	}{
		{"dirty", "blocked", "data_dirty", func(f *policyFixture) { f.state.Dirty = true }},
		{"unknown data", "blocked", "data_unknown", func(f *policyFixture) { f.state.DataProfile = "" }},
		{"schema mismatch", "manual_required", "no_compatible_path", func(f *policyFixture) { f.state.DataProfile = "unknown-v99" }},
		{"component mismatch", "blocked", "component_mismatch", func(f *policyFixture) { f.state.Components["rag"] = "other-abi" }},
		{"old OS", "unsupported_system", "system_version", func(f *policyFixture) { f.state.SystemVersion = "11.0.0" }},
		{"missing bridge", "manual_required", "no_compatible_path", func(f *policyFixture) { f.policy["edges"] = []any{fixtureEdge("r2", "r3")} }},
		{"protocol", "manual_required", "updater_protocol", func(f *policyFixture) { f.policy["minUpdaterProtocol"] = 2 }},
		{"source revoked", "blocked", "source_revoked", func(f *policyFixture) { f.policy["revoked"] = []string{"r1"} }},
		{"same version different release", "blocked", "version_conflict", func(f *policyFixture) {
			f.policy["releases"].([]any)[1].(map[string]any)["version"] = "1.0.0+repack"
			f.policy["edges"] = []any{}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPolicyFixture(t)
			tc.change(f)
			d, err := f.check(t)
			if err != nil || d.Status != tc.status || d.Reason != tc.reason {
				t.Fatalf("decision=%+v err=%v; want %s/%s", d, err, tc.status, tc.reason)
			}
		})
	}
}

func TestPolicyDoesNotOfferDowngradesOrPrereleasesToStable(t *testing.T) {
	for _, version := range []string{"0.9.0", "1.0.0+build", "1.1.0-alpha.1", "1.1.0-beta.1", "1.1.0-rc.1"} {
		t.Run(version, func(t *testing.T) {
			f := newPolicyFixture(t)
			if strings.HasPrefix(version, "1.0.0+") {
				f.config.Version = version
				f.policy["releases"] = []any{fixtureRelease("r1", version)}
			} else {
				f.policy["releases"] = []any{fixtureRelease("r1", "1.0.0"), fixtureRelease("r2", version)}
			}
			f.policy["edges"] = []any{}
			d, err := f.check(t)
			if err != nil || d.Status != "up_to_date" || len(d.Route) != 0 {
				t.Fatalf("decision=%+v err=%v", d, err)
			}
		})
	}
}

func TestTrustLedgerRejectsReplayAndEquivocationAcrossRestarts(t *testing.T) {
	f := newPolicyFixture(t)
	if _, err := f.check(t); err != nil {
		t.Fatal(err)
	}
	if _, err := f.check(t); err != nil {
		t.Fatalf("identical policy must be idempotent: %v", err)
	}
	f.policy["policySequence"] = 43
	if _, err := f.check(t); err != nil {
		t.Fatal(err)
	}
	f.policy["policySequence"] = 42
	if _, err := f.check(t); err == nil {
		t.Fatal("accepted replay after newer policy")
	}
	f.policy["policySequence"] = 43
	f.policy["expiresAt"] = policyTime.Add(2 * time.Hour).Format(time.RFC3339)
	if _, err := f.check(t); err == nil {
		t.Fatal("accepted different bytes at the same sequence")
	}
	// Business state may be restored; the independent trust root stays intact.
	f.state.DataProfile = "known-v1"
	f.policy["policySequence"] = 42
	if _, err := f.check(t); err == nil {
		t.Fatal("business recovery reset the trust high-water mark")
	}
}

func TestTrustLedgerCorruptionAndMissingExistingStateFailClosed(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrupt", true: "missing"}[missing], func(t *testing.T) {
			f := newPolicyFixture(t)
			if _, err := f.check(t); err != nil {
				t.Fatal(err)
			}
			var ledger string
			if err := filepath.WalkDir(f.root, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.Name() == "ledger.json" {
					ledger = p
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if ledger == "" {
				t.Fatal("no durable ledger")
			}
			if missing {
				if err := os.Remove(ledger); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(ledger, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Check(context.Background(), f.envelope(t), f.config, f.state, f.root, true, policyTime); err == nil {
				t.Fatal("corrupt or missing existing trust was reset")
			}
		})
	}
}

func TestConcurrentPolicyAcceptanceNeverRegresses(t *testing.T) {
	f := newPolicyFixture(t)
	if _, err := f.check(t); err != nil {
		t.Fatal(err)
	}
	var payloads [][]byte
	for seq := 43; seq <= 50; seq++ {
		f.policy["policySequence"] = seq
		payloads = append(payloads, f.envelope(t))
	}
	var wg sync.WaitGroup
	highestResult := make(chan error, 1)
	for index, raw := range payloads {
		wg.Add(1)
		go func(index int, raw []byte) {
			defer wg.Done()
			_, err := Check(context.Background(), raw, f.config, f.state, f.root, false, policyTime)
			if index == len(payloads)-1 {
				highestResult <- err
			}
		}(index, raw)
	}
	wg.Wait()
	if err := <-highestResult; err != nil {
		t.Fatalf("highest concurrent policy must succeed: %v", err)
	}
	f.policy["policySequence"] = 49
	if _, err := f.check(t); err == nil {
		t.Fatal("concurrent acceptance lost the high-water mark")
	}
	f.policy["policySequence"] = 50
	if _, err := f.check(t); err != nil {
		t.Fatalf("highest policy not durable/idempotent: %v", err)
	}
}

func TestOnlyUsableEdgesCanBeSelected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*policyFixture)
	}{
		{"revoked bridge", func(f *policyFixture) { f.policy["revoked"] = []string{"r2"} }},
		{"wrong architecture", func(f *policyFixture) { f.policy["releases"].([]any)[1].(map[string]any)["arch"] = "amd64" }},
		{"changed signer", func(f *policyFixture) {
			f.policy["releases"].([]any)[1].(map[string]any)["signingIdentity"] = "OTHER_TEAM"
		}},
		{"missing source data profile", func(f *policyFixture) {
			f.policy["edges"].([]any)[0].(map[string]any)["sourceDataProfiles"] = []string{"other-data"}
		}},
		{"unsupported recovery protocol", func(f *policyFixture) {
			f.policy["edges"].([]any)[0].(map[string]any)["recoveryContract"].(map[string]any)["helperProtocolMin"] = 2
			f.policy["edges"].([]any)[0].(map[string]any)["recoveryContract"].(map[string]any)["helperProtocolMax"] = 2
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPolicyFixture(t)
			tc.change(f)
			d, err := f.check(t)
			if err == nil && (d.Status == "direct" || d.Status == "bridge" || len(d.Route) != 0) {
				t.Fatalf("unusable path selected: %+v", d)
			}
		})
	}
}

func TestMalformedEdgesAreRejectedBeforeAcceptingTrust(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"unknown target":  func(e map[string]any) { e["targetReleaseId"] = "not-present" },
		"downgrade cycle": func(e map[string]any) { e["sourceReleaseId"] = "r3"; e["targetReleaseId"] = "r1" },
		"no recovery":     func(e map[string]any) { delete(e, "recoveryContract") },
		"wrong source artifact": func(e map[string]any) {
			e["recoveryContract"].(map[string]any)["sourcePackageSHA256"] = strings.Repeat("c", 64)
		},
		"target artifact used for recovery": func(e map[string]any) {
			e["recoveryContract"].(map[string]any)["sourcePackageSHA256"] = fixtureArtifact("r2")["sha256"]
		},
		"same-schema changes data profile":         func(e map[string]any) { e["targetDataProfile"] = "different-v2" },
		"same-schema admits mixed source profiles": func(e map[string]any) { e["sourceDataProfiles"] = []string{"known-v1", "different-v2"} },
		"unknown migration":                        func(e map[string]any) { e["migrationProfile"] = "" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newPolicyFixture(t)
			change(f.policy["edges"].([]any)[0].(map[string]any))
			if _, err := f.check(t); err == nil {
				t.Fatal("malformed edge accepted")
			}
			entries, _ := os.ReadDir(f.root)
			if len(entries) > 0 {
				t.Fatal("malformed edge advanced trust")
			}
		})
	}
}

func TestBridgeTieUsesPublisherPriorityAndRechecksDataAtEachHop(t *testing.T) {
	f := newPolicyFixture(t)
	f.policy["releases"] = append(f.policy["releases"].([]any), fixtureRelease("r2b", "1.2.0"))
	preferred := fixtureEdge("r1", "r2b")
	preferred["priority"] = -1
	f.policy["edges"] = append(f.policy["edges"].([]any), preferred, fixtureEdge("r2b", "r3"))
	d, err := f.check(t)
	if err != nil || !reflect.DeepEqual(d.Route, []string{"r2b", "r3"}) {
		t.Fatalf("priority decision=%+v err=%v", d, err)
	}
	// A new installed bridge instance must measure state again; a remembered
	// route does not authorize its second hop after data becomes dirty.
	f.config.ReleaseID = "r2b"
	f.config.Version = "1.2.0"
	f.state.Dirty = true
	d, err = f.check(t)
	if err != nil || d.Status != "blocked" || d.Reason != "data_dirty" || len(d.Route) != 0 {
		t.Fatalf("dirty second hop=%+v err=%v", d, err)
	}
}

func TestNormalHelperRejectsLocalTestMode(t *testing.T) {
	f := newPolicyFixture(t)
	t.Setenv("LAZYMIND_DESKTOP_ALLOW_UNSIGNED_UPDATE_TEST", "true")
	f.config.Mode = "unsigned-test"
	if _, err := f.check(t); err == nil {
		t.Fatal("normal helper accepted local test mode")
	}
}

func TestInvalidPolicyCannotAdvanceExistingLedger(t *testing.T) {
	f := newPolicyFixture(t)
	raw42 := f.envelope(t)
	if _, err := f.check(t); err != nil {
		t.Fatal(err)
	}
	f.policy["policySequence"] = 999
	f.policy["expiresAt"] = policyTime.Add(-time.Minute).Format(time.RFC3339)
	if _, err := f.check(t); err == nil {
		t.Fatal("expired policy accepted")
	}
	if _, err := Check(context.Background(), raw42, f.config, f.state, f.root, true, policyTime); err != nil {
		t.Fatalf("rejected input poisoned valid trust: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Check(ctx, raw42, f.config, f.state, f.root, true, policyTime); err == nil {
		t.Fatal("cancelled operation continued")
	}
}

func TestUninitializedRecoveryAndCorruptEnvelopeAreRejected(t *testing.T) {
	f := newPolicyFixture(t)
	if _, err := Check(context.Background(), f.envelope(t), f.config, f.state, f.root, true, policyTime); err == nil {
		t.Fatal("existing transaction silently bootstrapped missing trust")
	}
	for _, raw := range [][]byte{[]byte("{"), []byte(`{"keyId":"unknown","payload":"aA==","signature":"aA=="}`), []byte(strings.Repeat("x", 2*1024*1024))} {
		if _, err := Check(context.Background(), raw, f.config, f.state, f.root, false, policyTime); err == nil {
			t.Fatal("corrupt or oversized envelope accepted")
		}
	}
	var envelope map[string]string
	if err := json.Unmarshal(f.envelope(t), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["payload"] = base64.StdEncoding.EncodeToString([]byte(`{"policySequence":999}`))
	tampered, _ := json.Marshal(envelope)
	if _, err := Check(context.Background(), tampered, f.config, f.state, f.root, false, policyTime); err == nil {
		t.Fatal("payload tampering accepted")
	}
}

func TestUnknownNonemptyMigrationContractIsNotSelected(t *testing.T) {
	f := newPolicyFixture(t)
	f.policy["edges"].([]any)[0].(map[string]any)["migrationProfile"] = "future-transform-v9"
	d, err := f.check(t)
	if err != nil || d.Status != "manual_required" || d.Reason != "migration_profile" || len(d.Route) != 0 {
		t.Fatalf("unknown migration interpreted: %+v err=%v", d, err)
	}
}

func TestSignaturesAndSequenceIdentityUseOriginalPayloadBytes(t *testing.T) {
	f := newPolicyFixture(t)
	compact, _ := json.Marshal(f.policy)
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact, "", "  "); err != nil {
		t.Fatal(err)
	}
	rawPayload := append([]byte(" \n"), indented.Bytes()...)
	wrap := func(payload, signature []byte) []byte {
		raw, _ := json.Marshal(map[string]string{"keyId": "key1", "payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(signature)})
		return raw
	}
	signature := ed25519.Sign(f.private, rawPayload)
	d, err := Check(context.Background(), wrap(rawPayload, signature), f.config, f.state, f.root, false, policyTime)
	wantDigest := sha256.Sum256(rawPayload)
	if err != nil || d.Status != "bridge" || d.PolicyDigest != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("original bytes not accepted/identified: %+v err=%v", d, err)
	}
	changed := append(append([]byte{}, rawPayload...), '\n')
	if _, err := Check(context.Background(), wrap(changed, signature), f.config, f.state, f.root, false, policyTime); err == nil {
		t.Fatal("changed raw bytes accepted with old signature")
	}
	if _, err := Check(context.Background(), wrap(changed, ed25519.Sign(f.private, changed)), f.config, f.state, f.root, false, policyTime); err == nil {
		t.Fatal("semantically equal but different bytes accepted at same sequence")
	}
}

func TestReachablePrereleaseIsFilteredByChannel(t *testing.T) {
	for _, channel := range []string{"stable", "beta"} {
		t.Run(channel, func(t *testing.T) {
			f := newPolicyFixture(t)
			f.config.Channel = channel
			f.policy["channel"] = channel
			f.policy["releases"] = []any{fixtureRelease("r1", "1.0.0"), fixtureRelease("r2", "1.1.0-beta.1")}
			f.policy["edges"] = []any{fixtureEdge("r1", "r2")}
			d, err := f.check(t)
			if err != nil {
				t.Fatal(err)
			}
			if channel == "stable" {
				if d.Status != "up_to_date" || len(d.Route) != 0 {
					t.Fatalf("stable offered prerelease: %+v", d)
				}
			} else if d.Status != "direct" || !reflect.DeepEqual(d.Route, []string{"r2"}) {
				t.Fatalf("beta rejected compatible prerelease: %+v", d)
			}
		})
	}
	f := newPolicyFixture(t)
	f.config.Channel = "canary"
	f.policy["channel"] = "canary"
	f.config.Version = "1.1.0-alpha.9"
	f.policy["releases"] = []any{fixtureRelease("r1", "1.1.0-alpha.9"), fixtureRelease("r2", "1.1.0-alpha.10")}
	f.policy["edges"] = []any{fixtureEdge("r1", "r2")}
	if d, err := f.check(t); err != nil || d.Status != "direct" {
		t.Fatalf("numeric prerelease precedence: %+v err=%v", d, err)
	}
}

func TestMixedPolicyStillSelectsHighestCompatibleReachableTarget(t *testing.T) {
	for _, kind := range []string{"architecture", "platform", "system", "identity", "protocol"} {
		t.Run(kind, func(t *testing.T) {
			f := newPolicyFixture(t)
			higher := fixtureRelease("r4", "3.0.0")
			source := fixtureRelease("other-source", "1.0.0")
			switch kind {
			case "architecture":
				higher["arch"] = "amd64"
				source["arch"] = "amd64"
			case "platform":
				higher["platform"] = "windows"
				source["platform"] = "windows"
				higher["arch"] = "amd64"
				source["arch"] = "amd64"
			case "system":
				higher["minimumSystemVersion"] = "15.0.0"
			case "identity":
				higher["signingIdentity"] = "OTHER_TEAM"
			case "protocol":
				higher["minUpdaterProtocol"] = 2
				higher["updaterProtocol"] = 2
			}
			f.policy["releases"] = append(f.policy["releases"].([]any), higher)
			from := "r1"
			if kind == "architecture" || kind == "platform" {
				f.policy["releases"] = append(f.policy["releases"].([]any), source)
				from = "other-source"
			}
			f.policy["edges"] = append(f.policy["edges"].([]any), fixtureEdge(from, "r4"))
			d, err := f.check(t)
			if err != nil || d.Status != "bridge" || !reflect.DeepEqual(d.Route, []string{"r2", "r3"}) {
				t.Fatalf("mixed %s policy lost compatible target: %+v err=%v", kind, d, err)
			}
		})
	}
}
