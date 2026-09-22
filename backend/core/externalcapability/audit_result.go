package externalcapability

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	maxAuditResultBytes = 16 << 10
	maxAuditStringRunes = 4096
	maxAuditArrayItems  = 50
	maxAuditObjectKeys  = 100
)

var (
	bearerCredentialPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	inlineCredentialPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|password|passwd|secret|credential)\b\s*[:=]\s*["']?[^\s,"'}]+`)
)

// auditResultPreview creates a JSON envelope that is safe to expose to the
// owning user in invocation history. It never receives request bodies, strips
// common credential-bearing fields and values, and caps stored output size.
func auditResultPreview(value any) json.RawMessage {
	if value == nil {
		return json.RawMessage(`{}`)
	}
	normalized := normalizeAuditValue(value)
	envelope, err := json.Marshal(map[string]any{"data": normalized, "truncated": false})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	if len(envelope) <= maxAuditResultBytes {
		return envelope
	}

	encoded, _ := json.Marshal(normalized)
	previewRunes := []rune(string(encoded))
	limit := min(len(previewRunes), maxAuditResultBytes/2)
	for limit > 0 {
		truncated, marshalErr := json.Marshal(map[string]any{
			"preview": string(previewRunes[:limit]) + "…", "truncated": true,
		})
		if marshalErr == nil && len(truncated) <= maxAuditResultBytes {
			return truncated
		}
		limit = limit * 3 / 4
	}
	return json.RawMessage(`{"truncated":true}`)
}

func normalizeAuditValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	return sanitizeAuditValue(decoded)
}

func sanitizeAuditValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, min(len(typed), maxAuditObjectKeys))
		count := 0
		for key, item := range typed {
			if count >= maxAuditObjectKeys {
				result["_lazymind_truncated"] = true
				break
			}
			if auditKeyIsSensitive(key) {
				result[key] = "[redacted]"
			} else {
				result[key] = sanitizeAuditValue(item)
			}
			count++
		}
		return result
	case []any:
		limit := min(len(typed), maxAuditArrayItems)
		result := make([]any, 0, limit+1)
		for _, item := range typed[:limit] {
			result = append(result, sanitizeAuditValue(item))
		}
		if len(typed) > limit {
			result = append(result, map[string]any{"_lazymind_truncated": true})
		}
		return result
	case string:
		redacted := bearerCredentialPattern.ReplaceAllString(typed, "Bearer [redacted]")
		redacted = inlineCredentialPattern.ReplaceAllString(redacted, "$1=[redacted]")
		return truncateRunes(redacted, maxAuditStringRunes)
	default:
		return typed
	}
}

func auditKeyIsSensitive(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	switch normalized {
	case "authorization", "proxyauthorization", "apikey", "token", "accesstoken", "refreshtoken",
		"password", "passwd", "secret", "clientsecret", "credential", "credentials", "cookie",
		"setcookie", "privatekey", "headers":
		return true
	default:
		return strings.HasSuffix(normalized, "apikey") || strings.HasSuffix(normalized, "accesstoken") ||
			strings.HasSuffix(normalized, "refreshtoken") || strings.HasSuffix(normalized, "password") ||
			strings.HasSuffix(normalized, "secret")
	}
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
