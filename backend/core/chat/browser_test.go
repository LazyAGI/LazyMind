package chat

import (
	"strings"
	"testing"

	"lazymind/core/browser"
)

func TestApplyBrowserRuntimeConfigDisabledByDefault(t *testing.T) {
	t.Setenv("LAZYMIND_BROWSER_ENABLED", "")
	t.Setenv("LAZYMIND_BROWSER_MCP_URL", "")

	body := map[string]any{}
	if err := applyBrowserRuntimeConfig("user-1", body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["system_mcp_config"]; ok {
		t.Fatal("browser MCP config should not be injected by default")
	}
}

func TestApplyBrowserRuntimeConfigInjectsSignedUserToken(t *testing.T) {
	t.Setenv("LAZYMIND_BROWSER_ENABLED", "true")
	t.Setenv("LAZYMIND_BROWSER_MCP_URL", "http://core:8000/mcp/browser/v1")

	body := map[string]any{}
	if err := applyBrowserRuntimeConfig("user-42", body); err != nil {
		t.Fatal(err)
	}

	configs, ok := body["system_mcp_config"].([]any)
	if !ok || len(configs) != 1 {
		t.Fatalf("unexpected system_mcp_config: %#v", body["system_mcp_config"])
	}
	config, ok := configs[0].(map[string]any)
	if !ok || config["name"] != "lazymind-browser" || config["url"] != "http://core:8000/mcp/browser/v1" {
		t.Fatalf("unexpected browser MCP config: %#v", configs[0])
	}
	if config["transport"] != "streamable-http" {
		t.Fatalf("unexpected browser MCP transport: %#v", config["transport"])
	}
	headers, ok := config["headers"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected browser MCP headers: %#v", config["headers"])
	}
	authorization, _ := headers["Authorization"].(string)
	token := strings.TrimPrefix(authorization, "Bearer ")
	if token == authorization || token == "" {
		t.Fatalf("missing bearer browser authorization: %q", authorization)
	}
	if _, err := browser.DefaultHub.VerifyToolToken(token); err != nil {
		t.Fatalf("verify browser authorization: %v", err)
	}
}

func TestApplyBrowserRuntimeConfigRejectsMissingUser(t *testing.T) {
	t.Setenv("LAZYMIND_BROWSER_ENABLED", "true")
	t.Setenv("LAZYMIND_BROWSER_MCP_URL", "http://core:8000/mcp/browser/v1")

	if err := applyBrowserRuntimeConfig("", map[string]any{}); err == nil {
		t.Fatal("browser MCP config must reject a missing user")
	}
}
