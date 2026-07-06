package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoadUsesDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if cfg.Listen.Host != "127.0.0.1" {
		t.Fatalf("host = %q, want 127.0.0.1", cfg.Listen.Host)
	}
	if cfg.Listen.Port != 5024 {
		t.Fatalf("port = %d, want 5024", cfg.Listen.Port)
	}
	if cfg.Auth.Mode != "local-rbac" {
		t.Fatalf("auth.mode = %q, want local-rbac", cfg.Auth.Mode)
	}
	if cfg.Auth.AuthServiceURL != "http://127.0.0.1:8000" {
		t.Fatalf("auth.authServiceURL = %q, want http://127.0.0.1:8000", cfg.Auth.AuthServiceURL)
	}

	wantOrigins := []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	if !reflect.DeepEqual(cfg.CORS.AllowedOrigins, wantOrigins) {
		t.Fatalf("allowedOrigins = %#v, want %#v", cfg.CORS.AllowedOrigins, wantOrigins)
	}

	if len(cfg.Routes) != 5 {
		t.Fatalf("routes = %d, want 5", len(cfg.Routes))
	}

	routesByName := map[string]RouteConfig{}
	for _, route := range cfg.Routes {
		routesByName[route.Name] = route
	}

	wantRoute := func(name, prefix, upstream, healthPath string, stripPath, optional bool) {
		route, ok := routesByName[name]
		if !ok {
			t.Fatalf("route %q missing", name)
		}
		if route.Prefix != prefix {
			t.Fatalf("route %q prefix = %q, want %q", name, route.Prefix, prefix)
		}
		if route.Upstream != upstream {
			t.Fatalf("route %q upstream = %q, want %q", name, route.Upstream, upstream)
		}
		if route.StripPath != stripPath {
			t.Fatalf("route %q stripPath = %v, want %v", name, route.StripPath, stripPath)
		}
		if route.Optional != optional {
			t.Fatalf("route %q optional = %v, want %v", name, route.Optional, optional)
		}
		if route.HealthPath != healthPath {
			t.Fatalf("route %q healthPath = %q, want %q", name, route.HealthPath, healthPath)
		}
		if !route.Enabled {
			t.Fatalf("route %q enabled = false, want true", name)
		}
	}

	wantRoute("authservice-route", "/api/authservice", "http://127.0.0.1:8000", "/health", false, false)
	wantRoute("chat-route", "/api/chat", "http://127.0.0.1:8046", "/health", false, false)
	wantRoute("scan-route", "/api/scan", "http://127.0.0.1:18080", "/health", false, true)
	wantRoute("core-route", "/api/core", "http://127.0.0.1:8001", "/health", true, false)
	wantRoute("evo-route", "/api/evo", "http://127.0.0.1:8047", "/health", true, true)
}

func TestLoadFromConfigFile(t *testing.T) {
	cfgPath := writeTestConfig(t, `
listen:
  host: "127.0.0.1"
  port: 7001
allowNonLocalBind: true
auth:
  mode: "local-rbac"
  authServiceURL: "http://auth-service:8000"
cors:
  allowedOrigins:
    - "https://example.com"
    - "http://127.0.0.1:5173"
timeouts:
  connect: 2s
  read: 5s
  write: 10s
log:
  level: "debug"
  path: "/tmp/local-proxy.log"
`)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load(%q): %v", cfgPath, err)
	}
	if cfg.Listen.Host != "127.0.0.1" {
		t.Fatalf("host = %q, want 127.0.0.1", cfg.Listen.Host)
	}
	if cfg.Listen.Port != 7001 {
		t.Fatalf("port = %d, want 7001", cfg.Listen.Port)
	}
	if cfg.Auth.AuthServiceURL != "http://auth-service:8000" {
		t.Fatalf("authServiceURL = %q, want http://auth-service:8000", cfg.Auth.AuthServiceURL)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("log.level = %q, want debug", cfg.Log.Level)
	}
	if cfg.Timeouts.Connect != 2*time.Second {
		t.Fatalf("timeouts.connect = %s, want 2s", cfg.Timeouts.Connect)
	}
}

func TestLoadExpandsEnvVariablesInFileValues(t *testing.T) {
	cfgPath := writeTestConfig(t, `
listen:
  host: "127.0.0.1"
  port: ${LAZYMIND_LOCAL_PROXY_PORT}

auth:
  mode: "local-rbac"
  authServiceURL: "http://127.0.0.1:${LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT}"

cors:
  allowedOrigins:
    - "http://localhost:${LAZYMIND_FRONTEND_PORT}"
`)

	t.Setenv("LAZYMIND_LOCAL_PROXY_PORT", "7001")
	t.Setenv("LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT", "18000")
	t.Setenv("LAZYMIND_FRONTEND_PORT", "9090")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load(%q): %v", cfgPath, err)
	}
	if cfg.Listen.Port != 7001 {
		t.Fatalf("port = %d, want 7001", cfg.Listen.Port)
	}
	if cfg.Auth.AuthServiceURL != "http://127.0.0.1:18000" {
		t.Fatalf("auth.authServiceURL = %q, want http://127.0.0.1:18000", cfg.Auth.AuthServiceURL)
	}
	wantOrigins := []string{"http://localhost:9090"}
	if !reflect.DeepEqual(cfg.CORS.AllowedOrigins, wantOrigins) {
		t.Fatalf("allowedOrigins = %#v, want %#v", cfg.CORS.AllowedOrigins, wantOrigins)
	}
}

func TestLoadUsesEnvOverrides(t *testing.T) {
	cfgPath := writeTestConfig(t, `
listen:
  host: "127.0.0.1"
  port: 7001
allowNonLocalBind: true
`)

	t.Setenv("LAZYMIND_LOCAL_PROXY_ADDRESS", "0.0.0.0")
	t.Setenv("LAZYMIND_LOCAL_PROXY_PORT", "1234")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load(%q): %v", cfgPath, err)
	}
	if cfg.Listen.Host != "0.0.0.0" {
		t.Fatalf("host = %q, want 0.0.0.0", cfg.Listen.Host)
	}
	if cfg.Listen.Port != 1234 {
		t.Fatalf("port = %d, want 1234", cfg.Listen.Port)
	}
}

func TestLoadCloudReplaceKongConfigFile(t *testing.T) {
	t.Setenv("LAZYMIND_LOCAL_PROXY_PORT", "5024")
	t.Setenv("LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT", "18000")
	t.Setenv("LAZYMIND_LOCAL_PROXY_CORE_HOST_PORT", "18001")
	t.Setenv("LAZYMIND_LOCAL_PROXY_CHAT_HOST_PORT", "18046")
	t.Setenv("LAZYMIND_LOCAL_PROXY_SCAN_HOST_PORT", "18080")
	t.Setenv("LAZYMIND_LOCAL_PROXY_EVO_HOST_PORT", "18047")
	t.Setenv("LAZYMIND_FRONTEND_PORT", "8090")

	cfg, err := Load(filepath.Join("..", "..", "configs", "cloud-replace-kong.yaml"))
	if err != nil {
		t.Fatalf("Load cloud-replace-kong.yaml: %v", err)
	}

	if cfg.Listen.Host != "127.0.0.1" {
		t.Fatalf("host = %q, want 127.0.0.1", cfg.Listen.Host)
	}
	if cfg.Listen.Port != 5024 {
		t.Fatalf("port = %d, want 5024", cfg.Listen.Port)
	}
	if cfg.Auth.Mode != "local-rbac" {
		t.Fatalf("auth.mode = %q, want local-rbac", cfg.Auth.Mode)
	}
	if cfg.Auth.AuthServiceURL != "http://127.0.0.1:18000" {
		t.Fatalf("auth.authServiceURL = %q, want http://127.0.0.1:18000", cfg.Auth.AuthServiceURL)
	}

	wantOrigins := []string{
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://localhost:8090",
		"http://127.0.0.1:8090",
	}
	if !reflect.DeepEqual(cfg.CORS.AllowedOrigins, wantOrigins) {
		t.Fatalf("allowedOrigins = %#v, want %#v", cfg.CORS.AllowedOrigins, wantOrigins)
	}

	routesByName := map[string]RouteConfig{}
	for _, route := range cfg.Routes {
		routesByName[route.Name] = route
	}

	wantRoute := func(name, prefix, upstream, healthPath string, stripPath, optional bool) {
		route, ok := routesByName[name]
		if !ok {
			t.Fatalf("route %q missing", name)
		}
		if route.Prefix != prefix {
			t.Fatalf("route %q prefix = %q, want %q", name, route.Prefix, prefix)
		}
		if route.Upstream != upstream {
			t.Fatalf("route %q upstream = %q, want %q", name, route.Upstream, upstream)
		}
		if route.StripPath != stripPath {
			t.Fatalf("route %q stripPath = %v, want %v", name, route.StripPath, stripPath)
		}
		if route.Optional != optional {
			t.Fatalf("route %q optional = %v, want %v", name, route.Optional, optional)
		}
		if route.HealthPath != healthPath {
			t.Fatalf("route %q healthPath = %q, want %q", name, route.HealthPath, healthPath)
		}
	}

	wantRoute("authservice-route", "/api/authservice", "http://127.0.0.1:18000", "/api/authservice/auth/health", false, false)
	wantRoute("chat-route", "/api/chat", "http://127.0.0.1:18046", "/health", false, false)
	wantRoute("scan-route", "/api/scan", "http://127.0.0.1:18080", "/healthz", false, true)
	wantRoute("core-route", "/api/core", "http://127.0.0.1:18001", "/health", true, false)
	wantRoute("evo-route", "/api/evo", "http://127.0.0.1:18047", "/healthz", true, true)
}

func TestLoadCloudReplaceKongConfigFileIncludesLANOriginWhenSet(t *testing.T) {
	t.Setenv("LAZYMIND_LOCAL_PROXY_PORT", "5024")
	t.Setenv("LAZYMIND_LOCAL_PROXY_AUTH_HOST_PORT", "18000")
	t.Setenv("LAZYMIND_LOCAL_PROXY_CORE_HOST_PORT", "18001")
	t.Setenv("LAZYMIND_LOCAL_PROXY_CHAT_HOST_PORT", "18046")
	t.Setenv("LAZYMIND_LOCAL_PROXY_SCAN_HOST_PORT", "18080")
	t.Setenv("LAZYMIND_LOCAL_PROXY_EVO_HOST_PORT", "18047")
	t.Setenv("LAZYMIND_FRONTEND_PORT", "8090")
	t.Setenv("LAZYMIND_FRONTEND_LAN_ORIGIN", "http://10.0.0.2:8090")

	cfg, err := Load(filepath.Join("..", "..", "configs", "cloud-replace-kong.yaml"))
	if err != nil {
		t.Fatalf("Load cloud-replace-kong.yaml: %v", err)
	}

	wantOrigins := []string{
		"http://localhost:5173",
		"http://127.0.0.1:5173",
		"http://localhost:8090",
		"http://127.0.0.1:8090",
		"http://10.0.0.2:8090",
	}
	if !reflect.DeepEqual(cfg.CORS.AllowedOrigins, wantOrigins) {
		t.Fatalf("allowedOrigins = %#v, want %#v", cfg.CORS.AllowedOrigins, wantOrigins)
	}
}

func TestLoadUsesCLIConfigPathOverEnv(t *testing.T) {
	root := t.TempDir()
	envPath := writeTestConfigToDir(t, root, "env-config.yaml", `
listen:
  host: "10.0.0.2"
  port: 7002
`)
	cliPath := writeTestConfigToDir(t, root, "cli-config.yaml", `
listen:
  host: "10.0.0.3"
  port: 7003
allowNonLocalBind: true
`)

	t.Setenv("LAZYMIND_LOCAL_PROXY_CONFIG", envPath)

	cfg, err := Load(cliPath)
	if err != nil {
		t.Fatalf("Load(%q): %v", cliPath, err)
	}
	if cfg.Listen.Host != "10.0.0.3" {
		t.Fatalf("host = %q, want 10.0.0.3", cfg.Listen.Host)
	}
	if cfg.Listen.Port != 7003 {
		t.Fatalf("port = %d, want 7003", cfg.Listen.Port)
	}
}

func TestValidateRejectsNonLocalBind(t *testing.T) {
	cfgPath := writeTestConfig(t, `
listen:
  host: "0.0.0.0"
  port: 7000
`)

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load() should reject non-local bind without allowNonLocalBind")
	}
}

func TestLoadUsesEnvOverridesAndRejectsNonLocalBind(t *testing.T) {
	cfgPath := writeTestConfig(t, `
listen:
  host: "127.0.0.1"
  port: 7001
`)

	t.Setenv("LAZYMIND_LOCAL_PROXY_ADDRESS", "0.0.0.0")

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load() should reject LAZYMIND_LOCAL_PROXY_ADDRESS=0.0.0.0 without allowNonLocalBind")
	}
}

func TestValidateRejectsUnsupportedAuthMode(t *testing.T) {
	cfgPath := writeTestConfig(t, `
auth:
  mode: "unsupported"
`)

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load() should reject unsupported auth mode")
	}
}

func TestValidateRejectsInvalidAuthServiceURL(t *testing.T) {
	cfgPath := writeTestConfig(t, `
auth:
  mode: "local-rbac"
  authServiceURL: "://bad-url"
`)
	if _, err := Load(cfgPath); err == nil {
		t.Fatal("Load() should reject invalid auth service URL")
	}
}

func TestLoadRejectsInvalidRouteValidation(t *testing.T) {
	cases := []struct {
		name   string
		config string
	}{
		{
			name: "missing route name",
			config: `
routes:
  - prefix: "/api/core"
    upstream: "http://127.0.0.1:8001"
    healthPath: "/health"
`,
		},
		{
			name: "route prefix without leading slash",
			config: `
routes:
  - name: "bad-route"
    prefix: "api/core"
    upstream: "http://127.0.0.1:8001"
    healthPath: "/health"
`,
		},
		{
			name: "missing route upstream",
			config: `
routes:
  - name: "bad-route"
    prefix: "/api/core"
`,
		},
		{
			name: "invalid upstream URL",
			config: `
routes:
  - name: "bad-route"
    prefix: "/api/core"
    upstream: "://upstream"
`,
		},
		{
			name: "invalid health path",
			config: `
routes:
  - name: "bad-route"
    prefix: "/api/core"
    upstream: "http://127.0.0.1:8001"
    healthPath: "health"
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := writeTestConfig(t, tc.config)
			if _, err := Load(cfgPath); err == nil {
				t.Fatalf("Load() should reject invalid route config: %q", tc.name)
			}
		})
	}
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	return writeTestConfigToDir(t, t.TempDir(), "local-proxy.yaml", content)
}

func writeTestConfigToDir(t *testing.T, root, name, content string) string {
	t.Helper()

	cfgDir := filepath.Join(root, "local", "local-proxy", "configs")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	cfgPath := filepath.Join(cfgDir, name)
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return cfgPath
}
