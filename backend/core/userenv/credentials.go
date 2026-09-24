package userenv

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/common/secretcrypto"
	"lazymind/core/credentialvault"
)

const CredentialVersion = 2

var ErrConflict = errors.New("environment variable changed; reload and retry")

var keyManagerState = struct {
	sync.RWMutex
	manager *credentialvault.LocalKeyManager
}{}

// SetCredentialKeyManager injects shared key infrastructure at application startup.
func SetCredentialKeyManager(manager *credentialvault.LocalKeyManager) func() {
	keyManagerState.Lock()
	previous := keyManagerState.manager
	keyManagerState.manager = manager
	keyManagerState.Unlock()
	return func() {
		keyManagerState.Lock()
		keyManagerState.manager = previous
		keyManagerState.Unlock()
	}
}

type userEnvEnvelope struct {
	Version    int    `json:"version"`
	KeySource  string `json:"key_source"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func userEnvKey(source, userID string) ([]byte, error) {
	if source == "server" {
		secret := strings.TrimSpace(os.Getenv("LAZYMIND_USER_ENV_SECRET_KEY"))
		if path := strings.TrimSpace(os.Getenv("LAZYMIND_USER_ENV_SECRET_KEY_FILE")); path != "" {
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
				return nil, fmt.Errorf("user env key file must be a private regular file")
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("cannot read user env key file")
			}
			secret = strings.TrimSpace(string(raw))
			clear(raw)
		}
		if len(secret) < 32 {
			return nil, fmt.Errorf("user env server key must contain at least 32 bytes")
		}
		key := sha256.Sum256([]byte(secret))
		return key[:], nil
	}
	keyManagerState.RLock()
	manager := keyManagerState.manager
	keyManagerState.RUnlock()
	if source != "device" || manager == nil {
		return nil, credentialvault.ErrLocalSecureStoreUnavailable
	}
	return manager.RootKey(context.Background(), credentialvault.AccountScope{
		CloudIssuer: "lazymind-local", CloudAccountID: userID,
	})
}

func userEnvAAD(row orm.UserEnvironmentVariable) []byte {
	raw, _ := json.Marshal(struct {
		Purpose  string
		Version  int
		UserID   string
		ID       string
		Name     string
		Revision int64
	}{"user_environment_variable", CredentialVersion, row.UserID, row.ID, row.Name, row.CredentialRevision})
	return raw
}

func userEnvCipher(source string, row orm.UserEnvironmentVariable) (cipher.AEAD, error) {
	key, err := userEnvKey(source, row.UserID)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func EncryptValue(row orm.UserEnvironmentVariable, value string) (string, error) {
	source := "device"
	if strings.TrimSpace(os.Getenv("LAZYMIND_USER_ENV_SECRET_KEY")) != "" || strings.TrimSpace(os.Getenv("LAZYMIND_USER_ENV_SECRET_KEY_FILE")) != "" {
		source = "server"
	}
	aead, err := userEnvCipher(source, row)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	plain := []byte(value)
	defer clear(plain)
	raw, err := json.Marshal(userEnvEnvelope{CredentialVersion, source, nonce, aead.Seal(nil, nonce, plain, userEnvAAD(row))})
	return string(raw), err
}

func DecryptValue(row orm.UserEnvironmentVariable) (string, error) {
	if row.CredentialVersion != CredentialVersion {
		return "", fmt.Errorf("unsupported user env credential version")
	}
	var envelope userEnvEnvelope
	if err := json.Unmarshal([]byte(row.ValueCiphertext), &envelope); err != nil || envelope.Version != CredentialVersion {
		return "", fmt.Errorf("invalid user env ciphertext")
	}
	aead, err := userEnvCipher(envelope.KeySource, row)
	if err != nil {
		return "", err
	}
	if len(envelope.Nonce) != aead.NonceSize() {
		return "", fmt.Errorf("invalid user env nonce")
	}
	plain, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, userEnvAAD(row))
	if err != nil {
		return "", fmt.Errorf("cannot decrypt user env credential")
	}
	defer clear(plain)
	return string(plain), nil
}

// The public legacy key is read-only compatibility, never a key for new writes.
func UpgradeCredential(db *gorm.DB, row *orm.UserEnvironmentVariable) (string, error) {
	if row.CredentialVersion != 1 {
		return DecryptValue(*row)
	}
	var wrapper secretcrypto.Wrapper
	if err := json.Unmarshal([]byte(row.ValueCiphertext), &wrapper); err != nil {
		return "", fmt.Errorf("invalid legacy user env credential")
	}
	nonce, err := base64.StdEncoding.DecodeString(wrapper.Nonce)
	if err != nil || len(nonce) != 12 {
		return "", fmt.Errorf("invalid legacy user env nonce")
	}
	keys := []string{
		strings.TrimSpace(os.Getenv("LAZYMIND_USER_ENV_LEGACY_SECRET_KEY")),
		strings.TrimSpace(os.Getenv("LAZYMIND_USER_ENV_SECRET_KEY")),
		"lazymind-core-user-env-default-secret",
	}
	var plain []byte
	for _, key := range keys {
		if key == "" {
			continue
		}
		decoded, ok, decodeErr := secretcrypto.DecodeAESGCM(json.RawMessage(row.ValueCiphertext), key)
		if decodeErr == nil && ok {
			plain = decoded
			break
		}
	}
	if len(plain) == 0 {
		return "", fmt.Errorf("cannot decrypt legacy user env credential")
	}
	defer clear(plain)
	original := *row
	upgraded := original
	upgraded.CredentialVersion = CredentialVersion
	upgraded.CredentialRevision++
	upgraded.ValueCiphertext, err = EncryptValue(upgraded, string(plain))
	if err != nil {
		return "", err
	}
	tx := RevisionQuery(db, original).Updates(map[string]any{
		"value_ciphertext": upgraded.ValueCiphertext, "credential_version": upgraded.CredentialVersion,
		"credential_revision": upgraded.CredentialRevision,
		"updated_at":          original.UpdatedAt,
	})
	if tx.Error != nil {
		return "", tx.Error
	}
	if tx.RowsAffected != 1 {
		return "", ErrConflict
	}
	*row = upgraded
	return string(plain), nil
}

func RevisionQuery(db *gorm.DB, row orm.UserEnvironmentVariable) *gorm.DB {
	return db.Model(&orm.UserEnvironmentVariable{}).Where(
		"id = ? AND user_id = ? AND credential_revision = ? AND value_ciphertext = ? AND updated_at = ?",
		row.ID, row.UserID, row.CredentialRevision, row.ValueCiphertext, row.UpdatedAt,
	)
}
