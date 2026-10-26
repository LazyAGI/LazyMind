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
	if cfg["id"] != "lazymind-knowledge" || len(cfg["allowed_tools"].([]string)) != 10 {
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

func TestKnowledgeDisabledOnlyAffectsRunConfiguration(t *testing.T) {
	t.Setenv("LAZYMIND_BROWSER_ENABLED", "false")
	t.Setenv("LAZYMIND_KNOWLEDGE_MANAGEMENT_ENABLED", "true")
	body := map[string]any{"disabled_tools": []string{"knowledge_management"}}
	if err := applySystemMCPConfig("alice", body); err != nil {
		t.Fatal(err)
	}
	if configs, ok := body["system_mcp_config"].([]any); ok && len(configs) != 0 {
		t.Fatalf("disabled provider loaded: %v", configs)
	}
	delete(body, "disabled_tools")
	if err := applySystemMCPConfig("bob", body); err != nil {
		t.Fatal(err)
	}
	if len(body["system_mcp_config"].([]any)) != 1 {
		t.Fatal("another user inherited the disabled provider")
	}
}
