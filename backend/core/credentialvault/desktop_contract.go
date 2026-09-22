package credentialvault

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrLocalKeyNotFound            = errors.New("credential vault local key was not found")
	ErrLocalSecureStoreUnavailable = errors.New("credential vault local secure store is unavailable")
	ErrLocalConflict               = errors.New("credential vault local state conflicts with Cloud")
)

type AccountScope struct {
	CloudIssuer    string
	CloudAccountID string
}

type LocalKeyKind string

const (
	LocalRootKey     LocalKeyKind = "local-root-key"
	RecordSigningKey LocalKeyKind = "record-signing-key"
)

type LocalKeyStore interface {
	Load(context.Context, AccountScope, LocalKeyKind) ([]byte, error)
	Save(context.Context, AccountScope, LocalKeyKind, []byte) error
	Delete(context.Context, AccountScope, LocalKeyKind) error
}

type LocalKeyManager struct {
	mu     sync.Mutex
	store  LocalKeyStore
	random io.Reader
}

func NewLocalKeyManager(store LocalKeyStore, random io.Reader) (*LocalKeyManager, error) {
	if store == nil || random == nil {
		return nil, ErrLocalSecureStoreUnavailable
	}
	return &LocalKeyManager{store: store, random: random}, nil
}

func (manager *LocalKeyManager) RootKey(ctx context.Context, scope AccountScope) ([]byte, error) {
	return manager.loadOrCreate(ctx, scope, LocalRootKey, 32, func() ([]byte, error) {
		value := make([]byte, 32)
		_, err := io.ReadFull(manager.random, value)
		return value, err
	})
}

func (manager *LocalKeyManager) SigningKey(ctx context.Context, scope AccountScope) (ed25519.PrivateKey, error) {
	value, err := manager.loadOrCreate(ctx, scope, RecordSigningKey, ed25519.PrivateKeySize, func() ([]byte, error) {
		_, privateKey, err := ed25519.GenerateKey(manager.random)
		return privateKey, err
	})
	if err != nil {
		return nil, err
	}
	return ed25519.PrivateKey(value), nil
}

func (manager *LocalKeyManager) loadOrCreate(ctx context.Context, scope AccountScope, kind LocalKeyKind, size int, generate func() ([]byte, error)) ([]byte, error) {
	if !validAccountScope(scope) {
		return nil, ErrLocalSecureStoreUnavailable
	}
	// Serialize the complete read/create/save sequence, including the random
	// reader, so first-use requests cannot overwrite each other's root key.
	manager.mu.Lock()
	defer manager.mu.Unlock()
	value, err := manager.store.Load(ctx, scope, kind)
	if err == nil {
		if len(value) != size {
			return nil, ErrLocalSecureStoreUnavailable
		}
		return append([]byte(nil), value...), nil
	}
	if !errors.Is(err, ErrLocalKeyNotFound) {
		return nil, ErrLocalSecureStoreUnavailable
	}
	value, err = generate()
	if err != nil || len(value) != size {
		return nil, ErrLocalSecureStoreUnavailable
	}
	if err := manager.store.Save(ctx, scope, kind, value); err != nil {
		return nil, ErrLocalSecureStoreUnavailable
	}
	return append([]byte(nil), value...), nil
}

type LocalCredentialAAD struct {
	SchemaVersion      int    `json:"schema_version"`
	LocalProviderGroup string `json:"local_provider_group_id"`
	CredentialRevision int64  `json:"credential_revision"`
}

type LocalCredentialCiphertext struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func EncryptLocalCredential(random io.Reader, rootKey []byte, aad LocalCredentialAAD, plaintext []byte) (LocalCredentialCiphertext, error) {
	// Local groups can contain multiple existing keys. Cloud record size limits
	// are enforced by the Cloud envelope, not by local storage or migration.
	if random == nil || len(rootKey) != 32 || len(plaintext) == 0 || !validLocalCredentialAAD(aad) {
		return LocalCredentialCiphertext{}, ErrInvalidContract
	}
	aead, err := localCredentialAEAD(rootKey)
	if err != nil {
		return LocalCredentialCiphertext{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return LocalCredentialCiphertext{}, ErrLocalSecureStoreUnavailable
	}
	encodedAAD, _ := json.Marshal(aad)
	return LocalCredentialCiphertext{Version: 2, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plaintext, encodedAAD)}, nil
}

func DecryptLocalCredential(rootKey []byte, aad LocalCredentialAAD, encrypted LocalCredentialCiphertext) ([]byte, error) {
	if len(rootKey) != 32 || encrypted.Version != 2 || len(encrypted.Nonce) != 12 || len(encrypted.Ciphertext) < 17 || !validLocalCredentialAAD(aad) {
		return nil, ErrInvalidContract
	}
	aead, err := localCredentialAEAD(rootKey)
	if err != nil {
		return nil, err
	}
	encodedAAD, _ := json.Marshal(aad)
	plaintext, err := aead.Open(nil, encrypted.Nonce, encrypted.Ciphertext, encodedAAD)
	if err != nil || len(plaintext) == 0 {
		return nil, ErrInvalidContract
	}
	return plaintext, nil
}

type ManifestCache struct {
	mu       sync.RWMutex
	trust    ed25519.PublicKey
	issuer   string
	now      func() time.Time
	manifest KeyManifest
}

func NewManifestCache(trust ed25519.PublicKey, issuer string, now func() time.Time) (*ManifestCache, error) {
	issuer = strings.TrimSpace(issuer)
	if len(trust) != ed25519.PublicKeySize || issuer == "" || now == nil {
		return nil, ErrInvalidContract
	}
	return &ManifestCache{trust: append(ed25519.PublicKey(nil), trust...), issuer: issuer, now: now}, nil
}

func (cache *ManifestCache) Replace(payload, signature []byte) error {
	manifest, err := VerifyKeyManifest(cache.trust, payload, signature, cache.issuer)
	if err != nil {
		return err
	}
	now := cache.now()
	for _, key := range manifest.Keys {
		notBefore, beforeErr := time.Parse(time.RFC3339, key.NotBefore)
		notAfter, afterErr := time.Parse(time.RFC3339, key.NotAfter)
		publicDER, decodeErr := base64.StdEncoding.DecodeString(key.PublicKey)
		parsed, parseErr := x509.ParsePKIXPublicKey(publicDER)
		publicKey, ok := parsed.(*rsa.PublicKey)
		if beforeErr != nil || afterErr != nil || decodeErr != nil || parseErr != nil || !ok ||
			now.Before(notBefore) || !now.Before(notAfter) || !notBefore.Before(notAfter) ||
			publicKey.N == nil || publicKey.N.BitLen() != 3072 || publicKey.E != 65537 {
			return ErrInvalidContract
		}
	}
	cache.mu.Lock()
	cache.manifest = manifest
	cache.mu.Unlock()
	return nil
}

func (cache *ManifestCache) ActiveKey(shard int) (ShardPublicKey, error) {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	for _, key := range cache.manifest.Keys {
		if key.ShardID == shard && key.Status == "active" {
			return key, nil
		}
	}
	return ShardPublicKey{}, ErrInvalidContract
}

type BackupOperation string

const (
	BackupUpsert BackupOperation = "upsert"
	BackupDelete BackupOperation = "delete"
)

type BackupState string

const (
	BackupPending   BackupState = "pending"
	BackupRunning   BackupState = "running"
	BackupFailed    BackupState = "failed"
	BackupConflict  BackupState = "conflict"
	BackupSucceeded BackupState = "succeeded"
)

type BackupOutboxItem struct {
	ID                      string
	LocalProviderGroupID    string
	LocalCredentialRevision int64
	Operation               BackupOperation
	State                   BackupState
	AttemptCount            int
	NextAttemptAt           time.Time
	LastErrorCode           int
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

func NewBackupOutboxItem(id, groupID string, revision int64, operation BackupOperation, now time.Time) (BackupOutboxItem, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(groupID) == "" || revision < 1 ||
		(operation != BackupUpsert && operation != BackupDelete) || now.IsZero() {
		return BackupOutboxItem{}, ErrInvalidContract
	}
	return BackupOutboxItem{
		ID: id, LocalProviderGroupID: groupID, LocalCredentialRevision: revision, Operation: operation,
		State: BackupPending, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (item BackupOutboxItem) Retry(now time.Time, errorCode int) (BackupOutboxItem, error) {
	if now.IsZero() || errorCode <= 0 || item.AttemptCount < 0 {
		return BackupOutboxItem{}, ErrInvalidContract
	}
	item.AttemptCount++
	delay := time.Minute * time.Duration(1<<min(item.AttemptCount-1, 6))
	if delay > time.Hour {
		delay = time.Hour
	}
	item.State, item.LastErrorCode, item.NextAttemptAt, item.UpdatedAt = BackupFailed, errorCode, now.Add(delay), now
	return item, nil
}

type CloudCredentialBinding struct {
	ID                          string
	CloudIssuer                 string
	CloudAccountID              string
	VaultID                     string
	CloudRecordID               string
	LocalProviderGroupID        string
	LastCloudRevision           int64
	LastLocalCredentialRevision int64
	LastETag                    string
	BackupState                 BackupState
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}

func validAccountScope(scope AccountScope) bool {
	issuer := strings.TrimSpace(scope.CloudIssuer)
	account := strings.TrimSpace(scope.CloudAccountID)
	if issuer == "" || account == "" || len(issuer) > 512 || len(account) > 255 {
		return false
	}
	if issuer == "lazymind-local" {
		return true
	}
	parsed, err := url.Parse(issuer)
	return err == nil && parsed.Scheme != "" && parsed.Host != "" && parsed.String() == issuer
}

func validLocalCredentialAAD(aad LocalCredentialAAD) bool {
	return aad.SchemaVersion == 1 && strings.TrimSpace(aad.LocalProviderGroup) != "" &&
		len(aad.LocalProviderGroup) <= 128 && aad.CredentialRevision > 0
}

func localCredentialAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidContract
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || aead.NonceSize() != 12 {
		return nil, ErrInvalidContract
	}
	return aead, nil
}

func EncodeLocalCredentialCiphertext(value LocalCredentialCiphertext) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func DecodeLocalCredentialCiphertext(value string) (LocalCredentialCiphertext, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	var encrypted LocalCredentialCiphertext
	if err := decoder.Decode(&encrypted); err != nil {
		return LocalCredentialCiphertext{}, ErrInvalidContract
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return LocalCredentialCiphertext{}, ErrInvalidContract
	}
	return encrypted, nil
}
