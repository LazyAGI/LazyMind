package cloudsession

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// System storage is the default. Encrypted-file and nonpersistent memory stores
// require explicit selection; a system-store failure never enables a fallback.

func NewSystemSecureTokenStore(cloudIssuer string) SecureTokenStore {
	return newSystemSecureTokenStore("com.lazymind.desktop.cloud", systemSecureTokenAccount(cloudIssuer))
}

func systemSecureTokenAccount(cloudIssuer string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(cloudIssuer)))
	return "refresh:" + hex.EncodeToString(sum[:8])
}
