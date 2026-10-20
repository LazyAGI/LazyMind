package main

import "testing"

func TestKnowledgeMoveBudgetRequiresExplicitValidLimits(t *testing.T) {
	t.Setenv("LAZYMIND_KNOWLEDGE_MOVE_BUDGET_JSON", "")
	value, err := knowledgeMoveBudget()
	if err != nil || value.Calibrated {
		t.Fatalf("default opened cross moves: %+v %v", value, err)
	}
	for _, raw := range []string{`{}`, `{"max_resources":10,"max_bytes":1000,"prepare_seconds":2,"copy_bytes_per_second":100}`, `{"max_resources":10,"max_bytes":1000,"prepare_seconds":10,"copy_bytes_per_second":100,"unknown":true}`} {
		t.Setenv("LAZYMIND_KNOWLEDGE_MOVE_BUDGET_JSON", raw)
		if _, err = knowledgeMoveBudget(); err == nil {
			t.Fatalf("invalid budget accepted: %s", raw)
		}
	}
	t.Setenv("LAZYMIND_KNOWLEDGE_MOVE_BUDGET_JSON", `{"max_resources":10,"max_bytes":1000,"prepare_seconds":10,"copy_bytes_per_second":100}`)
	value, err = knowledgeMoveBudget()
	if err != nil || !value.Calibrated || value.MaxResources != 10 {
		t.Fatalf("valid budget rejected: %+v %v", value, err)
	}
}
