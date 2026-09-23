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

func TestManifestRotationAllowsOneActiveAndRetiringVersionsForTheSameShard(t *testing.T) {
	now := time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	trustPublic, trustPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oldKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}
	entry := func(keyID, status string, key *rsa.PublicKey) ShardPublicKey {
		der, marshalErr := x509.MarshalPKIXPublicKey(key)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return ShardPublicKey{
			KeyID: keyID, ShardID: 17, Algorithm: "RSA-3072-OAEP-SHA256", PublicKey: base64.StdEncoding.EncodeToString(der),
			Status: status, NotBefore: now.Add(-time.Hour).Format(time.RFC3339), NotAfter: now.Add(90 * 24 * time.Hour).Format(time.RFC3339),
		}
	}
	manifest := KeyManifest{SchemaVersion: 1, Issuer: "https://cloud.example.test", Keys: []ShardPublicKey{
		entry("credential-shard-17-v1", "retiring", &oldKey.PublicKey),
		entry("credential-shard-17-v2", "active", &newKey.PublicKey),
	}}
	payload, _ := json.Marshal(manifest)
	cache, err := NewManifestCache(trustPublic, manifest.Issuer, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Replace(payload, ed25519.Sign(trustPrivate, payload)); err != nil {
		t.Fatalf("replace manifest during shard rotation: %v", err)
	}
	active, err := cache.ActiveKey(17)
	if err != nil || active.KeyID != "credential-shard-17-v2" {
		t.Fatalf("active rotated key = %+v, %v", active, err)
	}
	retiring, err := cache.KeyByID("credential-shard-17-v1")
	if err != nil || retiring.Status != "retiring" {
		t.Fatalf("retiring key lookup = %+v, %v", retiring, err)
	}
}

func TestManifestRotationRejectsTwoActiveKeysForOneShard(t *testing.T) {
	now := time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	trustPublic, trustPrivate, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := rsa.GenerateKey(rand.Reader, 3072)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	entry := ShardPublicKey{
		ShardID: 4, Algorithm: "RSA-3072-OAEP-SHA256", PublicKey: base64.StdEncoding.EncodeToString(der), Status: "active",
		NotBefore: now.Add(-time.Hour).Format(time.RFC3339), NotAfter: now.Add(time.Hour).Format(time.RFC3339),
	}
	first, second := entry, entry
	first.KeyID, second.KeyID = "credential-shard-04-v1", "credential-shard-04-v2"
	payload, _ := json.Marshal(KeyManifest{SchemaVersion: 1, Issuer: "https://cloud.example.test", Keys: []ShardPublicKey{first, second}})
	cache, _ := NewManifestCache(trustPublic, "https://cloud.example.test", func() time.Time { return now })
	if err := cache.Replace(payload, ed25519.Sign(trustPrivate, payload)); err == nil {
		t.Fatal("manifest accepted two active key versions for one shard")
	}
}
