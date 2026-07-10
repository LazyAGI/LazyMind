package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lazyagi/lazymind/local_proxy/internal/auth"
	"github.com/lazyagi/lazymind/local_proxy/internal/config"
)

func TestAdminSessionEndpointAllowsLoopback(t *testing.T) {
	t.Parallel()

	handler := testAdminSessionHandler(false)
	req := httptest.NewRequest(http.MethodPost, "/_local/admin-session", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
}

func TestAdminSessionEndpointRejectsLANByDefault(t *testing.T) {
	t.Parallel()

	handler := testAdminSessionHandler(false)
	req := httptest.NewRequest(http.MethodPost, "/_local/admin-session", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "192.168.1.10")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
}

func TestAdminSessionEndpointRejectsSpoofedLoopbackForwardedForFromLAN(t *testing.T) {
	t.Parallel()

	handler := testAdminSessionHandler(false)
	req := httptest.NewRequest(http.MethodPost, "/_local/admin-session", nil)
	req.RemoteAddr = "192.168.1.10:54321"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
}

func TestAdminSessionEndpointUsesRightmostForwardedForFromTrustedProxy(t *testing.T) {
	t.Parallel()

	handler := testAdminSessionHandler(false)
	req := httptest.NewRequest(http.MethodPost, "/_local/admin-session", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "127.0.0.1, 192.168.1.10")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.Code)
	}
}

func TestAdminSessionEndpointAllowsLANWhenConfigured(t *testing.T) {
	t.Parallel()

	handler := testAdminSessionHandler(true)
	req := httptest.NewRequest(http.MethodPost, "/_local/admin-session", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "192.168.1.10")
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
	}
}

func TestAdminSessionEndpointForceRefreshBypassesCachedSession(t *testing.T) {
	t.Parallel()

	loginCalls := 0
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/authservice/auth/login":
					loginCalls++
					_, _ = w.Write([]byte(fmt.Sprintf(`{"access_token":"token-%d","refresh_token":"refresh","role":"system-admin","expires_in":3600}`, loginCalls)))
				case "/api/authservice/auth/me":
					_, _ = w.Write([]byte(`{"user_id":"u-1","username":"admin","role":"system-admin"}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}),
		},
	}
	handler := &adminSessionHandler{
		cfg: config.Config{
			Auth: config.AuthConfig{},
		},
		manager: auth.NewAdminSessionManager("http://auth", &http.Client{Transport: rt}),
	}

	for _, target := range []string{"/_local/admin-session", "/_local/admin-session", "/_local/admin-session?force=true"} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		req.RemoteAddr = "127.0.0.1:54321"
		resp := httptest.NewRecorder()

		handler.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200; body=%s", target, resp.Code, resp.Body.String())
		}
	}

	if loginCalls != 2 {
		t.Fatalf("loginCalls = %d, want 2", loginCalls)
	}
}

func TestAdminSessionEndpointRejectsNonPost(t *testing.T) {
	t.Parallel()

	handler := testAdminSessionHandler(true)
	req := httptest.NewRequest(http.MethodGet, "/_local/admin-session", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.Code)
	}
}

func testAdminSessionHandler(allowLAN bool) http.Handler {
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/authservice/auth/login":
					_, _ = w.Write([]byte(`{"access_token":"token","refresh_token":"refresh","role":"system-admin","expires_in":3600}`))
				case "/api/authservice/auth/me":
					_, _ = w.Write([]byte(`{"user_id":"u-1","username":"admin","role":"system-admin"}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}),
		},
	}
	return &adminSessionHandler{
		cfg: config.Config{
			Auth: config.AuthConfig{
				AutoLoginAllowLAN: allowLAN,
			},
		},
		manager: auth.NewAdminSessionManager("http://auth", &http.Client{Transport: rt}),
	}
}
