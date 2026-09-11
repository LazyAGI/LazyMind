package chat

import (
	"os"
	"strings"

	"lazymind/core/browser"
)

func applyBrowserRuntimeConfig(userID string, body map[string]any) error {
	endpoint := strings.TrimSpace(os.Getenv("LAZYMIND_BROWSER_MCP_URL"))
	if endpoint == "" || !browserFeatureEnabled() {
		return nil
	}
	token, err := browser.DefaultHub.ToolToken(userID)
	if err != nil {
		return err
	}
	body["system_mcp_config"] = []any{map[string]any{
		"id":            "lazymind-browser",
		"name":          "lazymind-browser",
		"transport":     "streamable-http",
		"url":           endpoint,
		"headers":       map[string]any{"Authorization": "Bearer " + token},
		"allowed_tools": append([]string(nil), browser.ToolNames...),
		"timeout":       35,
	}}
	return nil
}

func browserFeatureEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_BROWSER_ENABLED")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
