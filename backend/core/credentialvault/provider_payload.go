package credentialvault

import (
	"bytes"
	"encoding/json"
)

const MaximumAPIKeyLength = 512

// ValidAPIKeyPool keeps the per-key bound while permitting multiple saved keys
// in the existing newline-separated credential format.
func ValidAPIKeyPool(pool []byte) bool {
	if len(pool) == 0 || len(pool) > MaximumPlaintextLength {
		return false
	}
	count := 0
	for _, line := range bytes.Split(pool, []byte{'\n'}) {
		key := bytes.TrimSpace(line)
		if len(key) == 0 {
			continue
		}
		if len(key) > MaximumAPIKeyLength {
			return false
		}
		count++
	}
	return count > 0
}

func validProviderKeySelection(pool []byte, index *int) bool {
	if index == nil {
		return true
	}
	count := 0
	for _, line := range bytes.Split(pool, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 {
			count++
		}
	}
	return *index >= -1 && *index < count
}

// EncodeProviderCredentialPayload is shared by backup and local pool writes so
// a locally accepted pool always fits the complete encrypted-record payload.
func EncodeProviderCredentialPayload(credential ProviderCredential) ([]byte, error) {
	if !ValidAPIKeyPool(credential.APIKey) || !ValidProviderRequestConfig(credential.RequestConfig) || !validProviderKeySelection(credential.APIKey, credential.ActiveKeyIndex) {
		return nil, ErrInvalidContract
	}
	fields := map[string]any{}
	if credential.RequestConfig != "" {
		fields["request_config"] = json.RawMessage(credential.RequestConfig)
	}
	if credential.ActiveKeyIndex != nil {
		fields["active_key_index"] = *credential.ActiveKeyIndex
	}
	payload, err := json.Marshal(map[string]any{
		"schema_version": 1, "provider_catalog_key": credential.ProviderCatalogKey,
		"display_name": credential.DisplayName, "base_url": credential.BaseURL,
		"credential_type": "api_key", "api_key": string(credential.APIKey),
		"provider_specific_fields": fields,
	})
	if err != nil || len(payload) > MaximumPlaintextLength {
		clear(payload)
		return nil, ErrInvalidContract
	}
	return payload, nil
}
