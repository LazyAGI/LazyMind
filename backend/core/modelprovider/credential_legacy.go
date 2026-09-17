package modelprovider

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"lazymind/core/common/secretcrypto"
)

const legacyModelProviderDefaultSecret = "lazymind-core-model-provider-default-secret"

const legacyModelProviderCredentialVersion = 1

// Personal BYO credentials retain the main-branch storage contract. Cloud
// integration must not force existing users to migrate into OS-backed V2.
func encodeLegacyModelProviderCiphertext(apiKey string) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", nil
	}
	raw, err := secretcrypto.EncodeAESGCM([]byte(apiKey), legacyModelProviderEncryptionKey())
	return string(raw), err
}

func legacyModelProviderEncryptionKey() string {
	if key := strings.TrimSpace(os.Getenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY")); key != "" {
		return key
	}
	return legacyModelProviderDefaultSecret
}

func decodeLegacyModelProviderCiphertext(ciphertext string) (string, error) {
	decoded, ok, err := secretcrypto.DecodeAESGCM(json.RawMessage(ciphertext), legacyModelProviderEncryptionKey())
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("unsupported model provider credential ciphertext")
	}
	return string(decoded), nil
}
