package subagent

import "testing"

func TestKnowledgeDefaultReleaseRespectsSelectionAndRollback(t *testing.T) {
	t.Setenv("LAZYMIND_KNOWLEDGE_MANAGEMENT_ENABLED", "")
	configs := KnowledgeSystemConfig(nil)
	if len(configs) != 1 {
		t.Fatalf("default configs=%v", configs)
	}
	cfg := configs[0].(map[string]any)
	if len(cfg["allowed_tools"].([]string)) != 9 {
		t.Fatalf("management tools=%v", cfg["allowed_tools"])
	}
	empty := KnowledgeSystemConfig([]string{})[0].(map[string]any)
	if len(empty["allowed_tools"].([]string)) != 0 {
		t.Fatal("explicit empty selection granted tools")
	}
	t.Setenv("LAZYMIND_KNOWLEDGE_MANAGEMENT_ENABLED", "false")
	if len(KnowledgeSystemConfig(nil)) != 0 {
		t.Fatal("rollback still loads management")
	}
}
