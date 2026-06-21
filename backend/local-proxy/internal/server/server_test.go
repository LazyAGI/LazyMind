package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lazyagi/lazymind/local_proxy/internal/auth"
	"github.com/lazyagi/lazymind/local_proxy/internal/config"
)

func TestHealthzReturnsOKJSON(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth.local",
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/_local/healthz", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	var payload map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json failed: %v", err)
	}
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if payload["status"] != "ok" {
		t.Fatalf("expected status=ok, got %q", payload["status"])
	}
	if _, ok := payload["timestamp"]; !ok {
		t.Fatalf("timestamp missing")
	}
}

func TestHealthzRejectsNonGet(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth.local",
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/_local/healthz", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", resp.Code)
	}
}

func TestStatusIncludesUpstreamHealthAndListenInfo(t *testing.T) {
	withStatusRoundTripper := &roundTripper{
		handlers: map[string]http.Handler{
			"healthy.internal": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/health" {
					t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}),
		},
	}
	prevStatusRoundTripper := statusRoundTripper
	statusRoundTripper = withStatusRoundTripper
	t.Cleanup(func() {
		statusRoundTripper = prevStatusRoundTripper
	})

	healthUpstream := "http://healthy.internal"

	cfg := config.Config{
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 5024,
		},
		Auth: config.AuthConfig{
			Mode: "local-rbac",
		},
		Routes: []config.RouteConfig{
			{
				Name:       "core-route",
				Prefix:     "/api/core",
				Upstream:   healthUpstream,
				Enabled:    true,
				Optional:   false,
				HealthPath: "/health",
			},
			{
				Name:       "scan-route",
				Prefix:     "/api/scan",
				Upstream:   healthUpstream,
				Enabled:    true,
				Optional:   true,
				HealthPath: "/health",
			},
		},
	}

	handler := NewHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/_local/status", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var payload struct {
		Status    string `json:"status"`
		Timestamp string `json:"timestamp"`
		Listen    struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		} `json:"listen"`
		AuthMode string `json:"authMode"`
		Routes   []struct {
			Name       string `json:"name"`
			Prefix     string `json:"prefix"`
			Enabled    bool   `json:"enabled"`
			Optional   bool   `json:"optional"`
			Health     string `json:"health"`
			HealthPath string `json:"healthPath"`
			Upstream   string `json:"upstream"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json failed: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status=ok, got %q", payload.Status)
	}
	if payload.Timestamp == "" {
		t.Fatalf("timestamp missing")
	}
	if payload.Listen.Host != "127.0.0.1" || payload.Listen.Port != 5024 {
		t.Fatalf("listen mismatch: %#v", payload.Listen)
	}
	if payload.AuthMode != "local-rbac" {
		t.Fatalf("authMode = %q", payload.AuthMode)
	}
	if len(payload.Routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(payload.Routes))
	}
	if payload.Routes[0].Name != "core-route" || payload.Routes[0].Prefix != "/api/core" || !payload.Routes[0].Enabled || payload.Routes[0].Health != "healthy" || payload.Routes[0].HealthPath != "/health" {
		t.Fatalf("route 0 unexpected: %#v", payload.Routes[0])
	}
	if payload.Routes[1].Name != "scan-route" || !payload.Routes[1].Optional {
		t.Fatalf("route 1 unexpected: %#v", payload.Routes[1])
	}
}

func TestStatusReportsDisabledOptionalRoute(t *testing.T) {
	cfg := config.Config{
		Listen: config.ListenConfig{
			Host: "127.0.0.1",
			Port: 5024,
		},
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth-service:8000",
		},
		Routes: []config.RouteConfig{
			{
				Name:       "evo-route",
				Prefix:     "/api/evo",
				Upstream:   "http://127.0.0.1:1",
				Enabled:    false,
				Optional:   true,
				HealthPath: "/health",
			},
		},
	}

	handler := NewHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/_local/status", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	var payload struct {
		Routes []struct {
			Name         string `json:"name"`
			Enabled      bool   `json:"enabled"`
			Optional     bool   `json:"optional"`
			HealthStatus string `json:"health"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json failed: %v", err)
	}

	if payload.Routes[0].HealthStatus != "disabled" {
		t.Fatalf("expected disabled route status, got %q", payload.Routes[0].HealthStatus)
	}
}

func TestStatusReportsUnreachableUpstream(t *testing.T) {
	prevStatusRoundTripper := statusRoundTripper
	statusRoundTripper = &roundTripper{
		errorByHost: map[string]error{
			"unreachable.internal": fmt.Errorf("unreachable"),
		},
	}
	t.Cleanup(func() {
		statusRoundTripper = prevStatusRoundTripper
	})

	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth.local",
		},
		Routes: []config.RouteConfig{
			{
				Name:       "core-route",
				Prefix:     "/api/core",
				Upstream:   "http://unreachable.internal",
				Enabled:    true,
				Optional:   false,
				HealthPath: "/health",
			},
		},
	}

	handler := NewHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/_local/status", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	var payload struct {
		Routes []struct {
			Name         string `json:"name"`
			HealthStatus string `json:"health"`
			HealthCode   int    `json:"healthCode"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json failed: %v", err)
	}
	if payload.Routes[0].HealthStatus != "unreachable" {
		t.Fatalf("expected unreachable, got %q", payload.Routes[0].HealthStatus)
	}
	if payload.Routes[0].HealthCode != 0 {
		t.Fatalf("expected no healthCode for unreachable upstream")
	}
}

func TestStatusReportsUnknownWithoutHealthPath(t *testing.T) {
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth.local",
		},
		Routes: []config.RouteConfig{
			{
				Name:     "core-route",
				Prefix:   "/api/core",
				Upstream: "http://no-health.internal",
				Enabled:  true,
				Optional: false,
			},
		},
	}

	handler := NewHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/_local/status", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	var payload struct {
		Routes []struct {
			HealthStatus string `json:"health"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json failed: %v", err)
	}
	if payload.Routes[0].HealthStatus != "unknown" {
		t.Fatalf("expected unknown, got %q", payload.Routes[0].HealthStatus)
	}
}

func TestStatusRedactsCredentialedUpstreamURLs(t *testing.T) {
	upstreamHost := "http://credentialed.internal"
	healthURL, _ := url.Parse(upstreamHost)
	withStatusRoundTripper := &roundTripper{
		handlers: map[string]http.Handler{
			healthURL.Host: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}),
		},
	}
	prevStatusRoundTripper := statusRoundTripper
	statusRoundTripper = withStatusRoundTripper
	t.Cleanup(func() {
		statusRoundTripper = prevStatusRoundTripper
	})

	upstream := (&url.URL{
		Scheme: "http",
		User:   url.UserPassword("alice", "very-secret-token"),
		Host:   healthURL.Host,
	}).String()

	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth.local",
		},
		Routes: []config.RouteConfig{
			{
				Name:       "core-route",
				Prefix:     "/api/core",
				Upstream:   upstream,
				Enabled:    true,
				Optional:   false,
				HealthPath: "/health",
			},
		},
	}

	handler := NewHandler(cfg)
	req := httptest.NewRequest(http.MethodGet, "/_local/status", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	responseBody := strings.ToLower(resp.Body.String())

	if strings.Contains(responseBody, "very-secret-token") || strings.Contains(responseBody, "alice:very-secret-token") {
		t.Fatalf("response leaks upstream credentials")
	}

	var payload struct {
		Routes []struct {
			Upstream string `json:"upstream"`
		} `json:"routes"`
	}
	if err := json.NewDecoder(strings.NewReader(responseBody)).Decode(&payload); err != nil {
		t.Fatalf("decode json failed: %v", err)
	}
	if !strings.Contains(payload.Routes[0].Upstream, healthURL.Host) {
		t.Fatalf("expected sanitized upstream host, got %q", payload.Routes[0].Upstream)
	}
	if strings.Contains(payload.Routes[0].Upstream, "very-secret-token") {
		t.Fatalf("upstream not sanitized: %q", payload.Routes[0].Upstream)
	}
}

func TestCORS_AllowsAllowedOrigin(t *testing.T) {
	t.Parallel()

	authCalls := 0
	upstreamCalls := 0

	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCalls++
		_, _ = w.Write([]byte(`{}`))
	})
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if got := r.Header.Get("X-User-Id"); got != "" {
			t.Fatalf("unexpected upstream header forwarding")
		}
		_, _ = w.Write([]byte("ok"))
	})

	cfg := config.Config{
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"http://localhost:5173"},
		},
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	handler := newCORSHandler(newAPIProxyHandler(cfg, rt), cfg.CORS.AllowedOrigins)
	req := httptest.NewRequest(http.MethodGet, "/api/core/objects", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:5173")
	}
	if got := resp.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}
	if authCalls != 1 {
		t.Fatalf("expected auth to run, got %d", authCalls)
	}
	if upstreamCalls != 1 {
		t.Fatalf("expected upstream to run, got %d", upstreamCalls)
	}
}

func TestCORS_RejectsDisallowedOrigin(t *testing.T) {
	t.Parallel()

	authCalls := 0
	upstreamCalls := 0

	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCalls++
		_, _ = w.Write([]byte(`{}`))
	})
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		_, _ = w.Write([]byte("ok"))
	})

	cfg := config.Config{
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"http://localhost:5173"},
		},
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	handler := newCORSHandler(newAPIProxyHandler(cfg, rt), cfg.CORS.AllowedOrigins)
	req := httptest.NewRequest(http.MethodGet, "/api/core/objects", nil)
	req.Header.Set("Origin", "http://attacker.example")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", resp.Code)
	}
	if authCalls != 0 {
		t.Fatalf("expected auth to be blocked, got %d calls", authCalls)
	}
	if upstreamCalls != 0 {
		t.Fatalf("expected upstream to be blocked, got %d calls", upstreamCalls)
	}
}

func TestCORS_AllowsRequestWithoutOrigin(t *testing.T) {
	t.Parallel()

	authCalls := 0
	upstreamCalls := 0

	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCalls++
		_, _ = w.Write([]byte(`{}`))
	})
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		_, _ = w.Write([]byte("ok"))
	})

	cfg := config.Config{
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"http://localhost:5173"},
		},
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	handler := newCORSHandler(newAPIProxyHandler(cfg, rt), cfg.CORS.AllowedOrigins)
	req := httptest.NewRequest(http.MethodGet, "/api/core/objects", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty for non-browser request", got)
	}
	if authCalls != 1 {
		t.Fatalf("expected auth to run, got %d", authCalls)
	}
	if upstreamCalls != 1 {
		t.Fatalf("expected upstream to run, got %d", upstreamCalls)
	}
}

func TestCORS_PreflightHandlesAllowedOrigin(t *testing.T) {
	t.Parallel()

	authCalls := 0
	upstreamCalls := 0

	cfg := config.Config{
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"http://localhost:5173"},
		},
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { authCalls++ }),
			"upstream": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamCalls++ }),
		},
	}
	handler := newCORSHandler(newAPIProxyHandler(cfg, rt), cfg.CORS.AllowedOrigins)
	req := httptest.NewRequest(http.MethodOptions, "/api/core/objects", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Authorization,Content-Type")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", resp.Code)
	}
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:5173")
	}
	if got := resp.Header().Get("Access-Control-Allow-Methods"); got != http.MethodPost {
		t.Fatalf("Access-Control-Allow-Methods = %q, want %q", got, http.MethodPost)
	}
	if got := resp.Header().Get("Access-Control-Allow-Headers"); got != "Authorization,Content-Type" {
		t.Fatalf("Access-Control-Allow-Headers = %q, want %q", got, "Authorization,Content-Type")
	}
	if authCalls != 0 {
		t.Fatalf("expected auth to be skipped for preflight, got %d", authCalls)
	}
	if upstreamCalls != 0 {
		t.Fatalf("expected upstream to be skipped for preflight, got %d", upstreamCalls)
	}
}

func TestCORS_PreflightRejectsDisallowedOrigin(t *testing.T) {
	t.Parallel()

	authCalls := 0
	upstreamCalls := 0

	cfg := config.Config{
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"http://localhost:5173"},
		},
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { authCalls++ }),
			"upstream": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamCalls++ }),
		},
	}
	handler := newCORSHandler(newAPIProxyHandler(cfg, rt), cfg.CORS.AllowedOrigins)
	req := httptest.NewRequest(http.MethodOptions, "/api/core/objects", nil)
	req.Header.Set("Origin", "http://attacker.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", resp.Code)
	}
	if authCalls != 0 {
		t.Fatalf("expected auth to be skipped for disallowed preflight, got %d", authCalls)
	}
	if upstreamCalls != 0 {
		t.Fatalf("expected upstream to be skipped for disallowed preflight, got %d", upstreamCalls)
	}
}

func TestAPIProxy_IgnoresUpstreamQueryParameter(t *testing.T) {
	t.Parallel()

	observedUpstreamHost := ""

	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedUpstreamHost = r.URL.Host
		_, _ = w.Write([]byte("ok"))
	})
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}
	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodGet, "/api/core/objects?upstream=http://attacker.local&next=http://localhost:80", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if observedUpstreamHost != "upstream" {
		t.Fatalf("observedUpstreamHost = %q, want %q", observedUpstreamHost, "upstream")
	}
}

func TestAPIProxy_IgnoresUpstreamHeader(t *testing.T) {
	t.Parallel()

	observedUpstreamHost := ""

	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedUpstreamHost = r.URL.Host
		_, _ = w.Write([]byte("ok"))
	})
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}
	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodGet, "/api/core/objects", nil)
	req.Header.Set("X-Target-Upstream", "http://attacker.local")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if observedUpstreamHost != "upstream" {
		t.Fatalf("observedUpstreamHost = %q, want %q", observedUpstreamHost, "upstream")
	}
}

func TestAPIProxy_StripsCorePrefixAndPreservesQuery(t *testing.T) {
	t.Parallel()
	var seenAuthPath string
	var seenUpstreamPath string
	var seenUpstreamQuery string

	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUpstreamPath = r.URL.Path
		seenUpstreamQuery = r.URL.RawQuery
		_, _ = w.Write([]byte("ok"))
	})
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode auth body: %v", err)
		}
		seenAuthPath = body.Path
		_, _ = w.Write([]byte(`{"user_id":"u1"}`))
	})

	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}

	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodGet, "/api/core/foo?x=1", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if seenAuthPath != "/api/core/foo" {
		t.Fatalf("authorization path = %q, want %q", seenAuthPath, "/api/core/foo")
	}
	if seenUpstreamPath != "/foo" {
		t.Fatalf("upstream path = %q, want %q", seenUpstreamPath, "/foo")
	}
	if seenUpstreamQuery != "x=1" {
		t.Fatalf("upstream query = %q, want %q", seenUpstreamQuery, "x=1")
	}
}

func TestAPIProxy_ChatStreamForwardsWithoutStripping(t *testing.T) {
	t.Parallel()
	var seenUpstreamPath string

	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUpstreamPath = r.URL.Path
		_, _ = w.Write([]byte("stream-ok"))
	})
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "chat", Prefix: "/api/chat", Upstream: "http://upstream", StripPath: false, Enabled: true},
		},
	}

	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodGet, "/api/chat/stream", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if seenUpstreamPath != "/api/chat/stream" {
		t.Fatalf("upstream path = %q, want %q", seenUpstreamPath, "/api/chat/stream")
	}
}

func TestAPIProxy_UploadBodyIsUnchanged(t *testing.T) {
	t.Parallel()
	payload := bytes.Repeat([]byte("upload-body-bytes-"), 64)
	var observed []byte

	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		observed, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		_, _ = w.Write([]byte("ok"))
	})
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}
	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodPost, "/api/core/files", bytes.NewReader(payload))
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if !bytes.Equal(observed, payload) {
		t.Fatalf("upstream body mismatch")
	}
}

func TestAPIProxy_UploadBodyForwardsContentType(t *testing.T) {
	t.Parallel()

	payload := []byte("upload-content-type")
	var observedContentType string
	var observedBody []byte

	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedContentType = r.Header.Get("Content-Type")
		var err error
		observedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream body: %v", err)
		}
		_, _ = w.Write([]byte("ok"))
	})
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}

	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodPost, "/api/core/files", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if observedContentType != "application/octet-stream" {
		t.Fatalf("content-type = %q, want %q", observedContentType, "application/octet-stream")
	}
	if !bytes.Equal(observedBody, payload) {
		t.Fatalf("upstream body mismatch")
	}
}

func TestAPIProxy_AuthClaimsMappedToUpstreamIdentityHeaders(t *testing.T) {
	t.Parallel()

	var authPayload map[string]any
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&authPayload)
		_, _ = w.Write([]byte(`{"user_id":"u-42","username":"alice","tenant_id":"tenant-1","role":"admin"}`))
	})

	var seenHeaders http.Header
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHeaders = r.Header.Clone()
		_, _ = w.Write([]byte("ok"))
	})
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}

	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodGet, "/api/core/messages", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-User-Id", "spoofed-id")
	req.Header.Set("X-User-Name", "spoofed-name")
	req.Header.Set("X-Tenant-Id", "spoofed-tenant")
	req.Header.Set("X-User-Role", "spoofed-role")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if authPayload["path"] != "/api/core/messages" {
		t.Fatalf("auth payload path = %#v, want %q", authPayload["path"], "/api/core/messages")
	}
	if seenHeaders.Get("X-User-Id") != "u-42" {
		t.Fatalf("X-User-Id = %q, want %q", seenHeaders.Get("X-User-Id"), "u-42")
	}
	if seenHeaders.Get("X-User-Name") != "alice" {
		t.Fatalf("X-User-Name = %q, want %q", seenHeaders.Get("X-User-Name"), "alice")
	}
	if seenHeaders.Get("X-Tenant-Id") != "tenant-1" {
		t.Fatalf("X-Tenant-Id = %q, want %q", seenHeaders.Get("X-Tenant-Id"), "tenant-1")
	}
	if seenHeaders.Get("X-User-Role") != "admin" {
		t.Fatalf("X-User-Role = %q, want %q", seenHeaders.Get("X-User-Role"), "admin")
	}
}

func TestAPIProxy_AuthorizationFailureStopsBeforeUpstream(t *testing.T) {
	t.Parallel()
	upstreamCalls := 0

	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		_, _ = w.Write([]byte("should-not-happen"))
	})
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"nope"}`))
	})
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth":     authHandler,
			"upstream": upstreamHandler,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
	}
	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodGet, "/api/core/protected", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", resp.Code)
	}
	if upstreamCalls != 0 {
		t.Fatalf("expected upstream to be skipped, got %d", upstreamCalls)
	}
}

func TestAPIProxy_RouteNotFoundReturns404(t *testing.T) {
	t.Parallel()
	authCalls := 0
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCalls++
	})
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth": authHandler,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "chat", Prefix: "/api/chat", Upstream: "http://upstream", StripPath: false, Enabled: true},
		},
	}
	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodGet, "/api/unknown", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", resp.Code)
	}
	if authCalls != 0 {
		t.Fatalf("expected no auth call, got %d", authCalls)
	}
}

func TestAPIProxy_DisabledOptionalRouteReturns503(t *testing.T) {
	t.Parallel()
	authCalls := 0
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCalls++
	})
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth": authHandler,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "evo", Prefix: "/api/evo", Upstream: "http://upstream", Enabled: false, Optional: true},
		},
	}
	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodGet, "/api/evo/feature", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", resp.Code)
	}
	if authCalls != 0 {
		t.Fatalf("expected no auth call for disabled route, got %d", authCalls)
	}
}

func TestAPIProxy_UpstreamTimeoutReturns504(t *testing.T) {
	t.Parallel()
	authHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	rt := &roundTripper{
		handlers: map[string]http.Handler{
			"auth": authHandler,
		},
		errorByHost: map[string]error{
			"upstream": context.DeadlineExceeded,
		},
	}
	cfg := config.Config{
		Auth: config.AuthConfig{
			Mode:           "local-rbac",
			AuthServiceURL: "http://auth",
		},
		Routes: []config.RouteConfig{
			{Name: "core", Prefix: "/api/core", Upstream: "http://upstream", StripPath: true, Enabled: true},
		},
		Timeouts: config.TimeoutConfig{
			Read: 1 * time.Millisecond,
		},
	}
	handler := newAPIProxyHandler(cfg, rt)
	req := httptest.NewRequest(http.MethodGet, "/api/core/slow", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected status 504, got %d", resp.Code)
	}
}

func TestStatusSanitizesUpstreamCredentials(t *testing.T) {
	redacted := sanitizeUpstream("http://alice:super-secret-token@127.0.0.1:8001")
	if strings.Contains(redacted, "alice") || strings.Contains(redacted, "super-secret-token") {
		t.Fatalf("sanitizeUpstream leaked credentials: %q", redacted)
	}
	if redacted != "http://127.0.0.1:8001" {
		t.Fatalf("sanitizeUpstream = %q, want %q", redacted, "http://127.0.0.1:8001")
	}
}

type roundTripper struct {
	handlers    map[string]http.Handler
	errorByHost map[string]error
}

func (r *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if errMap, ok := r.errorByHost[req.URL.Host]; ok && errMap != nil {
		return nil, errMap
	}
	handler, ok := r.handlers[req.URL.Host]
	if !ok {
		return nil, fmt.Errorf("no handler for host %q", req.URL.Host)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func newAPIProxyHandler(cfg config.Config, rt *roundTripper) http.Handler {
	return &apiProxyHandler{
		routes: cfg.Routes,
		rbac: &auth.RBACAdapter{
			AuthServiceURL: cfg.Auth.AuthServiceURL,
			Client: &http.Client{
				Transport: rt,
			},
		},
		transport: rt,
	}
}
