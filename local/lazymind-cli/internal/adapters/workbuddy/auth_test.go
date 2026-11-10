package workbuddy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAvailabilityValidatesDesktopSessionAndLogoutMarker(t *testing.T) {
	now := time.Now().UnixMilli()
	for _, tc := range []struct {
		name, body, reason string
		ready, loggedOut   bool
	}{
		{name: "garbage", body: "authenticated", reason: "could not be read"},
		{name: "empty object", body: "{}", reason: "not signed in"},
		{name: "missing account", body: `{"auth":{"accessToken":"fixture-token"}}`, reason: "not signed in"},
		{name: "missing token", body: `{"account":{"uid":"fixture-user"},"auth":{}}`, reason: "not signed in"},
		{name: "valid", body: sessionFixture(t, now+60000, 0), ready: true},
		{name: "numeric account ID", body: `{"account":{"uid":123456789},"auth":{"accessToken":"fixture-token"}}`, ready: true},
		{name: "invalid account ID", body: `{"account":{"uid":{}},"auth":{"accessToken":"fixture-token"}}`, reason: "could not be read"},
		{name: "expired", body: sessionFixture(t, now-60000, now-1000), reason: "expired"},
		{name: "refreshable", body: sessionFixture(t, now-60000, now+60000), ready: true},
		{name: "logged out with residual session", body: sessionFixture(t, now+60000, 0), loggedOut: true, reason: "not signed in"},
		{name: "encrypted", body: `{"account":{"uid":"fixture-user"},"auth":{"accessToken":{"$wbEncrypted":1,"envelope":"fixture-envelope"}}}`, ready: true},
		{name: "asymmetric encrypted", body: `{"account":{"uid":"fixture-user"},"auth":{"accessToken":{"$wbEncrypted":1,"scheme":"asym-v1","envelope":"fixture-envelope"}}}`, ready: true},
		{name: "unknown token object", body: `{"account":{"uid":"fixture-user"},"auth":{"accessToken":{"invalid":true}}}`, reason: "could not be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "workbuddy-desktop.info")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.loggedOut {
				if err := os.WriteFile(path+".logged-out", []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ready, reason := availability(path)
			if ready != tc.ready || !strings.Contains(reason, tc.reason) {
				t.Fatalf("availability=(%v, %q), want ready=%v reason containing %q", ready, reason, tc.ready, tc.reason)
			}
			if strings.Contains(reason, "fixture-token") || strings.Contains(reason, "fixture-user") {
				t.Fatal("status leaked credential data")
			}
		})
	}
}

func TestAuthenticationStatusOnlyRequiresLoginForConfirmedSignedOutOrExpired(t *testing.T) {
	now := time.Now().UnixMilli()
	for _, tc := range []struct {
		name, body           string
		ready, loginRequired bool
	}{
		{name: "missing", loginRequired: true},
		{name: "valid", body: sessionFixture(t, now+60000, 0), ready: true},
		{name: "expired", body: sessionFixture(t, now-60000, now-1000), loginRequired: true},
		{name: "unreadable format", body: "incomplete session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "workbuddy-desktop.info")
			if tc.body != "" {
				if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got := authenticationStatus(path)
			if got.Ready != tc.ready || got.LoginRequired != tc.loginRequired {
				t.Fatalf("status=%+v", got)
			}
			if !got.Ready && got.Message == "" {
				t.Fatal("missing actionable reason")
			}
		})
	}
}

func TestAuthFileUsesNativeProductDataDirectory(t *testing.T) {
	home := t.TempDir()
	for platform, root := range map[string]string{
		"windows": filepath.Join(home, "AppData", "Local"),
		"darwin":  filepath.Join(home, "Library", "Application Support"),
	} {
		want := filepath.Join(root, "CodeBuddyExtension", "Data", "Public", "auth", "workbuddy-desktop.info")
		if got := authFileForPlatform(platform, home); got != want {
			t.Fatalf("%s: got %q want %q", platform, got, want)
		}
	}
}

func TestAvailabilityDoesNotCallUnreadableStateSignedOut(t *testing.T) {
	if ready, reason := availability(t.TempDir()); ready || !strings.Contains(reason, "could not be read") {
		t.Fatalf("directory availability=(%v, %q)", ready, reason)
	}
}

func sessionFixture(t *testing.T, expiresAt, refreshExpiresAt int64) string {
	t.Helper()
	auth := map[string]any{"accessToken": "fixture-token", "expiresAt": expiresAt}
	if refreshExpiresAt != 0 {
		auth["refreshToken"] = "fixture-refresh"
		auth["refreshExpiresAt"] = refreshExpiresAt
	}
	body, err := json.Marshal(map[string]any{"account": map[string]string{"uid": "fixture-user"}, "auth": auth})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
