package chat

import "testing"

func TestKnowledgeDefaultReleaseReplacesUntrustedConfig(t *testing.T) {
	t.Setenv("LAZYMIND_BROWSER_ENABLED", "false")
	t.Setenv("LAZYMIND_KNOWLEDGE_MANAGEMENT_ENABLED", "")
	body := map[string]any{"system_mcp_config": []any{map[string]any{"id": "untrusted"}}}
	if err := applySystemMCPConfig("user", body); err != nil {
		t.Fatal(err)
	}
	configs := body["system_mcp_config"].([]any)
	if len(configs) != 1 {
		t.Fatalf("configs=%v", configs)
	}
	cfg := configs[0].(map[string]any)
	if cfg["id"] != "lazymind-knowledge" || len(cfg["allowed_tools"].([]string)) != 9 {
		t.Fatalf("config=%v", cfg)
	}
	t.Setenv("LAZYMIND_KNOWLEDGE_MANAGEMENT_ENABLED", "false")
	if err := applySystemMCPConfig("user", body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["system_mcp_config"]; ok {
		t.Fatal("rollback retained stale or untrusted config")
	}
}
