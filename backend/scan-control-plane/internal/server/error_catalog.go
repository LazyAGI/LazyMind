package server

import (
	"net/http"
	"regexp"
	"strings"
	"unicode"
)

type errorCatalogEntry struct {
	Code    string
	Message string
	Status  int
}

type errorCatalogPattern struct {
	Template string
	Matcher  *regexp.Regexp
	Entry    errorCatalogEntry
}

var (
	errorCatalog       = map[string]errorCatalogEntry{}
	errorCatalogByCode = map[string]errorCatalogEntry{}
	errorPatterns      []errorCatalogPattern
)

func registerError(message, code, canonicalMessage string, status int) {
	key := normalizeErrorMessage(message)
	if _, exists := errorCatalog[key]; exists {
		panic("duplicate scan-control-plane error message: " + key)
	}
	entry := errorCatalogEntry{Code: code, Message: canonicalMessage, Status: status}
	registerErrorCode(entry)
	errorCatalog[key] = entry
}

func registerErrorPattern(template, code, canonicalMessage string, status int) {
	normalized := normalizeErrorMessage(template)
	for _, pattern := range errorPatterns {
		if pattern.Template == normalized {
			panic("duplicate scan-control-plane error pattern: " + normalized)
		}
	}
	entry := errorCatalogEntry{Code: code, Message: canonicalMessage, Status: status}
	registerErrorCode(entry)
	errorPatterns = append(errorPatterns, errorCatalogPattern{
		Template: normalized,
		Matcher:  compileErrorPattern(normalized),
		Entry:    entry,
	})
}

func registerErrorCode(entry errorCatalogEntry) {
	if previous, exists := errorCatalogByCode[entry.Code]; exists {
		if previous.Message != entry.Message || previous.Status != entry.Status {
			panic("scan-control-plane error code maps to multiple meanings: " + entry.Code)
		}
		return
	}
	errorCatalogByCode[entry.Code] = entry
}

func resolveErrorPayload(defaultCode, message string, details map[string]any) (string, string, map[string]any) {
	cleaned := stripLeadingErrorCode(strings.TrimSpace(message))
	if entry, detail, ok := lookupCatalogEntry(cleaned); ok {
		if detail != "" {
			details = copyDetails(details)
			if _, exists := details["detail"]; !exists {
				details["detail"] = detail
			}
		}
		return entry.Code, entry.Message, detailsOrEmpty(details)
	}
	if defaultCode == "INTERNAL_ERROR" {
		return defaultCode, "internal error", detailsOrEmpty(details)
	}
	return defaultCode, cleaned, detailsOrEmpty(details)
}

func lookupCatalogEntry(message string) (errorCatalogEntry, string, bool) {
	normalized := normalizeErrorMessage(message)
	if entry, exists := errorCatalog[normalized]; exists {
		return entry, "", true
	}
	for _, pattern := range errorPatterns {
		if pattern.Matcher.MatchString(normalized) {
			return pattern.Entry, strings.TrimSpace(message), true
		}
	}
	if index := strings.Index(message, ": "); index > 0 {
		base := strings.TrimSpace(message[:index])
		detail := strings.TrimSpace(message[index+2:])
		if entry, exists := errorCatalog[normalizeErrorMessage(base)]; exists {
			return entry, detail, true
		}
		for _, pattern := range errorPatterns {
			if pattern.Matcher.MatchString(normalizeErrorMessage(base)) {
				return pattern.Entry, detail, true
			}
		}
	}
	return errorCatalogEntry{}, "", false
}

func stripLeadingErrorCode(message string) string {
	index := strings.Index(message, ": ")
	if index <= 0 {
		return message
	}
	prefix := message[:index]
	for _, r := range prefix {
		if r != '_' && !unicode.IsUpper(r) {
			return message
		}
	}
	return strings.TrimSpace(message[index+2:])
}

func normalizeErrorMessage(message string) string {
	return strings.ToLower(strings.TrimSpace(message))
}

func copyDetails(details map[string]any) map[string]any {
	copy := make(map[string]any, len(details)+1)
	for key, value := range details {
		copy[key] = value
	}
	return copy
}

func compileErrorPattern(template string) *regexp.Regexp {
	var pattern strings.Builder
	pattern.WriteString("^")
	for index := 0; index < len(template); {
		percent := strings.IndexByte(template[index:], '%')
		if percent < 0 {
			pattern.WriteString(regexp.QuoteMeta(template[index:]))
			break
		}
		percent += index
		pattern.WriteString(regexp.QuoteMeta(template[index:percent]))
		if percent+1 < len(template) && template[percent+1] == '%' {
			pattern.WriteString("%")
			index = percent + 2
			continue
		}
		verb := percent + 1
		for verb < len(template) && !unicode.IsLetter(rune(template[verb])) {
			verb++
		}
		if verb >= len(template) {
			pattern.WriteString("%")
			index = percent + 1
			continue
		}
		pattern.WriteString(".+?")
		index = verb + 1
	}
	pattern.WriteString("$")
	return regexp.MustCompile(pattern.String())
}

func catalogStatus(code string) (int, bool) {
	entry, exists := errorCatalogByCode[code]
	if !exists {
		return http.StatusInternalServerError, false
	}
	return entry.Status, true
}
