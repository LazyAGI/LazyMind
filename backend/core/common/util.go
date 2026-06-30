package common

import (
	"encoding/hex"

	"github.com/google/uuid"
)

// GenerateID returns a random 32-char hex id (UUID v4, no dashes). Each call is independent.
func GenerateID() string {
	u := uuid.New()
	return hex.EncodeToString(u[:])
}

// GeneratePrefixedID returns a prefixed ID that fits within maxLen characters.
// It truncates the random suffix so that len(prefix)+len(suffix) <= maxLen.
// Panics if maxLen <= len(prefix) (invalid usage).
func GeneratePrefixedID(prefix string, maxLen int) string {
	u := uuid.New()
	full := hex.EncodeToString(u[:])
	suffixLen := maxLen - len(prefix)
	if suffixLen <= 0 {
		panic("GeneratePrefixedID: maxLen too small for prefix")
	}
	if suffixLen > len(full) {
		suffixLen = len(full)
	}
	return prefix + full[:suffixLen]
}
