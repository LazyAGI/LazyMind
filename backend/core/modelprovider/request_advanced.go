package modelprovider

import (
	"encoding/json"
	"net/http"
	"strings"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

// ResolveProviderRequestAdvanced decrypts settings for one supplier snapshot.
// Callers must keep the result inside credential-bearing internal contracts.
func ResolveProviderRequestAdvanced(parent orm.UserModelProvider) (credentialvault.ProviderRequestAdvanced, error) {
	var stored savedProviderRequestConfig
	if parent.RequestConfig == "" {
		return credentialvault.ProviderRequestAdvanced{}, nil
	}
	if err := json.Unmarshal([]byte(parent.RequestConfig), &stored); err != nil {
		return credentialvault.ProviderRequestAdvanced{}, credentialvault.ErrInvalidContract
	}
	var advanced credentialvault.ProviderRequestAdvanced
	if stored.AdvancedCiphertext != "" {
		plain, err := decryptModelProviderAPIKeyForGroup(parent.CreateUserID, requestAdvancedAAD(parent.ID), 1, stored.AdvancedCiphertext)
		if err != nil {
			return advanced, err
		}
		if err := json.Unmarshal([]byte(plain), &advanced); err != nil {
			return advanced, credentialvault.ErrInvalidContract
		}
	} else if stored.Advanced != nil {
		// Portable values exist only during an encrypted restore transaction.
		advanced = *stored.Advanced
	}
	return advanced, credentialvault.ValidateProviderRequestAdvanced(advanced)
}

func requestAdvancedAAD(providerID string) string {
	return "request-config:" + strings.TrimSpace(providerID)
}

func encryptRequestAdvanced(parent orm.UserModelProvider, advanced credentialvault.ProviderRequestAdvanced, managers ...*credentialvault.LocalKeyManager) (string, error) {
	if err := credentialvault.ValidateProviderRequestAdvanced(advanced); err != nil {
		return "", err
	}
	if advanced.Empty() {
		return "", nil
	}
	encoded, err := json.Marshal(advanced)
	if err != nil {
		return "", err
	}
	defer clear(encoded)
	manager := credentialKeyManager()
	if len(managers) > 0 {
		manager = managers[0]
	}
	return encryptModelProviderAPIKeyBytes(manager, parent.CreateUserID, requestAdvancedAAD(parent.ID), 1, encoded)
}

func portableProviderRequestConfig(parent orm.UserModelProvider) (string, error) {
	if parent.RequestConfig == "" {
		return "", nil
	}
	var stored savedProviderRequestConfig
	if err := json.Unmarshal([]byte(parent.RequestConfig), &stored); err != nil {
		return "", err
	}
	if stored.AdvancedCiphertext == "" && stored.Advanced == nil {
		if !credentialvault.ValidProviderRequestConfig(parent.RequestConfig) {
			return "", credentialvault.ErrInvalidContract
		}
		return parent.RequestConfig, nil
	}
	advanced, err := ResolveProviderRequestAdvanced(parent)
	if err != nil {
		return "", err
	}
	stored.AdvancedCiphertext, stored.Advanced = "", nil
	if !advanced.Empty() {
		stored.Advanced = &advanced
	}
	encoded, err := json.Marshal(stored)
	if err != nil || !credentialvault.ValidProviderRequestConfig(string(encoded)) {
		return "", credentialvault.ErrInvalidContract
	}
	return string(encoded), nil
}

func encryptRestoredRequestConfig(parent orm.UserModelProvider, raw string, managers ...*credentialvault.LocalKeyManager) (string, error) {
	if raw == "" {
		return "", nil
	}
	if !credentialvault.ValidProviderRequestConfig(raw) {
		return "", credentialvault.ErrInvalidContract
	}
	var stored savedProviderRequestConfig
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return "", err
	}
	if stored.Advanced == nil {
		return raw, nil
	}
	ciphertext, err := encryptRequestAdvanced(parent, *stored.Advanced, managers...)
	if err != nil {
		return "", err
	}
	stored.Advanced, stored.AdvancedCiphertext = nil, ciphertext
	encoded, err := json.Marshal(stored)
	return string(encoded), err
}

func applyProviderRequestAdvanced(req *http.Request, advanced credentialvault.ProviderRequestAdvanced) {
	for key, value := range advanced.Headers {
		req.Header.Set(key, value)
	}
	query := req.URL.Query()
	for key, value := range advanced.Query {
		// Adapter-owned discovery filters take precedence over custom defaults.
		if !query.Has(key) {
			query.Set(key, value)
		}
	}
	req.URL.RawQuery = query.Encode()
}
