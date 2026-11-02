package modelconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriterSelectedConnectionNeverLoadsAnotherAccount(t *testing.T) {
	var tokenRequests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/authservice/v1/cloud/connections/internal/chat-enabled" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{
				map[string]any{"connection_id": "one", "owner_user_id": "owner", "provider": "github", "status": "ACTIVE"},
				map[string]any{"connection_id": "two", "owner_user_id": "owner", "provider": "github", "status": "ACTIVE"},
			}}})
			return
		}
		tokenRequests = append(tokenRequests, r.URL.Path)
		if r.URL.Path != "/api/authservice/v1/cloud/connections/two/token" {
			t.Errorf("wrong account request %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"connection_id": "two", "provider": "github", "status": "ACTIVE", "access_token": "fake-test-only-token"}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
	if _, err := LoadWriterProviderConnectionConfig(t.Context(), "github", "owner", "two"); err != nil {
		t.Fatal(err)
	}
	if len(tokenRequests) != 1 {
		t.Fatal("loaded extra credentials")
	}
	if _, err := LoadWriterProviderConnectionConfig(t.Context(), "github", "owner", "foreign"); err == nil {
		t.Fatal("unknown account accepted")
	}
	if len(tokenRequests) != 1 {
		t.Fatal("unknown account fell back to another credential")
	}
}
