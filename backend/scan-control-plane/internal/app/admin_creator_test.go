package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
)

func TestAuthServiceResolvesAdminIdentityThroughProtectedLookup(t *testing.T) {
	for _, id := range []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"} {
		t.Run(id, func(t *testing.T) {
			auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/authservice/user/admin/identity/internal" || r.URL.Query().Get("tenant_id") != "tenant-fixture" {
					t.Errorf("unexpected identity lookup: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.Header.Get("X-LazyMind-Internal-Token") != "synthetic-internal-token" || r.Header.Get("Authorization") != "" {
					t.Error("lookup must use internal service authentication")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				payload := map[string]any{"user_id": id, "username": "admin", "tenant_id": "tenant-fixture", "role": "system-admin", "disabled": false}
				if id == "22222222-2222-4222-8222-222222222222" {
					payload = map[string]any{"code": 200, "message": "success", "data": payload}
				}
				_ = json.NewEncoder(w).Encode(payload)
			}))
			defer auth.Close()
			verifier, err := newAuthServiceAdminVerifier(auth.URL, "synthetic-internal-token", auth.Client())
			if err != nil {
				t.Fatal(err)
			}
			resolver, ok := any(verifier).(sourceengine.CreatorResolver)
			if !ok {
				t.Fatal("runtime auth adapter does not implement dynamic admin identity lookup")
			}
			identity, err := resolver.ResolveAdmin(context.Background(), "tenant-fixture")
			if err != nil || identity.UserID != id || identity.UserName != "admin" || identity.TenantID != "tenant-fixture" {
				t.Fatalf("must use identity returned by user service: %+v err=%v", identity, err)
			}
		})
	}
}

func TestAuthServiceAdminLookupRejectsInvalidIdentity(t *testing.T) {
	for _, invalid := range []string{"missing-id", "invalid-id", "wrong-name", "disabled", "wrong-role", "wrong-tenant", "unavailable"} {
		t.Run(invalid, func(t *testing.T) {
			auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				payload := map[string]any{"user_id": "11111111-1111-4111-8111-111111111111", "username": "admin", "tenant_id": "tenant-fixture", "role": "system-admin", "disabled": false}
				switch invalid {
				case "missing-id":
					delete(payload, "user_id")
				case "invalid-id":
					payload["user_id"] = "invalid"
				case "wrong-name":
					payload["username"] = "another-admin"
				case "disabled":
					payload["disabled"] = true
				case "wrong-role":
					payload["role"] = "user"
				case "wrong-tenant":
					payload["tenant_id"] = "other-tenant"
				case "unavailable":
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_ = json.NewEncoder(w).Encode(payload)
			}))
			defer auth.Close()
			verifier, err := newAuthServiceAdminVerifier(auth.URL, "synthetic-internal-token", auth.Client())
			if err != nil {
				t.Fatal(err)
			}
			resolver, ok := any(verifier).(sourceengine.CreatorResolver)
			if !ok {
				t.Fatal("runtime auth adapter does not implement dynamic admin identity lookup")
			}
			if _, err := resolver.ResolveAdmin(context.Background(), "tenant-fixture"); err == nil {
				t.Fatal("invalid or unavailable identity must not fall back to another creator")
			}
		})
	}
}
