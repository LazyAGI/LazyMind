package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestZoteroLocalSourceSharedDeploymentRejectedBeforeNetworking(t *testing.T) {
	paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RUNTIME_MODE", "")
	t.Setenv("LAZYMIND_BOOTSTRAP_ADMIN_USERNAME", "owner")
	previous := http.DefaultTransport
	calls := 0
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, localURL := range []string{"", "http://localhost:23119/api", "http://127.0.0.1:23119/api", "http://host.docker.internal:23119/api"} {
		for _, source := range []string{"local", " LOCAL "} {
			r := httptest.NewRequest("POST", "/academic/zotero/preview?target_dataset_id=papers", strings.NewReader(fmt.Sprintf(`{"source":%q,"local_url":%q}`, source, localURL)))
			r.Header.Set("X-User-Id", "owner")
			r.Header.Set("X-User-Role", "system-admin")
			r.Header.Set("Authorization", "Bearer forged-owner")
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			w := httptest.NewRecorder()
			_, accepted := readZoteroRequest(w, r)
			if accepted || w.Code != http.StatusForbidden || calls != 0 {
				t.Fatalf("shared deployment read host-local source: accepted=%t status=%d network_calls=%d body=%s", accepted, w.Code, calls, w.Body.String())
			}
		}
	}
}

func TestZoteroLocalSourceRequiresVerifiedBootstrapOwner(t *testing.T) {
	paperLibraryTestDB(t)
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_BOOTSTRAP_ADMIN_USERNAME", "desktop-owner")
	for _, tc := range []struct {
		name, token, identity string
		status                int
		accepted              bool
	}{
		{"owner", "owner-token", `{"user_id":"owner","username":"desktop-owner","role":"system-admin"}`, 200, true},
		{"enveloped owner", "owner-token", `{"code":200,"data":{"user_id":"owner","username":"desktop-owner","role":"system-admin"}}`, 200, true},
		{"ordinary account", "ordinary-token", `{"user_id":"owner","username":"ordinary","role":"user"}`, 403, false},
		{"another administrator", "other-admin-token", `{"user_id":"owner","username":"other-admin","role":"system-admin"}`, 403, false},
		{"forged caller identity", "other-user-token", `{"user_id":"different-user","username":"desktop-owner","role":"system-admin"}`, 403, false},
		{"demoted owner", "old-owner-token", `{"user_id":"owner","username":"desktop-owner","role":"user"}`, 403, false},
		{"no bearer", "", ``, 403, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/authservice/auth/me" || r.Header.Get("Authorization") != "Bearer "+tc.token || r.Header.Get("X-User-Id") != "" || r.Header.Get("X-User-Role") != "" {
					t.Errorf("owner identity was not verified independently: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tc.identity)
			}))
			defer auth.Close()
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", auth.URL)
			// Keep catalog/scan fixtures synthetic while sending the actual auth
			// HTTP request to a local test listener.
			previous := http.DefaultTransport
			transport := &http.Transport{}
			defer transport.CloseIdleConnections()
			http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == strings.TrimPrefix(auth.URL, "http://") {
					return transport.RoundTrip(r)
				}
				return previous.RoundTrip(r)
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			r := httptest.NewRequest("POST", "/academic/zotero/preview?target_dataset_id=papers", strings.NewReader(`{"source":" LOCAL "}`))
			r.Header.Set("X-User-Id", "owner")
			r.Header.Set("X-User-Name", "desktop-owner")
			r.Header.Set("X-User-Role", "system-admin")
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			config, accepted := readZoteroRequest(w, r)
			if accepted != tc.accepted || w.Code != tc.status || (tc.token != "" && calls != 1) || (tc.token == "" && calls != 0) {
				t.Fatalf("local owner policy: accepted=%t status=%d auth_calls=%d body=%s", accepted, w.Code, calls, w.Body.String())
			}
			if accepted && config.Source != "local" {
				t.Fatalf("source escaped normalization: %q", config.Source)
			}
		})
	}
}

func TestZoteroLocalSourceAuthenticationFailsClosed(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_BOOTSTRAP_ADMIN_USERNAME", "desktop-owner")
	for _, tc := range []struct {
		name, body string
		upstream   int
		status     int
	}{
		{"expired bearer", `{"detail":"private authentication details"}`, 401, 403},
		{"auth unavailable", `{"detail":"private authentication details"}`, 500, 503},
		{"invalid response", `not JSON; secret-token`, 200, 503},
		{"missing identity", `{}`, 200, 503},
		{"failed envelope", `{"code":500,"data":{"user_id":"owner","username":"desktop-owner","role":"system-admin"}}`, 200, 503},
		{"redirect", `private authentication details`, 302, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/must-not-follow")
				if r.URL.Path == "/must-not-follow" {
					t.Fatal("owner token followed an auth-service redirect")
				}
				w.WriteHeader(tc.upstream)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
			r := httptest.NewRequest("POST", "/academic/zotero/preview?target_dataset_id=papers", strings.NewReader(`{"source":"local"}`))
			r.Header.Set("X-User-Id", "owner")
			r.Header.Set("Authorization", "Bearer secret-token")
			w := httptest.NewRecorder()
			_, accepted := readZoteroRequest(w, r)
			if accepted || w.Code != tc.status || strings.Contains(w.Body.String(), "secret-token") || strings.Contains(w.Body.String(), "private authentication details") {
				t.Fatalf("unsafe owner authentication failure: accepted=%t status=%d body=%s", accepted, w.Code, w.Body.String())
			}
		})
	}
}

func TestZoteroHandlersRejectSharedLocalSourceBeforeNetworking(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "")
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("shared local Zotero rejection performed network access")
		return nil, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, handler := range []http.HandlerFunc{PreviewZoteroLibrary, DownloadZoteroAttachment} {
		r := httptest.NewRequest("POST", "/academic/zotero/preview?target_dataset_id=papers", strings.NewReader(`{"source":"local","local_url":"http://host.docker.internal:23119/api"}`))
		r.Header.Set("X-User-Id", "owner")
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "ZOTERO_LOCAL_ACCESS_FORBIDDEN") {
			t.Fatalf("shared handler accepted host access: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestZoteroCloudRedirectCannotReachHostLocalAPI(t *testing.T) {
	client, err := newZoteroClient(zoteroConfig{Source: "remote", LibraryID: "123", APIKey: "private-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	for _, target := range []string{"http://localhost:23119/api/users/0/items", "https://127.0.0.1:23119/api/users/0/items", "https://[::1]:23119/api/users/0/items"} {
		request := httptest.NewRequest("GET", target, nil)
		original := httptest.NewRequest("GET", "https://api.zotero.org/users/123/items/ABC12345/file", nil)
		if err := client.checkRedirect(request, []*http.Request{original}); err == nil {
			t.Errorf("cloud source redirect accepted host-local target %s", target)
		}
	}
	// A hostname may pass URL-only checks, but its actual private DNS result
	// must still be rejected by the public transport before dialing.
	u, _ := url.Parse("https://localhost:23119/api/users/0/items")
	client.base, _ = url.Parse("https://api.zotero.org")
	r := &http.Request{Method: "GET", URL: u, Header: make(http.Header)}
	_, err = client.http.Transport.RoundTrip(r.WithContext(context.Background()))
	if err == nil {
		t.Fatal("cloud transport accepted the localhost DNS result")
	}
	if !strings.Contains(err.Error(), "不可用于导入") {
		t.Fatalf("cloud transport failed after the private-address check: %v", err)
	}
	if zoteroAllowedIP(net.ParseIP("127.0.0.1"), false) {
		t.Fatal("public source transport permits loopback")
	}
}

func TestZoteroLocalSourceRequiresExplicitOwnerConfiguration(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_BOOTSTRAP_ADMIN_USERNAME", "")
	r := httptest.NewRequest("POST", "/academic/zotero/preview?target_dataset_id=papers", strings.NewReader(`{"source":"local"}`))
	r.Header.Set("X-User-Id", "owner")
	r.Header.Set("Authorization", "Bearer guessed-owner")
	w := httptest.NewRecorder()
	_, accepted := readZoteroRequest(w, r)
	var response map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if accepted || w.Code != http.StatusForbidden || response["code"] != "ZOTERO_LOCAL_ACCESS_FORBIDDEN" {
		t.Fatalf("missing owner configuration permitted local source: %d %s", w.Code, w.Body.String())
	}
}
