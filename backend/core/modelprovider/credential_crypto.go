package modelprovider

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

const modelProviderCredentialVersion = 2

var credentialKeyManagerState = struct {
	sync.RWMutex
	manager *credentialvault.LocalKeyManager
}{}

func SetCredentialKeyManager(manager *credentialvault.LocalKeyManager) func() {
	credentialKeyManagerState.Lock()
	previous := credentialKeyManagerState.manager
	credentialKeyManagerState.manager = manager
	credentialKeyManagerState.Unlock()
	return func() {
		credentialKeyManagerState.Lock()
		credentialKeyManagerState.manager = previous
		credentialKeyManagerState.Unlock()
	}
}

func credentialKeyManager() *credentialvault.LocalKeyManager {
	credentialKeyManagerState.RLock()
	defer credentialKeyManagerState.RUnlock()
	return credentialKeyManagerState.manager
}

func modelProviderEncryptionKey() string { return "" }

type modelProviderCredentialEnvelope struct {
	Version int                                `json:"version"`
	Scope   credentialvault.AccountScope       `json:"scope"`
	AAD     credentialvault.LocalCredentialAAD `json:"aad"`
	Nonce   []byte                             `json:"nonce"`
	Body    []byte                             `json:"ciphertext"`
}

func localProviderCredentialScope(userID string) credentialvault.AccountScope {
	return credentialvault.AccountScope{CloudIssuer: "lazymind-local", CloudAccountID: strings.TrimSpace(userID)}
}

func encryptModelProviderAPIKeyForGroup(userID, groupID string, revision int64, apiKey string) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", nil
	}
	manager := credentialKeyManager()
	if manager == nil {
		return "", credentialvault.ErrLocalSecureStoreUnavailable
	}
	plaintext := []byte(apiKey)
	defer clear(plaintext)
	return encryptModelProviderAPIKeyBytes(manager, userID, groupID, revision, plaintext)
}

func encryptModelProviderAPIKeyBytes(manager *credentialvault.LocalKeyManager, userID, groupID string, revision int64, plaintext []byte) (string, error) {
	if manager == nil {
		return "", credentialvault.ErrLocalSecureStoreUnavailable
	}
	if len(plaintext) == 0 || len(plaintext) > 512 {
		return "", credentialvault.ErrInvalidContract
	}
	scope := localProviderCredentialScope(userID)
	aad := credentialvault.LocalCredentialAAD{SchemaVersion: 1, LocalProviderGroup: strings.TrimSpace(groupID), CredentialRevision: revision}
	rootKey, err := manager.RootKey(context.Background(), scope)
	if err != nil {
		return "", err
	}
	defer clear(rootKey)
	encrypted, err := credentialvault.EncryptLocalCredential(rand.Reader, rootKey, aad, plaintext)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(modelProviderCredentialEnvelope{
		Version: encrypted.Version, Scope: scope, AAD: aad, Nonce: encrypted.Nonce, Body: encrypted.Ciphertext,
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func decryptModelProviderAPIKeyForGroup(userID, groupID string, revision int64, ciphertext string) (string, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return "", nil
	}
	var envelope modelProviderCredentialEnvelope
	decoder := json.NewDecoder(strings.NewReader(ciphertext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || envelope.Version != modelProviderCredentialVersion {
		return "", fmt.Errorf("unsupported model provider credential ciphertext")
	}
	wantScope := localProviderCredentialScope(userID)
	wantAAD := credentialvault.LocalCredentialAAD{SchemaVersion: 1, LocalProviderGroup: strings.TrimSpace(groupID), CredentialRevision: revision}
	if envelope.Scope != wantScope || envelope.AAD != wantAAD {
		return "", credentialvault.ErrInvalidContract
	}
	manager := credentialKeyManager()
	if manager == nil {
		return "", credentialvault.ErrLocalSecureStoreUnavailable
	}
	rootKey, err := manager.RootKey(context.Background(), wantScope)
	if err != nil {
		return "", err
	}
	defer clear(rootKey)
	plaintext, err := credentialvault.DecryptLocalCredential(rootKey, wantAAD, credentialvault.LocalCredentialCiphertext{
		Version: envelope.Version, Nonce: envelope.Nonce, Ciphertext: envelope.Body,
	})
	if err != nil {
		return "", err
	}
	defer clear(plaintext)
	return string(plaintext), nil
}

func encryptModelProviderAPIKey(apiKey string) (string, error) {
	return encryptModelProviderAPIKeyForGroup("test-local-user", "test-provider-group", 1, apiKey)
}

func decryptModelProviderAPIKey(ciphertext string) (string, error) {
	return decryptModelProviderAPIKeyForGroup("test-local-user", "test-provider-group", 1, ciphertext)
}

func ResolveAPIKey(legacyPlaintext, ciphertext string) (string, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return strings.TrimSpace(legacyPlaintext), nil
	}
	var envelope modelProviderCredentialEnvelope
	if err := json.Unmarshal([]byte(ciphertext), &envelope); err == nil && envelope.Version == modelProviderCredentialVersion {
		return decryptModelProviderAPIKeyForGroup(envelope.Scope.CloudAccountID, envelope.AAD.LocalProviderGroup, envelope.AAD.CredentialRevision, ciphertext)
	}
	return decodeLegacyModelProviderCiphertext(ciphertext)
}

func apiKeyForGroup(db *gorm.DB, row *orm.UserModelProviderGroup) (string, error) {
	if row == nil {
		return "", credentialvault.ErrInvalidContract
	}
	if row.CredentialRevision < 1 {
		row.CredentialRevision = 1
	}
	if strings.TrimSpace(row.APIKeyCiphertext) != "" && row.CredentialVersion >= modelProviderCredentialVersion {
		return decryptModelProviderAPIKeyForGroup(row.CreateUserID, row.ID, row.CredentialRevision, row.APIKeyCiphertext)
	}
	apiKey := strings.TrimSpace(row.APIKey)
	if apiKey == "" && strings.TrimSpace(row.APIKeyCiphertext) != "" {
		legacy, err := decodeLegacyModelProviderCiphertext(row.APIKeyCiphertext)
		if err != nil {
			return "", err
		}
		apiKey = strings.TrimSpace(legacy)
	}
	if apiKey == "" {
		return "", nil
	}
	ciphertext, err := encryptModelProviderAPIKeyForGroup(row.CreateUserID, row.ID, row.CredentialRevision, apiKey)
	if err != nil {
		return "", err
	}
	if db != nil {
		if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", row.ID).Updates(map[string]any{
			"api_key": "", "api_key_ciphertext": ciphertext, "credential_version": modelProviderCredentialVersion,
			"credential_revision": row.CredentialRevision,
		}).Error; err != nil {
			return "", err
		}
	}
	row.APIKey, row.APIKeyCiphertext, row.CredentialVersion = "", ciphertext, modelProviderCredentialVersion
	return apiKey, nil
}

func encryptedAPIKeyUpdates(userID, groupID string, revision int64, apiKey string) (map[string]any, error) {
	ciphertext, err := encryptModelProviderAPIKeyForGroup(userID, groupID, revision, apiKey)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"api_key": "", "api_key_ciphertext": ciphertext, "credential_version": modelProviderCredentialVersion,
		"credential_revision": revision,
	}, nil
}

func MigrateLegacyAPIKeys(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	var rows []orm.UserModelProviderGroup
	if err := db.Where("(TRIM(api_key) <> '' OR TRIM(api_key_ciphertext) <> '') AND credential_version < ?", modelProviderCredentialVersion).Find(&rows).Error; err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for i := range rows {
			if _, err := apiKeyForGroup(tx, &rows[i]); err != nil {
				return fmt.Errorf("migrate model provider credential %s: %w", rows[i].ID, err)
			}
		}
		return nil
	})
}
