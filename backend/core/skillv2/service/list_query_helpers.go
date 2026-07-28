package service

import (
	"encoding/json"
	"strings"

	"gorm.io/gorm"
)

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func jsonString(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func jsonStringContent(value string) string {
	encoded := jsonString(value)
	return strings.TrimSuffix(strings.TrimPrefix(encoded, `"`), `"`)
}

func jsonStringElementLikePattern(value string) string {
	return `%"` + escapeLike(jsonStringContent(value)) + `"%`
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `!`, `!!`)
	value = strings.ReplaceAll(value, `%`, `!%`)
	value = strings.ReplaceAll(value, `_`, `!_`)
	return value
}

func tagsTextExpr(db *gorm.DB) string {
	if db != nil && db.Dialector != nil {
		switch db.Dialector.Name() {
		case "mysql":
			return "CAST(tags AS CHAR)"
		case "postgres":
			return "tags::text"
		}
	}
	return "CAST(tags AS TEXT)"
}
