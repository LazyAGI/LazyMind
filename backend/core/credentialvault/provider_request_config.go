package credentialvault

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
)

// ValidProviderRequestConfig bounds the optional encrypted supplier metadata.
// Effective protocol is still carried by ProviderCatalogKey for old restores.
func ValidProviderRequestConfig(raw string) bool {
	if raw == "" {
		return true
	}
	if len(raw) > 8192 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var config struct {
		DefaultsVersion int                      `json:"defaults_version,omitempty"`
		DefaultProtocol string                   `json:"default_protocol"`
		Endpoints       map[string]string        `json:"endpoints"`
		Defaults        map[string]string        `json:"defaults"`
		Advanced        *ProviderRequestAdvanced `json:"advanced,omitempty"`
	}
	if decoder.Decode(&config) != nil {
		return false
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return false
	}
	if config.DefaultProtocol != "openai" && config.DefaultProtocol != "anthropic" {
		return false
	}
	if config.DefaultsVersion < 0 || config.DefaultsVersion > 1 {
		return false
	}
	if config.Advanced != nil && ValidateProviderRequestAdvanced(*config.Advanced) != nil {
		return false
	}
	for _, pair := range []struct {
		values   map[string]string
		defaults bool
	}{{config.Endpoints, false}, {config.Defaults, true}} {
		for key, value := range pair.values {
			if key != "openai" && key != "anthropic" && !(pair.defaults && (key == "gemini" || key == "openai-responses")) {
				return false
			}
			if value == "" {
				continue
			}
			parsed, err := url.Parse(value)
			if err != nil || len(value) > 1024 || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.TrimSpace(value) != value {
				return false
			}
		}
	}
	return true
}
