package credentialvault

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func d2SignedManifest(t *testing.T, privateKey ed25519.PrivateKey, issuer, keyID string, shard int, notBefore, notAfter time.Time) ([]byte, []byte) {
	t.Helper()
	shardKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&shardKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest := KeyManifest{SchemaVersion: 1, Issuer: issuer, Keys: []ShardPublicKey{{
		KeyID: keyID, ShardID: shard, Algorithm: "RSA-3072-OAEP-SHA256",
		PublicKey: base64.StdEncoding.EncodeToString(publicDER), Status: "active",
		NotBefore: notBefore.Format(time.RFC3339), NotAfter: notAfter.Format(time.RFC3339),
	}}}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return payload, ed25519.Sign(privateKey, payload)
}

func TestManifestCacheAcceptsOnlyOfflineTrustRootAndValidRSA3072Key(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 6, 20, 49, 0, time.UTC)
	cache, err := NewManifestCache(publicKey, "https://cloud.example.test", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	payload, signature := d2SignedManifest(t, privateKey, "https://cloud.example.test", "credential-shard-17-v1", 17, now.Add(-time.Hour), now.Add(24*time.Hour))
	if err := cache.Replace(payload, signature); err != nil {
		t.Fatalf("replace signed manifest: %v", err)
	}
	key, err := cache.ActiveKey(17)
	if err != nil {
		t.Fatalf("active manifest key: %v", err)
	}
	if key.KeyID != "credential-shard-17-v1" || key.ShardID != 17 || key.Algorithm != "RSA-3072-OAEP-SHA256" {
		t.Fatalf("active manifest key = %+v", key)
	}
}

func TestManifestCacheRejectsCloudReplacementAndKeepsLastKnownGood(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, attackerKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 6, 20, 49, 0, time.UTC)
	cache, err := NewManifestCache(publicKey, "https://cloud.example.test", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	validPayload, validSignature := d2SignedManifest(t, privateKey, "https://cloud.example.test", "credential-shard-09-v1", 9, now.Add(-time.Hour), now.Add(24*time.Hour))
	if err := cache.Replace(validPayload, validSignature); err != nil {
		t.Fatalf("seed last-known-good manifest: %v", err)
	}
	attackerPayload, attackerSignature := d2SignedManifest(t, attackerKey, "https://cloud.example.test", "credential-shard-09-attacker", 9, now.Add(-time.Hour), now.Add(24*time.Hour))
	if err := cache.Replace(attackerPayload, attackerSignature); err == nil {
		t.Fatal("manifest signed by an untrusted Cloud-controlled key was accepted")
	}
	key, err := cache.ActiveKey(9)
	if err != nil || key.KeyID != "credential-shard-09-v1" {
		t.Fatalf("last-known-good key after rejected replacement = %+v, %v", key, err)
	}
}

func TestManifestCacheFailsClosedForExpiredOrWrongIssuerManifest(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 6, 20, 49, 0, time.UTC)
	for name, fixture := range map[string]struct {
		issuer              string
		notBefore, notAfter time.Time
	}{
		"wrong issuer": {"https://attacker.example.test", now.Add(-time.Hour), now.Add(time.Hour)},
		"expired":      {"https://cloud.example.test", now.Add(-2 * time.Hour), now.Add(-time.Hour)},
		"future":       {"https://cloud.example.test", now.Add(time.Hour), now.Add(2 * time.Hour)},
	} {
		t.Run(name, func(t *testing.T) {
			cache, err := NewManifestCache(publicKey, "https://cloud.example.test", func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			payload, signature := d2SignedManifest(t, privateKey, fixture.issuer, "credential-shard-03-v1", 3, fixture.notBefore, fixture.notAfter)
			if err := cache.Replace(payload, signature); err == nil {
				t.Fatalf("%s manifest was accepted", name)
			}
			if _, err := cache.ActiveKey(3); err == nil {
				t.Fatalf("%s manifest left an active key", name)
			}
		})
	}
}
