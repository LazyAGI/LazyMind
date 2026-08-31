package source

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// sanitizePathPart mirrors doc.safePathPart so generated display names use the
// same portable filesystem rules as uploaded documents.
func sanitizePathPart(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "..", "")
	value = strings.ReplaceAll(value, "\\", "/")
	value = strings.Trim(value, "/")
	if value == "" {
		return "document"
	}
	replacer := strings.NewReplacer(
		"/", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	return replacer.Replace(value)
}

// shortHash returns a compact hex digest used to make generated file names
// stable and collision-resistant.
func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:4])
}

// anyRegexpMatch reports whether value matches any regular expression.
func anyRegexpMatch(patterns []*regexp.Regexp, value string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func compilePatterns(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		out = append(out, regexp.MustCompile(pattern))
	}
	return out
}
