package credentialvault

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var ErrInvalidContract = errors.New("credential vault contract is invalid")

const (
	ProtocolVersion        = "lazymind-credential-vault/v1"
	CredentialCryptoSuite  = "AES-256-GCM+RSA-3072-OAEP-SHA256+Ed25519"
	RecordDEKSize          = 32
	GCMNonceSize           = 12
	MaximumPlaintextLength = 16 * 1024
)

type RecordAAD struct {
	ProtocolVersion string `json:"protocol_version"`
	CloudIssuer     string `json:"cloud_issuer"`
	CloudAccountID  string `json:"cloud_account_id"`
	VaultID         string `json:"vault_id"`
	RecordID        string `json:"record_id"`
	Revision        int64  `json:"revision"`
	KeyID           string `json:"key_id"`
	PayloadType     string `json:"payload_type"`
}

type RecordEnvelope struct {
	AAD               RecordAAD `json:"aad"`
	Nonce             []byte    `json:"nonce"`
	Ciphertext        []byte    `json:"ciphertext"`
	WrappedDEK        []byte    `json:"wrapped_dek"`
	SigningMemberID   string    `json:"signing_member_id"`
	SigningKeyVersion int       `json:"signing_key_version"`
	Signature         []byte    `json:"signature"`
}

type ShardPublicKey struct {
	KeyID     string `json:"key_id"`
	ShardID   int    `json:"shard_id"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"public_key"`
	Status    string `json:"status"`
	NotBefore string `json:"not_before"`
	NotAfter  string `json:"not_after"`
}

type KeyManifest struct {
	SchemaVersion int              `json:"schema_version"`
	Issuer        string           `json:"issuer"`
	Keys          []ShardPublicKey `json:"keys"`
}

func CanonicalAAD(value RecordAAD) ([]byte, error) {
	if value.ProtocolVersion != ProtocolVersion ||
		strings.TrimSpace(value.CloudIssuer) == "" ||
		strings.TrimSpace(value.CloudAccountID) == "" ||
		strings.TrimSpace(value.VaultID) == "" ||
		strings.TrimSpace(value.RecordID) == "" ||
		value.Revision < 1 ||
		strings.TrimSpace(value.KeyID) == "" ||
		strings.TrimSpace(value.PayloadType) == "" {
		return nil, ErrInvalidContract
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode credential vault AAD: %w", err)
	}
	return encoded, nil
}

func EncryptPayload(random io.Reader, dek, aad, plaintext []byte) (nonce, ciphertext []byte, err error) {
	if random == nil || len(dek) != RecordDEKSize || len(aad) == 0 ||
		len(plaintext) == 0 || len(plaintext) > MaximumPlaintextLength {
		return nil, nil, ErrInvalidContract
	}
	aead, err := payloadAEAD(dek)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate credential vault nonce: %w", err)
	}
	return nonce, aead.Seal(nil, nonce, plaintext, aad), nil
}

func DecryptPayload(dek, nonce, aad, ciphertext []byte) ([]byte, error) {
	if len(dek) != RecordDEKSize || len(aad) == 0 || len(ciphertext) == 0 {
		return nil, ErrInvalidContract
	}
	aead, err := payloadAEAD(dek)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, ErrInvalidContract
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential vault payload: %w", err)
	}
	if len(plaintext) == 0 || len(plaintext) > MaximumPlaintextLength {
		return nil, ErrInvalidContract
	}
	return plaintext, nil
}

func WrapDEK(random io.Reader, publicKey *rsa.PublicKey, dek, label []byte) ([]byte, error) {
	if random == nil || !validRSAPublicKey(publicKey) || len(dek) != RecordDEKSize || len(label) == 0 {
		return nil, ErrInvalidContract
	}
	wrapped, err := rsa.EncryptOAEP(sha256.New(), random, publicKey, dek, label)
	if err != nil {
		return nil, fmt.Errorf("wrap credential vault DEK: %w", err)
	}
	return wrapped, nil
}

func UnwrapDEK(privateKey *rsa.PrivateKey, wrapped, label []byte) ([]byte, error) {
	if privateKey == nil || !validRSAPublicKey(&privateKey.PublicKey) ||
		len(wrapped) == 0 || len(label) == 0 {
		return nil, ErrInvalidContract
	}
	dek, err := rsa.DecryptOAEP(sha256.New(), nil, privateKey, wrapped, label)
	if err != nil {
		return nil, fmt.Errorf("unwrap credential vault DEK: %w", err)
	}
	if len(dek) != RecordDEKSize {
		return nil, ErrInvalidContract
	}
	return dek, nil
}

func RecordSigningBytes(envelope RecordEnvelope) ([]byte, error) {
	aad, err := CanonicalAAD(envelope.AAD)
	if err != nil {
		return nil, err
	}
	if len(envelope.Nonce) != GCMNonceSize ||
		len(envelope.Ciphertext) == 0 ||
		len(envelope.WrappedDEK) == 0 ||
		strings.TrimSpace(envelope.SigningMemberID) == "" ||
		envelope.SigningKeyVersion < 1 {
		return nil, ErrInvalidContract
	}
	unsigned := struct {
		AAD               json.RawMessage `json:"aad"`
		Nonce             []byte          `json:"nonce"`
		Ciphertext        []byte          `json:"ciphertext"`
		WrappedDEK        []byte          `json:"wrapped_dek"`
		SigningMemberID   string          `json:"signing_member_id"`
		SigningKeyVersion int             `json:"signing_key_version"`
	}{
		AAD: json.RawMessage(aad), Nonce: envelope.Nonce,
		Ciphertext: envelope.Ciphertext, WrappedDEK: envelope.WrappedDEK,
		SigningMemberID: envelope.SigningMemberID, SigningKeyVersion: envelope.SigningKeyVersion,
	}
	encoded, err := json.Marshal(unsigned)
	if err != nil {
		return nil, fmt.Errorf("encode credential vault record signature input: %w", err)
	}
	return encoded, nil
}

func SignRecord(privateKey ed25519.PrivateKey, envelope RecordEnvelope) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidContract
	}
	encoded, err := RecordSigningBytes(envelope)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(privateKey, encoded), nil
}

func VerifyRecordSignature(publicKey ed25519.PublicKey, envelope RecordEnvelope) error {
	if len(publicKey) != ed25519.PublicKeySize || len(envelope.Signature) != ed25519.SignatureSize {
		return ErrInvalidContract
	}
	encoded, err := RecordSigningBytes(envelope)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, encoded, envelope.Signature) {
		return ErrInvalidContract
	}
	return nil
}

func VerifyKeyManifest(publicKey ed25519.PublicKey, payload, signature []byte, expectedIssuer string) (KeyManifest, error) {
	if len(publicKey) != ed25519.PublicKeySize ||
		len(signature) != ed25519.SignatureSize ||
		len(payload) == 0 ||
		strings.TrimSpace(expectedIssuer) == "" ||
		!ed25519.Verify(publicKey, payload, signature) {
		return KeyManifest{}, ErrInvalidContract
	}
	var manifest KeyManifest
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return KeyManifest{}, fmt.Errorf("decode credential vault key manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return KeyManifest{}, ErrInvalidContract
	}
	if manifest.SchemaVersion != 1 || manifest.Issuer != expectedIssuer ||
		len(manifest.Keys) == 0 || len(manifest.Keys) > 256 {
		return KeyManifest{}, ErrInvalidContract
	}
	keyIDs := make(map[string]struct{}, len(manifest.Keys))
	activeByShard := make(map[int]int, len(manifest.Keys))
	seenShards := make(map[int]struct{}, len(manifest.Keys))
	for _, key := range manifest.Keys {
		if strings.TrimSpace(key.KeyID) == "" ||
			key.ShardID < 0 || key.ShardID >= 64 ||
			key.Algorithm != "RSA-3072-OAEP-SHA256" ||
			strings.TrimSpace(key.PublicKey) == "" ||
			(key.Status != "active" && key.Status != "retiring") ||
			strings.TrimSpace(key.NotBefore) == "" ||
			strings.TrimSpace(key.NotAfter) == "" {
			return KeyManifest{}, ErrInvalidContract
		}
		if _, exists := keyIDs[key.KeyID]; exists {
			return KeyManifest{}, ErrInvalidContract
		}
		if key.Status == "active" {
			activeByShard[key.ShardID]++
		}
		keyIDs[key.KeyID] = struct{}{}
		seenShards[key.ShardID] = struct{}{}
	}
	for shardID := range seenShards {
		if activeByShard[shardID] != 1 {
			return KeyManifest{}, ErrInvalidContract
		}
	}
	return manifest, nil
}

func payloadAEAD(dek []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("create credential vault cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential vault AEAD: %w", err)
	}
	if aead.NonceSize() != GCMNonceSize {
		return nil, ErrInvalidContract
	}
	return aead, nil
}

func validRSAPublicKey(publicKey *rsa.PublicKey) bool {
	return publicKey != nil && publicKey.N != nil && publicKey.E == 65537 && publicKey.N.BitLen() == 3072
}
