package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	update "github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func publisherFixture(t *testing.T) (map[string]record, plan, ed25519.PrivateKey) {
	t.Helper()
	public, key, _ := ed25519.GenerateKey(rand.Reader)
	records := map[string]record{}
	hash := strings.Repeat("a", 64)
	for i, id := range []string{"r1", "r2"} {
		version := []string{"1.0.0", "1.1.0"}[i]
		c := update.Config{TrustDomain: "fixture", Audience: "official", Channel: "stable", Mode: "official", ReleaseID: id, Version: version, Platform: "darwin", Arch: "arm64", SigningIdentity: "TEAM", UpdaterProtocol: 1, PolicyURL: "https://fixture.invalid/updates/stable.json", AllowedOrigins: []string{"https://fixture.invalid"}, PublicKeys: map[string]string{"fixture": base64.StdEncoding.EncodeToString(public)}}
		artifact := update.Artifact{URL: "https://fixture.invalid/releases/" + id + "/package.zip", SHA256: hash, Size: 10}
		r := update.Release{ID: id, Version: version, Platform: "darwin", Arch: "arm64", SigningIdentity: "TEAM", UpdaterProtocol: 1, MinUpdaterProtocol: 1, MinimumSystemVersion: "12.0.0", Package: artifact, Feed: artifact, Components: artifact}
		records[id] = record{Config: c, Release: r, ManifestSHA256: hash}
	}
	now := time.Now().UTC()
	p := plan{Sequence: 1, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), ReleaseIDs: []string{"r1", "r2"}, Edges: []edgePlan{{Source: "r1", Target: "r2", DataProfile: "known-v1"}}}
	return records, p, key
}
func TestAssembleUsesMeasuredReleaseAndExplicitProfile(t *testing.T) {
	records, p, _ := publisherFixture(t)
	policy, c, err := assemble(p, records)
	if err != nil {
		t.Fatal(err)
	}
	edge := policy.Edges[0]
	if edge.Recovery.SourceManifestSHA256 != records["r1"].ManifestSHA256 || edge.Recovery.SourcePackageSHA256 != records["r1"].Release.Package.SHA256 || edge.SourceDataProfiles[0] != "known-v1" || c.ReleaseID != "r1" {
		t.Fatal("incorrect source contract")
	}
	p.Edges[0].DataProfile = ""
	if _, _, err = assemble(p, records); err == nil {
		t.Fatal("guessed compatibility")
	}
}
func TestAssembleRejectsMixedTrustAndIdentity(t *testing.T) {
	for _, kind := range []string{"identity", "channel", "source", "duplicate", "missing"} {
		t.Run(kind, func(t *testing.T) {
			records, p, _ := publisherFixture(t)
			r := records["r2"]
			switch kind {
			case "identity":
				r.Release.Version = "9.0.0"
			case "channel":
				r.Config.Channel = "beta"
			case "source":
				r.Config.PolicyURL = "https://fixture.invalid/other.json"
			case "duplicate":
				p.ReleaseIDs = append(p.ReleaseIDs, "r1")
			case "missing":
				p.ReleaseIDs = append(p.ReleaseIDs, "missing")
			}
			records["r2"] = r
			if _, _, err := assemble(p, records); err == nil {
				t.Fatal("accepted invalid record plan")
			}
		})
	}
}

func TestV2SignsInitialBaselineAndBlocksAnEdgeWithoutNativeEvidence(t *testing.T) {
	records, nextPlan, key := publisherFixture(t)
	hash := strings.Repeat("a", 64)
	profile := "format-v2-" + hash
	for id, release := range records {
		release.Config.UpdaterProtocol = 2
		release.Release.UpdaterProtocol = 2
		release.Release.MinUpdaterProtocol = 2
		release.Release.Package.UnpackedBytes = 100
		release.FormatProfile = profile
		release.ComponentDescriptor = &update.ComponentDescriptor{ID: "rag", Tag: hash, BaseFingerprint: hash,
			PythonABI: "cp311", Packages: map[string]string{"milvus-lite": "3.0"}}
		records[id] = release
	}

	initial := nextPlan
	initial.ReleaseIDs = []string{"r1"}
	initial.Edges = nil
	policy, cfg, err := assemble(initial, records)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	envelope, err := update.SignPublication(policy, cfg, "fixture", key, now)
	if err != nil {
		t.Fatalf("failed to sign the initial v2 baseline: %v", err)
	}
	decision, err := update.Check(t.Context(), envelope, cfg, update.LocalState{SystemVersion: "14.0.0", DataProfile: profile}, t.TempDir(), false, now)
	if err != nil || decision.Status != "up_to_date" {
		t.Fatalf("first v2 publication should register A without offering an upgrade: %+v, %v", decision, err)
	}

	nextPlan.Edges[0].DataProfile = profile
	if _, _, err := assemble(nextPlan, records); err == nil || !strings.Contains(err.Error(), "native same-index format evidence is required") {
		t.Fatalf("an A-to-B edge must require native evidence, got %v", err)
	}
}

func TestSignCommandExtendsVerifiedV2Publication(t *testing.T) {
	records, next, key := publisherFixture(t)
	hash := strings.Repeat("a", 64)
	profile := "format-v2-" + hash
	class := update.ComponentSourceClass{Tag: hash, PythonABI: "cp311", BaseFingerprint: hash,
		Packages: map[string]string{"milvus-lite": "3.0"}}
	for id, r := range records {
		r.Config.UpdaterProtocol = 2
		r.Release.UpdaterProtocol, r.Release.MinUpdaterProtocol = 2, 2
		r.Release.Package.UnpackedBytes = 100
		prefix := strings.TrimSuffix(r.Release.Package.URL, "package.zip")
		r.Release.Feed.URL, r.Release.Components.URL = prefix+"feed.json", prefix+"components.json"
		r.FormatProfile = profile
		r.ComponentDescriptor = &update.ComponentDescriptor{ID: "rag", Tag: class.Tag, PythonABI: class.PythonABI,
			BaseFingerprint: class.BaseFingerprint, Packages: class.Packages}
		records[id] = r
	}
	transitions := func(target update.Release) map[string]update.ComponentTransition {
		return map[string]update.ComponentTransition{"rag": {TargetComponentsSHA256: target.Components.SHA256,
			SourceClasses: []update.ComponentSourceClass{class}}}
	}
	a, b := records["r1"], records["r2"]
	previousRelease := func(id, version string) update.Release {
		release := a.Release
		release.ID, release.Version = id, version
		for _, artifact := range []*update.Artifact{&release.Package, &release.Feed, &release.Components} {
			artifact.URL = strings.Replace(artifact.URL, "/r1/", "/"+id+"/", 1)
		}
		return release
	}
	prior := previousRelease("r0", "0.9.0")
	failed := previousRelease("failed", "0.9.5")
	priorEdge := update.Edge{SourceID: prior.ID, TargetID: a.Release.ID, MigrationProfile: "format-v2",
		SourceDataProfiles: []string{profile}, TargetDataProfile: profile, ComponentTransitions: transitions(a.Release)}
	repair := update.RepairAcceptance{SourceID: prior.ID, FailedTargetID: failed.ID, RepairID: a.Release.ID,
		SourceDataProfile: profile, TargetDataProfile: profile, ComponentTransitions: transitions(a.Release),
		TargetComponentsSHA256: a.Release.Components.SHA256}
	past := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	previous := update.Policy{SchemaVersion: 2, MinUpdaterProtocol: 2, Sequence: 1, TrustDomain: a.Config.TrustDomain,
		Audience: a.Config.Audience, Channel: a.Config.Channel, IssuedAt: past.Format(time.RFC3339),
		ExpiresAt: past.Add(time.Hour).Format(time.RFC3339), Releases: []update.Release{prior, failed, a.Release}, Edges: []update.Edge{priorEdge},
		Revoked: []string{failed.ID}, RepairAcceptances: []update.RepairAcceptance{repair}}
	oldEnvelope, err := update.SignPublication(previous, a.Config, "fixture", key, past)
	if err != nil {
		t.Fatalf("sign historical fixture: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	next.Sequence = previous.Sequence + 1
	for _, scenario := range []string{"valid history", "modified source record", "invalid historical signature", "evidence for a different target catalog"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			write := func(name string, raw []byte) string {
				t.Helper()
				file := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, raw, 0600); err != nil {
					t.Fatal(err)
				}
				return file
			}
			encode := func(value any) []byte {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			source, history := a, oldEnvelope
			if scenario == "modified source record" {
				source.Release.Package.SHA256 = strings.Repeat("b", 64)
			}
			if scenario == "invalid historical signature" {
				var envelope map[string]string
				if err := json.Unmarshal(history, &envelope); err != nil {
					t.Fatal(err)
				}
				envelope["signature"] = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
				history = encode(envelope)
			}
			write("records/r1/release.json", encode(source))
			write("records/r2/release.json", encode(b))
			catalogHash := b.Release.Components.SHA256
			if scenario == "evidence for a different target catalog" {
				catalogHash = strings.Repeat("c", 64)
			}
			// Synthetic evidence verifies the signer contract only. The native runner generates real evidence.
			evidence := write("a-to-b.json", encode(map[string]any{"sourceReleaseId": a.Release.ID, "targetReleaseId": b.Release.ID,
				"dataProfile": profile, "targetComponentsSHA256": catalogHash, "fixtureSHA256": hash,
				"sourceClasses": []update.ComponentSourceClass{class}, "readOnly": true, "passed": true}))
			p := next
			p.Edges = []edgePlan{{Source: a.Release.ID, Target: b.Release.ID, DataProfile: profile,
				ComponentTransitions: transitions(b.Release), FormatEvidence: evidence}}
			output := filepath.Join(root, "policy.json")
			err := run([]string{"sign", "--records", filepath.Join(root, "records"), "--plan", write("plan.json", encode(p)),
				"--previous-envelope", write("previous.json", history), "--key-id", "fixture",
				"--key-file", write("key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
				"--output", output, "--config-output", filepath.Join(root, "config.json")})
			if scenario != "valid history" {
				want := "source release record does not match published policy"
				if scenario == "invalid historical signature" {
					want = "policy_signature"
				}
				if scenario == "evidence for a different target catalog" {
					want = "native same-index format evidence is required"
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("%s should be rejected by its verification check, got %v", scenario, err)
				}
				if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
					t.Fatalf("rejected extension created a policy: %v", statErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("extend signed v2 history: %v", err)
			}
			raw, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := update.ValidatePublication(raw, a.Config, time.Now().UTC())
			if err != nil || policy.Sequence != previous.Sequence+1 || len(policy.Releases) != 4 || len(policy.Edges) != 2 {
				t.Fatalf("expected all historical and new entries in signed policy: %+v, %v", policy, err)
			}
			if !reflect.DeepEqual(policy.Revoked, previous.Revoked) || !reflect.DeepEqual(policy.RepairAcceptances, previous.RepairAcceptances) {
				t.Fatalf("published revocations or repair acceptances changed: %+v", policy)
			}
			for _, edge := range policy.Edges {
				if edge.SourceID == prior.ID && !reflect.DeepEqual(edge, priorEdge) {
					t.Fatalf("published historical edge changed: %+v", edge)
				}
			}
			priorConfig := a.Config
			priorConfig.ReleaseID, priorConfig.Version = prior.ID, prior.Version
			decision, err := update.Check(t.Context(), raw, priorConfig, update.LocalState{SystemVersion: "14.0.0", DataProfile: profile}, t.TempDir(), false, time.Now().UTC())
			if err != nil || decision.Status != "bridge" || !reflect.DeepEqual(decision.Route, []string{a.Release.ID, b.Release.ID}) {
				t.Fatalf("older client cannot reach B through the original path: %+v, %v", decision, err)
			}
		})
	}
}

func TestSignCommandProducesClientAcceptedPolicyAndRefusesOverwrite(t *testing.T) {
	records, p, key := publisherFixture(t)
	root := t.TempDir()
	write := func(name string, value any) {
		raw, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(root, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for id, r := range records {
		if err := os.Mkdir(filepath.Join(root, id), 0700); err != nil {
			t.Fatal(err)
		}
		write(id+"/release.json", r)
	}
	write("plan.json", p)
	encoded, _ := x509.MarshalPKCS8PrivateKey(key)
	if err := os.WriteFile(filepath.Join(root, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"sign", "--records", root, "--plan", filepath.Join(root, "plan.json"), "--key-id", "fixture", "--key-file", filepath.Join(root, "key.pem"), "--output", filepath.Join(root, "policy.json"), "--config-output", filepath.Join(root, "config.json")}
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := update.Check(context.Background(), raw, records["r1"].Config, update.LocalState{SystemVersion: "14.0.0", DataProfile: "known-v1"}, t.TempDir(), false, time.Now().UTC())
	if err != nil || decision.Status != "direct" || decision.Next.ID != "r2" {
		t.Fatalf("%+v %v", decision, err)
	}
	if err := run(args); err == nil {
		t.Fatal("overwrote signed output")
	}
	// Historical verification checks authenticity/ordering even after a policy expires.
	policy, c, _ := assemble(p, records)
	past := time.Now().Add(-48 * time.Hour).UTC()
	policy.IssuedAt = past.Format(time.RFC3339)
	policy.ExpiresAt = past.Add(time.Hour).Format(time.RFC3339)
	old, err := update.SignPublication(policy, c, "fixture", key, past)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "old.json"), old, 0600)
	verify := []string{"verify", "--config", filepath.Join(root, "config.json"), "--envelope", filepath.Join(root, "old.json")}
	if err := run(verify); err == nil {
		t.Fatal("current verifier accepted expired policy")
	}
	if err := run(append(verify, "--historical")); err != nil {
		t.Fatal(err)
	}
}

func TestSelectPublishedSourceKeepsPlatformSignerAndRevocations(t *testing.T) {
	target := update.Config{ReleaseID: "new", Version: "2.0.0", Platform: "darwin", Arch: "arm64",
		SigningIdentity: "TEAM", UpdaterProtocol: 2}
	prior := update.Release{ID: "old", Version: "1.0.0", Platform: target.Platform, Arch: target.Arch,
		SigningIdentity: target.SigningIdentity, UpdaterProtocol: 2}
	active, revoked, differentSigner, differentPlatform := prior, prior, prior, prior
	active.ID, active.Version = "active", "1.1.0"
	revoked.ID, revoked.Version = "revoked", "1.5.0"
	differentSigner.ID, differentSigner.Version, differentSigner.SigningIdentity = "other-team", "1.6.0", "OTHER"
	differentPlatform.ID, differentPlatform.Version, differentPlatform.Arch = "intel", "1.7.0", "amd64"
	policy := update.Policy{MinUpdaterProtocol: 2, Revoked: []string{revoked.ID},
		Releases: []update.Release{prior, differentSigner, active, revoked, differentPlatform}}
	selected, err := selectPublishedSource(policy, target)
	if err != nil || selected.ID != active.ID {
		t.Fatalf("expected latest active release on the same platform and signer, got %q: %v", selected.ID, err)
	}
	target.Version = active.Version
	if _, err := selectPublishedSource(policy, target); err == nil {
		t.Fatal("accepted a target that was not newer than its published source")
	}
}
