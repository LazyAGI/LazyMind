package bootstrap

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVocabularyVerifierRequiresServiceCredentialsAndLiveUser(t *testing.T) {
	lookups := 0
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		lookups++
		if request.Header.Get("X-LazyMind-Internal-Token") != "internal-token" {
			t.Error("identity lookup lost service credentials")
		}
		switch request.URL.Path {
		case "/api/authservice/user/alice/identity/internal", "/api/authservice/user/bob/identity/internal":
			user := "alice"
			if request.URL.Path == "/api/authservice/user/bob/identity/internal" {
				user = "bob"
			}
			_, _ = fmt.Fprintf(w, `{"sub":%q,"tenant_id":"tenant","permissions":["qa.read"]}`, user)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer authServer.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", authServer.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-token")
	verify := vocabularyVerifier("internal-token")
	for _, user := range []string{"alice", "bob"} {
		request := httptest.NewRequest(http.MethodPost, "/internal/mcp/vocabulary", nil)
		request.Header.Set("X-User-Id", user)
		identity, err := verify(t.Context(), "internal-token", request)
		if err != nil || identity.UserID != user || identity.Extra["tenant_id"] != "tenant" || len(identity.Scopes) != 1 || identity.Scopes[0] != "qa.read" {
			t.Fatalf("user=%s identity=%+v error=%v", user, identity, err)
		}
		if _, err := verify(t.Context(), "browser-token", request); err == nil {
			t.Fatal("browser credentials accepted at the internal endpoint")
		}
	}
	if lookups != 2 {
		t.Fatalf("untrusted requests reached identity lookup: %d", lookups)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/mcp/vocabulary", nil)
	request.Header.Set("X-User-Id", "disabled-user")
	if _, err := verify(t.Context(), "internal-token", request); err == nil {
		t.Fatal("disabled account accepted")
	}
}
