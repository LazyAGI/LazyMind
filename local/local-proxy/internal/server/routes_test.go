package server

import (
	"errors"
	"net/http"
	"testing"

	"github.com/lazyagi/lazymind/local_proxy/internal/config"
)

func TestMatchRoute_DefaultRoutes(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	tests := []struct {
		name       string
		path       string
		upstream   string
		targetPath string
		optional   bool
		stripPath  bool
	}{
		{
			name:       "authservice no strip",
			path:       "/api/authservice/session",
			upstream:   "http://127.0.0.1:8000",
			targetPath: "/api/authservice/session",
			stripPath:  false,
		},
		{
			name:       "chat no strip",
			path:       "/api/chat/messages",
			upstream:   "http://127.0.0.1:8046",
			targetPath: "/api/chat/messages",
			stripPath:  false,
		},
		{
			name:       "scan no strip",
			path:       "/api/scan/jobs",
			upstream:   "http://127.0.0.1:18080",
			targetPath: "/api/scan/jobs",
			optional:   true,
		},
		{
			name:       "core strip nested path",
			path:       "/api/core/conversations",
			upstream:   "http://127.0.0.1:8001",
			targetPath: "/conversations",
			stripPath:  true,
		},
		{
			name:       "core strip root path",
			path:       "/api/core",
			upstream:   "http://127.0.0.1:8001",
			targetPath: "/",
			stripPath:  true,
		},
		{
			name:       "evo strip nested path",
			path:       "/api/evo/jobs",
			upstream:   "http://127.0.0.1:8047",
			targetPath: "/jobs",
			optional:   true,
			stripPath:  true,
		},
	}

	for _, tc := range tests {
		match, err := MatchRoute(cfg.Routes, tc.path)
		if err != nil {
			t.Fatalf("%s: MatchRoute(%q): %v", tc.name, tc.path, err)
		}
		if match.Upstream != tc.upstream {
			t.Fatalf("%s: upstream = %q, want %q", tc.name, match.Upstream, tc.upstream)
		}
		if match.TargetPath != tc.targetPath {
			t.Fatalf("%s: targetPath = %q, want %q", tc.name, match.TargetPath, tc.targetPath)
		}
		if match.Route.Optional != tc.optional {
			t.Fatalf("%s: optional = %v, want %v", tc.name, match.Route.Optional, tc.optional)
		}
		if match.Route.StripPath != tc.stripPath {
			t.Fatalf("%s: stripPath = %v, want %v", tc.name, match.Route.StripPath, tc.stripPath)
		}
	}
}

func TestMatchRoute_BoundaryMatch(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	match, err := MatchRoute(cfg.Routes, "/api/corex")
	if !errors.As(err, &RouteNotFound{}) {
		t.Fatalf("expected route miss for %q, got match %#v and err %v", "/api/corex", match, err)
	}
}

func TestMatchRoute_LongestPrefixWins(t *testing.T) {
	routes := []config.RouteConfig{
		{Name: "root", Prefix: "/api", Upstream: "http://127.0.0.1:9000", StripPath: true, Enabled: true},
		{Name: "core", Prefix: "/api/core", Upstream: "http://127.0.0.1:9001", StripPath: true, Enabled: true},
	}

	match, err := MatchRoute(routes, "/api/core/conversations")
	if err != nil {
		t.Fatalf("MatchRoute(): %v", err)
	}
	if match.Route.Name != "core" {
		t.Fatalf("route.Name = %q, want %q", match.Route.Name, "core")
	}
	if match.TargetPath != "/conversations" {
		t.Fatalf("targetPath = %q, want %q", match.TargetPath, "/conversations")
	}
}

func TestMatchRoute_MissReturnsNotFound(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	_, err = MatchRoute(cfg.Routes, "/api/unknown")
	if !errors.As(err, &RouteNotFound{}) {
		t.Fatalf("expected RouteNotFound, got %v", err)
	}
}

func TestMatchRoute_StripBehavior(t *testing.T) {
	routes := []config.RouteConfig{
		{
			Name:       "strip-route",
			Prefix:     "/api/core",
			Upstream:   "http://127.0.0.1:8001",
			StripPath:  true,
			Enabled:    true,
			Optional:   false,
			HealthPath: "/health",
		},
		{
			Name:       "nonstrip-route",
			Prefix:     "/api/chat",
			Upstream:   "http://127.0.0.1:8046",
			StripPath:  false,
			Enabled:    true,
			Optional:   false,
			HealthPath: "/health",
		},
	}

	tests := []struct {
		name   string
		path   string
		route  string
		target string
	}{
		{
			name:   "strip route root keeps slash",
			path:   "/api/core",
			route:  "strip-route",
			target: "/",
		},
		{
			name:   "strip route nested strips prefix",
			path:   "/api/core/jobs",
			route:  "strip-route",
			target: "/jobs",
		},
		{
			name:   "nonstrip route keeps full path",
			path:   "/api/chat/jobs",
			route:  "nonstrip-route",
			target: "/api/chat/jobs",
		},
	}

	for _, tc := range tests {
		match, err := MatchRoute(routes, tc.path)
		if err != nil {
			t.Fatalf("%s: MatchRoute(%q): %v", tc.name, tc.path, err)
		}
		if match.Route.Name != tc.route {
			t.Fatalf("%s: route = %q, want %q", tc.name, match.Route.Name, tc.route)
		}
		if match.TargetPath != tc.target {
			t.Fatalf("%s: targetPath = %q, want %q", tc.name, match.TargetPath, tc.target)
		}
	}
}

func TestMatchRoute_DisabledOptionalRoute(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	routes := append([]config.RouteConfig{}, cfg.Routes...)
	for i, route := range routes {
		if route.Prefix == "/api/evo" {
			routes[i].Enabled = false
		}
	}

	_, err = MatchRoute(routes, "/api/evo/feature")
	var routeErr RouteMatchError
	if !errors.As(err, &routeErr) {
		t.Fatalf("expected route error, got %v", err)
	}

	var disabled RouteDisabled
	if !errors.As(err, &disabled) {
		t.Fatalf("expected RouteDisabled, got %T", err)
	}
	if disabled.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", disabled.StatusCode(), http.StatusServiceUnavailable)
	}
}
