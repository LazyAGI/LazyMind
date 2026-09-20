package runtimeidentity

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

const aliasesExtKey = "runtime_aliases"

// NormalizeBindingName returns the stable comparison form used at the chat
// boundary. Runtime names are still emitted exactly as stored; normalization
// only makes human-entered bindings tolerant of case and separator variants.
func NormalizeBindingName(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "/")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		segment := normalizeSegment(part)
		if segment == "" {
			return ""
		}
		normalized = append(normalized, segment)
	}
	return strings.Join(normalized, "/")
}

func normalizeSegment(value string) string {
	var builder strings.Builder
	pendingSeparator := false
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsSpace(r) || r == '_' || r == '-' {
			pendingSeparator = builder.Len() > 0
			continue
		}
		if pendingSeparator {
			builder.WriteByte('-')
			pendingSeparator = false
		}
		builder.WriteRune(unicode.ToLower(r))
	}
	return builder.String()
}

func MergeAliases(raw []byte, aliases ...string) ([]byte, []string, error) {
	ext := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &ext); err != nil {
			return nil, nil, fmt.Errorf("decode skill runtime identity: %w", err)
		}
	}
	if ext == nil {
		ext = map[string]any{}
	}
	merged := aliasesFromValue(ext[aliasesExtKey])
	merged = append(merged, aliases...)
	merged = compact(merged)
	if len(merged) == 0 {
		return raw, nil, nil
	}
	ext[aliasesExtKey] = merged
	encoded, err := json.Marshal(ext)
	if err != nil {
		return nil, nil, fmt.Errorf("encode skill runtime identity: %w", err)
	}
	return encoded, merged, nil
}

func Aliases(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	ext := map[string]any{}
	if err := json.Unmarshal(raw, &ext); err != nil {
		return nil, fmt.Errorf("decode skill runtime identity: %w", err)
	}
	return compact(aliasesFromValue(ext[aliasesExtKey])), nil
}

func aliasesFromValue(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if alias, ok := value.(string); ok {
				out = append(out, alias)
			}
		}
		return out
	default:
		return nil
	}
}

func compact(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
