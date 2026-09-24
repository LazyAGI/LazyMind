package cloudclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnableCredentialVaultUsesBearerAndReturnsStrongETag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/credential-vault" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-access-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"`+strings.Repeat("a", 64)+`"`)
		_, _ = io.WriteString(w, `{"vault_id":"00000000-0000-7000-8000-000000000201","status":"active","key_shard_id":17,"active_key_id":"credential-shard-17-v1","record_count":0,"etag_version":1,"created_at":"2026-08-26T06:20:49Z","updated_at":"2026-08-26T06:20:49Z"}`)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	vault, etag, err := client.EnableCredentialVault(context.Background(), "fixture-access-token")
	if err != nil {
		t.Fatalf("enable credential vault: %v", err)
	}
	if vault.KeyShardID != 17 || vault.ActiveKeyID != "credential-shard-17-v1" || etag != strings.Repeat("a", 64) {
		t.Fatalf("enabled vault/etag = %+v/%q", vault, etag)
	}
}

func TestRegisterCredentialVaultMemberSendsOnlyPublicMaterial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(bytes.ToLower(body), []byte("private")) || bytes.Contains(body, []byte("fixture-signing-private")) {
			t.Fatalf("member registration sent private material: %s", body)
		}
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if len(request) != 3 || request["client_member_key"] == nil || request["signing_public_key"] == nil || request["signing_key_version"] == nil {
			t.Errorf("member registration body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(CredentialVaultMember{
			VaultMemberID: "00000000-0000-7000-8000-000000000203", ClientMemberKey: request["client_member_key"].(string),
			SigningPublicKey: bytes.Repeat([]byte{0x11}, 32), SigningKeyVersion: 1, Status: "active", CreatedAt: "2026-08-26T06:20:49Z",
		})
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	member, err := client.RegisterCredentialVaultMember(context.Background(), "fixture-access-token", CredentialVaultMemberRegistration{
		ClientMemberKey: strings.Repeat("m", 43), SigningPublicKey: bytes.Repeat([]byte{0x11}, 32), SigningKeyVersion: 1,
	})
	if err != nil {
		t.Fatalf("register credential vault member: %v", err)
	}
	if member.VaultMemberID == "" || member.Status != "active" {
		t.Fatalf("registered member = %+v", member)
	}
}

func TestGetCredentialVaultKeyManifestReturnsExactSignedPayloadForOfflineVerification(t *testing.T) {
	signedPayload := []byte(`{"schema_version":1,"issuer":"https://cloud.example.test","keys":[]}`)
	signature := bytes.Repeat([]byte{0x51}, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/credential-vault/key-manifest" ||
			r.Header.Get("Authorization") != "Bearer fixture-access-token" {
			t.Errorf("manifest request = %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SignedCredentialKeyManifest{Payload: signedPayload, Signature: signature})
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := client.GetCredentialVaultKeyManifest(context.Background(), "fixture-access-token")
	if err != nil {
		t.Fatalf("get credential vault key manifest: %v", err)
	}
	if !bytes.Equal(manifest.Payload, signedPayload) || !bytes.Equal(manifest.Signature, signature) {
		t.Fatalf("manifest bytes changed before offline verification: %+v", manifest)
	}
}

func TestPutCredentialVaultRecordUsesExplicitConditionAndDoesNotSendPlaintext(t *testing.T) {
	recordID := "00000000-0000-7000-8000-000000000202"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPut || r.URL.Path != "/v1/credential-vault/records/"+recordID || r.Header.Get("If-None-Match") != "*" {
			t.Errorf("record request = %s %s If-None-Match=%q", r.Method, r.URL.Path, r.Header.Get("If-None-Match"))
		}
		for _, forbidden := range [][]byte{[]byte("api_key"), []byte("fixture-plaintext-secret"), []byte("private_key")} {
			if bytes.Contains(bytes.ToLower(body), bytes.ToLower(forbidden)) {
				t.Errorf("record request contains plaintext marker %q: %s", forbidden, body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"`+strings.Repeat("b", 64)+`"`)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	envelope := CredentialVaultRecordEnvelope{
		VaultID: "00000000-0000-7000-8000-000000000201", RecordID: recordID, Revision: 1,
		KeyID: "credential-shard-17-v1", CryptoSuite: "AES-256-GCM+RSA-3072-OAEP-SHA256+Ed25519",
		Nonce: bytes.Repeat([]byte{0x11}, 12), AAD: CredentialRecordAAD{
			ProtocolVersion: "lazymind-credential-vault/v1", CloudIssuer: server.URL,
			CloudAccountID: "00000000-0000-7000-8000-000000000101", VaultID: "00000000-0000-7000-8000-000000000201",
			RecordID: recordID, Revision: 1, KeyID: "credential-shard-17-v1", PayloadType: "provider-credential",
		},
		Ciphertext: bytes.Repeat([]byte{0x22}, 48), WrappedDEK: bytes.Repeat([]byte{0x33}, 384),
		SigningMemberID: "00000000-0000-7000-8000-000000000203", SigningKeyVersion: 1,
		Signature: bytes.Repeat([]byte{0x44}, 64),
	}
	stored, etag, err := client.PutCredentialVaultRecord(context.Background(), "fixture-access-token", envelope, CredentialVaultWriteCondition{Create: true})
	if err != nil {
		t.Fatalf("put credential vault record: %v", err)
	}
	if stored.RecordID != recordID || etag != strings.Repeat("b", 64) {
		t.Fatalf("stored record/etag = %+v/%q", stored, etag)
	}
}
