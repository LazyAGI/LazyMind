package credentialvault

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"testing"
)

func fixtureAAD() RecordAAD {
	return RecordAAD{
		ProtocolVersion: ProtocolVersion,
		CloudIssuer:     "https://fixture-cloud.example.invalid",
		CloudAccountID:  "00000000-0000-7000-8000-000000000101",
		VaultID:         "00000000-0000-7000-8000-000000000102",
		RecordID:        "00000000-0000-7000-8000-000000000103",
		Revision:        7,
		KeyID:           "fixture-credential-shard-17-v1",
		PayloadType:     "provider-credential",
	}
}

func TestCredentialVaultProtocolConstants(t *testing.T) {
	if RecordDEKSize != 32 || GCMNonceSize != 12 || MaximumPlaintextLength != 16*1024 {
		t.Fatalf("unexpected protocol limits: dek=%d nonce=%d plaintext=%d", RecordDEKSize, GCMNonceSize, MaximumPlaintextLength)
	}
	if ProtocolVersion != "lazymind-credential-vault/v1" {
		t.Fatalf("protocol version = %q", ProtocolVersion)
	}
}

func TestCanonicalAADIsDeterministicAndBindsEveryField(t *testing.T) {
	base := fixtureAAD()
	first, err := CanonicalAAD(base)
	if err != nil {
		t.Fatalf("CanonicalAAD: %v", err)
	}
	second, err := CanonicalAAD(base)
	if err != nil {
		t.Fatalf("CanonicalAAD repeat: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("canonical AAD changed for the same input")
	}

	mutations := []RecordAAD{
		func() RecordAAD { value := base; value.CloudIssuer += "/other"; return value }(),
		func() RecordAAD { value := base; value.CloudAccountID += "-other"; return value }(),
		func() RecordAAD { value := base; value.VaultID += "-other"; return value }(),
		func() RecordAAD { value := base; value.RecordID += "-other"; return value }(),
		func() RecordAAD { value := base; value.Revision++; return value }(),
		func() RecordAAD { value := base; value.KeyID += "-other"; return value }(),
		func() RecordAAD { value := base; value.PayloadType += "-other"; return value }(),
	}
	for index, mutation := range mutations {
		encoded, err := CanonicalAAD(mutation)
		if err != nil {
			t.Fatalf("CanonicalAAD mutation %d: %v", index, err)
		}
		if bytes.Equal(first, encoded) {
			t.Errorf("AAD mutation %d did not change canonical bytes", index)
		}
	}
}

func TestPayloadAEADRoundTripAndRejectsTampering(t *testing.T) {
	dek := bytes.Repeat([]byte{0x41}, RecordDEKSize)
	aad, err := CanonicalAAD(fixtureAAD())
	if err != nil {
		t.Fatalf("CanonicalAAD: %v", err)
	}
	plaintext := []byte("{\"schema_version\":1,\"provider_catalog_key\":\"fixture-openai\",\"api_key\":\"fixture-key-never-valid\"}")
	nonce, ciphertext, err := EncryptPayload(rand.Reader, dek, aad, plaintext)
	if err != nil {
		t.Fatalf("EncryptPayload: %v", err)
	}
	if len(nonce) != GCMNonceSize || bytes.Contains(ciphertext, []byte("fixture-key-never-valid")) {
		t.Fatalf("invalid encrypted result: nonce=%d ciphertext_contains_plaintext=%t", len(nonce), bytes.Contains(ciphertext, []byte("fixture-key-never-valid")))
	}
	decrypted, err := DecryptPayload(dek, nonce, aad, ciphertext)
	if err != nil {
		t.Fatalf("DecryptPayload: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("round trip = %q, want %q", decrypted, plaintext)
	}

	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := DecryptPayload(dek, nonce, aad, tampered); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	wrongAAD := append([]byte(nil), aad...)
	wrongAAD[len(wrongAAD)-1] ^= 0x01
	if _, err := DecryptPayload(dek, nonce, wrongAAD, ciphertext); err == nil {
		t.Fatal("ciphertext was accepted with different AAD")
	}
}

func TestRSAOAEPWrapRoundTripAndWrongKeyFailure(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}
	wrongPrivateKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}
	dek := bytes.Repeat([]byte{0x52}, RecordDEKSize)
	label := []byte("lazymind-credential-vault/v1:fixture-record")
	wrapped, err := WrapDEK(rand.Reader, &privateKey.PublicKey, dek, label)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	unwrapped, err := UnwrapDEK(privateKey, wrapped, label)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if !bytes.Equal(unwrapped, dek) {
		t.Fatal("unwrapped DEK differs")
	}
	if _, err := UnwrapDEK(wrongPrivateKey, wrapped, label); err == nil {
		t.Fatal("wrong RSA private key unwrapped the DEK")
	}
}

func TestRecordSignatureCoversCiphertextAndWrappedDEK(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	envelope := RecordEnvelope{
		AAD:               fixtureAAD(),
		Nonce:             bytes.Repeat([]byte{0x11}, GCMNonceSize),
		Ciphertext:        []byte("fixture-ciphertext"),
		WrappedDEK:        []byte("fixture-wrapped-dek"),
		SigningMemberID:   "fixture-member",
		SigningKeyVersion: 1,
	}
	signature, err := SignRecord(privateKey, envelope)
	if err != nil {
		t.Fatalf("SignRecord: %v", err)
	}
	envelope.Signature = signature
	if err := VerifyRecordSignature(publicKey, envelope); err != nil {
		t.Fatalf("VerifyRecordSignature: %v", err)
	}

	envelope.Ciphertext = append(envelope.Ciphertext, 0x01)
	if err := VerifyRecordSignature(publicKey, envelope); err == nil {
		t.Fatal("record signature accepted changed ciphertext")
	}
}

func TestKeyManifestVerificationBindsIssuerAndSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := KeyManifest{
		SchemaVersion: 1,
		Issuer:        "https://fixture-cloud.example.invalid",
		Keys: []ShardPublicKey{{
			KeyID: "fixture-credential-shard-17-v1", ShardID: 17,
			Algorithm: "RSA-3072-OAEP-SHA256", PublicKey: "fixture-public-key",
			Status: "active", NotBefore: "2026-08-26T00:00:00Z", NotAfter: "2027-08-26T00:00:00Z",
		}},
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, payload)
	verified, err := VerifyKeyManifest(publicKey, payload, signature, manifest.Issuer)
	if err != nil {
		t.Fatalf("VerifyKeyManifest: %v", err)
	}
	if verified.Issuer != manifest.Issuer || len(verified.Keys) != 1 {
		t.Fatalf("verified manifest = %+v", verified)
	}
	if _, err := VerifyKeyManifest(publicKey, payload, signature, "https://attacker.example.invalid"); err == nil {
		t.Fatal("manifest was accepted for another issuer")
	}
}
