package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mesotron7x/LazyMind/local/local-runtime-manager/internal/desktopupdate"
)

func TestUnconfiguredHelperIsExplicitlyDisabled(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"config"}, strings.NewReader(""), &out, "", t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "disabled" || result["reason"] != "not_configured" {
		t.Fatalf("result=%v", result)
	}
}

func TestCLIRejectsRuntimeConfigurationOverrides(t *testing.T) {
	for _, args := range [][]string{{"config", "--config", "attacker.json"}, {"check", "--trust-root", "/tmp/alternate"}, {"install"}} {
		if err := run(context.Background(), args, strings.NewReader(""), &bytes.Buffer{}, "", t.TempDir(), time.Now()); err == nil {
			t.Fatalf("unsupported invocation accepted: %v", args)
		}
	}
	if err := run(context.Background(), []string{"config"}, strings.NewReader(""), &bytes.Buffer{}, "invalid-base64", t.TempDir(), time.Now()); err == nil {
		t.Fatal("invalid packaged config treated as disabled")
	}
}

func TestCLIActuallyAcceptsSignedPolicyAndRejectsReplay(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	cfg := desktopupdate.Config{TrustDomain: "cli-fixture", Audience: "official", Channel: "stable", Mode: "official", ReleaseID: "current", Version: "1.0.0", Platform: "darwin", Arch: "arm64", SigningIdentity: "FIXTURE_TEAM", UpdaterProtocol: 1, PolicyURL: "https://updates.example.invalid/policy.json", AllowedOrigins: []string{"https://updates.example.invalid"}, PublicKeys: map[string]string{"key": base64.StdEncoding.EncodeToString(pub)}}
	configRaw, _ := json.Marshal(cfg)
	config := base64.StdEncoding.EncodeToString(configRaw)
	artifact := map[string]any{"url": "https://updates.example.invalid/immutable/source.zip", "sha256": strings.Repeat("a", 64), "size": 1024}
	policy := map[string]any{"schemaVersion": 1, "minUpdaterProtocol": 1, "trustDomain": "cli-fixture", "audience": "official", "channel": "stable", "policySequence": 42, "issuedAt": now.Add(-time.Hour).Format(time.RFC3339), "expiresAt": now.Add(time.Hour).Format(time.RFC3339), "releases": []any{map[string]any{"releaseId": "current", "version": "1.0.0", "platform": "darwin", "arch": "arm64", "minimumSystemVersion": "12.0.0", "minUpdaterProtocol": 1, "updaterProtocol": 1, "signingIdentity": "FIXTURE_TEAM", "package": artifact, "feed": artifact, "components": artifact}}, "edges": []any{}, "revoked": []string{}}
	request := func() []byte {
		payload, _ := json.Marshal(policy)
		envelope := map[string]string{"keyId": "key", "payload": base64.StdEncoding.EncodeToString(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))}
		raw, _ := json.Marshal(map[string]any{"envelope": envelope, "state": map[string]any{"systemVersion": "14.0.0", "dataProfile": "known-v1", "components": map[string]string{}}})
		return raw
	}
	root := t.TempDir()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"check"}, bytes.NewReader(request()), &out, config, root, now); err != nil {
		t.Fatal(err)
	}
	var decision desktopupdate.Decision
	if err := json.Unmarshal(out.Bytes(), &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Status != "up_to_date" || decision.PolicySequence != 42 || len(decision.PolicyDigest) != 64 {
		t.Fatalf("decision=%+v", decision)
	}
	policy["policySequence"] = 41
	if err := run(context.Background(), []string{"check"}, bytes.NewReader(request()), &bytes.Buffer{}, config, root, now); err == nil {
		t.Fatal("CLI bypassed persistent replay protection")
	}
}
