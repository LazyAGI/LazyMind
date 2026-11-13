package workbuddy

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// AuthenticationResult contains only safe local authentication evidence.
// LoginRequired excludes inspection failures: those must not trigger a forced
// login or be presented as a confirmed logout.
type AuthenticationResult struct {
	Ready         bool
	LoginRequired bool
	Message       string
}

func AuthenticationStatus() AuthenticationResult {
	return authenticationStatus(authFile())
}

func authenticationStatus(path string) AuthenticationResult {
	ready, reason := availability(path)
	return AuthenticationResult{
		Ready:         ready,
		LoginRequired: reason == authSignedOut || reason == authExpired,
		Message:       reason,
	}
}

const (
	authSignedOut  = "WorkBuddy is not signed in; open WorkBuddy and complete sign-in"
	authExpired    = "WorkBuddy sign-in has expired; open WorkBuddy and sign in again"
	authUnreadable = "WorkBuddy sign-in status could not be read; open WorkBuddy and check its account status"
	maxAuthBytes   = 1 << 20
)

// WorkBuddy 5.9.3's FileAuthenticationStorage and bundled CLI share this
// product-scoped session. On Windows it is in Local, not Go's Roaming
// UserConfigDir. Never fall back to another user's or product's credentials.
func authFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return authFileForPlatform(runtime.GOOS, home)
}

func authFileForPlatform(platform, home string) string {
	var root string
	switch platform {
	case "windows":
		root = filepath.Join(home, "AppData", "Local")
	case "darwin":
		root = filepath.Join(home, "Library", "Application Support")
	default:
		root = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(root, "CodeBuddyExtension", "Data", "Public", "auth", "workbuddy-desktop.info")
}

// This is a local session availability check, not a server authorization
// decision. WorkBuddy owns credential decryption and refresh. No credential
// values, account identities or parser errors are returned to callers/logs.
func availability(path string) (bool, string) {
	if path == "" {
		return false, authUnreadable
	}
	if _, err := os.Stat(path + ".logged-out"); err == nil {
		return false, authSignedOut
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, authUnreadable
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, authSignedOut
	}
	if err != nil {
		return false, authUnreadable
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxAuthBytes+1))
	if err != nil || len(body) > maxAuthBytes {
		return false, authUnreadable
	}
	var session struct {
		Account struct {
			UID json.RawMessage `json:"uid"`
		} `json:"account"`
		Auth struct {
			AccessToken      json.RawMessage `json:"accessToken"`
			RefreshToken     json.RawMessage `json:"refreshToken"`
			ExpiresAt        int64           `json:"expiresAt"`
			RefreshExpiresAt int64           `json:"refreshExpiresAt"`
		} `json:"auth"`
	}
	if json.Unmarshal(body, &session) != nil {
		return false, authUnreadable
	}
	if !knownCredentialEncoding(session.Auth.AccessToken) || !knownCredentialEncoding(session.Auth.RefreshToken) {
		return false, authUnreadable
	}
	accountPresent, accountValid := accountIdentity(session.Account.UID)
	if !accountValid {
		return false, authUnreadable
	}
	if !accountPresent || !hasCredential(session.Auth.AccessToken) {
		return false, authSignedOut
	}
	now := time.Now().UnixMilli()
	if session.Auth.ExpiresAt > 0 && session.Auth.ExpiresAt <= now &&
		(!hasCredential(session.Auth.RefreshToken) || session.Auth.RefreshExpiresAt > 0 && session.Auth.RefreshExpiresAt <= now) {
		return false, authExpired
	}
	return true, ""
}

// The desktop session serializer preserves account IDs from the provider;
// accept string and numeric IDs without turning numeric IDs into parse errors.
func accountIdentity(raw json.RawMessage) (present, valid bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return false, true
	}
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return strings.TrimSpace(id) != "", true
	}
	var number float64
	if json.Unmarshal(raw, &number) == nil && number >= 0 && math.Trunc(number) == number {
		return number > 0, true
	}
	return false, false
}

func knownCredentialEncoding(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var plain string
	return json.Unmarshal(raw, &plain) == nil || hasCredential(raw)
}

// At-rest protection can wrap tokens. Recognize the client's documented-in-
// bundle wrapper shapes without decrypting them or mistaking them for logout.
func hasCredential(raw json.RawMessage) bool {
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return strings.TrimSpace(plain) != ""
	}
	var wrapper map[string]json.RawMessage
	if json.Unmarshal(raw, &wrapper) != nil {
		return false
	}
	var version int
	var envelope, scheme string
	if json.Unmarshal(wrapper["$wbEncrypted"], &version) != nil || version != 1 ||
		json.Unmarshal(wrapper["envelope"], &envelope) != nil || strings.TrimSpace(envelope) == "" {
		return false
	}
	if len(wrapper) == 2 {
		return true
	}
	return len(wrapper) == 3 && json.Unmarshal(wrapper["scheme"], &scheme) == nil && scheme == "asym-v1"
}
