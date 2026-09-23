package main

import (
	"fmt"
	"testing"
)

func TestMCPOAuthRuntimeEndpoints(t *testing.T) {
	repo := t.TempDir()
	writeComposeFixture(t, repo)
	cfg, paths, err := NewRuntimeConfig(defaultProfileValue(), repo)
	if err != nil {
		t.Fatal(err)
	}
	assertEnvContains(t, algorithmServiceEnv(cfg, paths, chatProcessName), fmt.Sprintf("LAZYMIND_AUTH_SERVICE_URL=http://127.0.0.1:%d/api/authservice", cfg.AuthService.Port))
	assertEnvContains(t, authServiceEnv(cfg, paths), fmt.Sprintf("LAZYMIND_MCP_OAUTH_PUBLIC_BASE_URL=http://127.0.0.1:%d", cfg.FrontendPort))
	t.Setenv("LAZYMIND_MCP_OAUTH_PUBLIC_BASE_URL", "https://mind.example.com")
	assertEnvContains(t, authServiceEnv(cfg, paths), "LAZYMIND_MCP_OAUTH_PUBLIC_BASE_URL=https://mind.example.com")
}
