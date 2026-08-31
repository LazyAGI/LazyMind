package cloudsession

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// SecureTokenStore is intentionally implemented at the platform boundary.
// D1 does not provide a file-based fallback: callers must inject an operating
// system credential-store implementation before enabling Cloud session restore.

func NewSystemSecureTokenStore(cloudIssuer string) SecureTokenStore {
	return newSystemSecureTokenStore("com.lazymind.desktop.cloud", systemSecureTokenAccount(cloudIssuer))
}

func systemSecureTokenAccount(cloudIssuer string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(cloudIssuer)))
	return "refresh:" + hex.EncodeToString(sum[:8])
}
