package common

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetCurrentIdentityUsesCurrentAuthenticatedAccount(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		allowed    bool
	}{
		{"plain", `{"sub":"owner","tenant_id":"company","role":"user","permissions":["qa.read"]}`, 200, true},
		{"envelope", `{"data":{"sub":"owner","tenant_id":"company","role":"user","permissions":["qa.read"]}}`, 200, true},
		{"wrong owner", `{"sub":"another","tenant_id":"company"}`, 200, false},
		{"empty", `{}`, 200, false},
		{"disabled", `{}`, 403, false},
		{"unavailable", `{}`, 503, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/authservice/user/owner/identity/internal" || r.Header.Get("X-LazyMind-Internal-Token") != "service-token" {
					t.Error("incorrect identity lookup")
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			t.Cleanup(auth.Close)
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", auth.URL)
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "service-token")
			identity, err := GetCurrentIdentity(t.Context(), "owner")
			if (err == nil) != test.allowed {
				t.Fatalf("identity=%+v error=%v", identity, err)
			}
			if test.allowed && (identity.Subject != "owner" || identity.TenantID != "company" || identity.Role != "user" || len(identity.Permissions) != 1 || identity.Permissions[0] != "qa.read") {
				t.Fatalf("incomplete account identity: %+v", identity)
			}
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "")
			if _, err := GetCurrentIdentity(t.Context(), "owner"); err == nil {
				t.Fatal("missing service credentials were accepted")
			}
		})
	}
}
