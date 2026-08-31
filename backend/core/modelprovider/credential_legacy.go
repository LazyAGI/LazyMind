package modelprovider

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"lazymind/core/common/secretcrypto"
)

const legacyModelProviderDefaultSecret = "lazymind-core-model-provider-default-secret"

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
