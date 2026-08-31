package credentialvault

import (
	"bytes"
	"testing"
)

func TestLocalCredentialCipherUsesAccountRootKeyAndBindsRevision(t *testing.T) {
	rootKey := bytes.Repeat([]byte{0x31}, 32)
	aad := LocalCredentialAAD{SchemaVersion: 1, LocalProviderGroup: "provider-group-a", CredentialRevision: 7}
	plaintext := []byte(`{"schema_version":1,"provider_catalog_key":"fixture","api_key":"fixture-local-secret"}`)
	encrypted, err := EncryptLocalCredential(bytes.NewReader(bytes.Repeat([]byte{0x11}, 64)), rootKey, aad, plaintext)
	if err != nil {
		t.Fatalf("encrypt local credential: %v", err)
	}
	if encrypted.Version != 2 || len(encrypted.Nonce) != 12 || bytes.Contains(encrypted.Ciphertext, []byte("fixture-local-secret")) {
		t.Fatalf("local ciphertext version/nonce/plaintext = %d/%d/%t", encrypted.Version, len(encrypted.Nonce), bytes.Contains(encrypted.Ciphertext, plaintext))
	}
	decrypted, err := DecryptLocalCredential(rootKey, aad, encrypted)
	if err != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypt local credential = %q, %v", decrypted, err)
	}
	wrongRevision := aad
	wrongRevision.CredentialRevision++
	if _, err := DecryptLocalCredential(rootKey, wrongRevision, encrypted); err == nil {
		t.Fatal("local ciphertext was accepted for another credential revision")
	}
}

func TestLocalCredentialCipherRejectsTamperingWrongKeyAndOversizedPlaintext(t *testing.T) {
	rootKey := bytes.Repeat([]byte{0x41}, 32)
	aad := LocalCredentialAAD{SchemaVersion: 1, LocalProviderGroup: "provider-group-b", CredentialRevision: 1}
	encrypted, err := EncryptLocalCredential(bytes.NewReader(bytes.Repeat([]byte{0x22}, 64)), rootKey, aad, []byte("fixture-credential"))
	if err != nil {
		t.Fatalf("encrypt local credential: %v", err)
	}
	tampered := encrypted
	tampered.Ciphertext = append([]byte(nil), encrypted.Ciphertext...)
	tampered.Ciphertext[len(tampered.Ciphertext)-1] ^= 0x01
	if _, err := DecryptLocalCredential(rootKey, aad, tampered); err == nil {
		t.Fatal("tampered local credential ciphertext was accepted")
	}
	if _, err := DecryptLocalCredential(bytes.Repeat([]byte{0x42}, 32), aad, encrypted); err == nil {
		t.Fatal("local credential ciphertext was accepted with another root key")
	}
	if _, err := EncryptLocalCredential(bytes.NewReader(bytes.Repeat([]byte{0x23}, 64)), rootKey, aad, bytes.Repeat([]byte("x"), 16*1024+1)); err == nil {
		t.Fatal("oversized local credential plaintext was accepted")
	}
}
