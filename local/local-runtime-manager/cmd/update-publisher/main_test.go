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
