package credentialvault

import (
	"encoding/json"
	"errors"
	"strings"
)

const MaximumRequestAdvancedLength = 6 * 1024

// ProviderRequestAdvanced is portable only inside an encrypted credential
// payload. Its values can contain private gateway credentials.
type ProviderRequestAdvanced struct {
	Headers   map[string]string `json:"headers,omitempty"`
	Query     map[string]string `json:"query,omitempty"`
	ExtraBody map[string]any    `json:"extra_body,omitempty" freeform:"true"`
}

func (config ProviderRequestAdvanced) Empty() bool {
	return len(config.Headers)+len(config.Query)+len(config.ExtraBody) == 0
}

// ValidateProviderRequestAdvanced never includes a user value in its errors.
func ValidateProviderRequestAdvanced(config ProviderRequestAdvanced) error {
	invalid := errors.New("invalid advanced request configuration")
	encoded, err := json.Marshal(config)
	if err != nil || len(encoded) > MaximumRequestAdvancedLength || len(config.Headers) > 32 || len(config.Query) > 32 || len(config.ExtraBody) > 64 {
		return invalid
	}
	seen := map[string]bool{}
	for key, value := range config.Headers {
		lower := strings.ToLower(key)
		if !validRequestToken(key) || seen[lower] || len(value) > 2048 || !validRequestHeaderValue(value) {
			return invalid
		}
		seen[lower] = true
		switch lower {
		case "authorization", "proxy-authorization", "x-api-key", "host", "content-type", "content-length", "accept-encoding", "anthropic-version", "connection", "transfer-encoding", "te", "trailer", "upgrade":
			return errors.New("authentication, protocol and transport headers are managed by LazyMind")
		}
	}
	for key, value := range config.Query {
		if strings.TrimSpace(key) != key || key == "" || len(key) > 128 || len(value) > 2048 || strings.ContainsAny(key+value, "\r\n\x00") {
			return invalid
		}
	}
	for key, value := range config.ExtraBody {
		if key == "" || len(key) > 128 || !validRequestJSONDepth(value, 0) {
			return invalid
		}
		switch strings.ToLower(key) {
		case "model", "messages", "input", "prompt", "query", "documents", "stream", "file", "files", "api_key", "apikey", "authorization", "encoding_format", "dimensions":
			return errors.New("model, input, output format and streaming fields are managed by LazyMind")
		}
	}
	return nil
}

func validRequestToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}

func validRequestHeaderValue(value string) bool {
	for _, c := range value {
		if c < 32 && c != '\t' || c > 126 {
			return false
		}
	}
	return true
}

func validRequestJSONDepth(value any, depth int) bool {
	if depth > 10 {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if !validRequestJSONDepth(child, depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range typed {
			if !validRequestJSONDepth(child, depth+1) {
				return false
			}
		}
	}
	return true
}
