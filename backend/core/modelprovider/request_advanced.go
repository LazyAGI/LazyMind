package modelprovider

import (
	"encoding/json"
	"net/http"
	"strings"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
)

// ResolveProviderRequestAdvanced reads plaintext options and supports legacy
// encrypted options until the supplier configuration is explicitly saved again.
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
		// New saves and restored settings use plaintext JSON.
		advanced = *stored.Advanced
	}
	return advanced, credentialvault.ValidateProviderRequestAdvanced(advanced)
}

func requestAdvancedAAD(providerID string) string {
	return "request-config:" + strings.TrimSpace(providerID)
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

// Restored options are already inside an authenticated backup payload. Local
// request options do not require an additional encryption layer.
func restoredRequestConfig(raw string) (string, error) {
	if !credentialvault.ValidProviderRequestConfig(raw) {
		return "", credentialvault.ErrInvalidContract
	}
	return raw, nil
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
