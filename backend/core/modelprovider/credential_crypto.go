package modelprovider

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/common/secretcrypto"
)

const modelProviderCredentialVersion = 1

func modelProviderEncryptionKey() string {
	if key := strings.TrimSpace(os.Getenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY")); key != "" {
		return key
	}
	return "lazymind-core-model-provider-default-secret"
}

func encryptModelProviderAPIKey(apiKey string) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", nil
	}
	raw, err := secretcrypto.EncodeAESGCM([]byte(apiKey), modelProviderEncryptionKey())
	return string(raw), err
}

func decryptModelProviderAPIKey(ciphertext string) (string, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return "", nil
	}
	decoded, ok, err := secretcrypto.DecodeAESGCM(json.RawMessage(ciphertext), modelProviderEncryptionKey())
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("unsupported model provider credential ciphertext")
	}
	return string(decoded), nil
}

// ResolveAPIKey returns the plaintext credential from the new encrypted column,
// falling back to the legacy plaintext column during migration.
func ResolveAPIKey(legacyPlaintext, ciphertext string) (string, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return strings.TrimSpace(legacyPlaintext), nil
	}
	return decryptModelProviderAPIKey(ciphertext)
}

func apiKeyForGroup(db *gorm.DB, row *orm.UserModelProviderGroup) (string, error) {
	if strings.TrimSpace(row.APIKeyCiphertext) != "" {
		return decryptModelProviderAPIKey(row.APIKeyCiphertext)
	}
	apiKey := strings.TrimSpace(row.APIKey)
	if apiKey == "" {
		return "", nil
	}
	ciphertext, err := encryptModelProviderAPIKey(apiKey)
	if err != nil {
		return "", err
	}
	if db != nil {
		if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", row.ID).Updates(map[string]any{
			"api_key": "", "api_key_ciphertext": ciphertext, "credential_version": modelProviderCredentialVersion,
		}).Error; err != nil {
			return "", err
		}
	}
	row.APIKey = ""
	row.APIKeyCiphertext = ciphertext
	row.CredentialVersion = modelProviderCredentialVersion
	return apiKey, nil
}

func encryptedAPIKeyUpdates(apiKey string) (map[string]any, error) {
	ciphertext, err := encryptModelProviderAPIKey(apiKey)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"api_key": "", "api_key_ciphertext": ciphertext, "credential_version": modelProviderCredentialVersion,
	}, nil
}

// MigrateLegacyAPIKeys encrypts legacy plaintext provider credentials in place.
// It is safe to run at every startup and leaves already encrypted rows untouched.
func MigrateLegacyAPIKeys(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	var rows []orm.UserModelProviderGroup
	if err := db.Where("TRIM(api_key) <> '' AND TRIM(api_key_ciphertext) = ''").Find(&rows).Error; err != nil {
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
