package doc

import (
	"net/http/httptest"
	"testing"
)

func TestPageResourceServiceUsesDeploymentMoveDependencies(t *testing.T) {
	newDocumentTestDB(t)
	t.Setenv("LAZYMIND_KNOWLEDGE_MOVE_BUDGET_JSON", `{"max_resources":10,"max_bytes":1000,"prepare_seconds":10,"copy_bytes_per_second":100}`)
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-User-Id", "user-1")
	service, op, err := resourcePageService(req)
	if err != nil {
		t.Fatal(err)
	}
	if service.budget.MaxResources != 10 || service.originals == nil || op.Caller.UserID != "user-1" {
		t.Fatalf("page dependencies differ from MCP: budget=%+v originals=%v user=%s", service.budget, service.originals != nil, op.Caller.UserID)
	}
}

func TestKnowledgeMoveBudgetDefaultsAndExplicitLimits(t *testing.T) {
	t.Setenv("LAZYMIND_KNOWLEDGE_MOVE_BUDGET_JSON", "")
	value, err := KnowledgeMoveBudget()
	if err != nil || value != defaultResourceBudget() {
		t.Fatalf("invalid defaults: %+v %v", value, err)
	}
	for _, raw := range []string{`{}`, `{"max_resources":10,"max_bytes":1000,"prepare_seconds":2,"copy_bytes_per_second":100}`, `{"max_resources":10,"max_bytes":1000,"prepare_seconds":10,"copy_bytes_per_second":100,"unknown":true}`} {
		t.Setenv("LAZYMIND_KNOWLEDGE_MOVE_BUDGET_JSON", raw)
		if _, err = KnowledgeMoveBudget(); err == nil {
			t.Fatalf("invalid budget accepted: %s", raw)
		}
	}
	t.Setenv("LAZYMIND_KNOWLEDGE_MOVE_BUDGET_JSON", `{"max_resources":10,"max_bytes":1000,"prepare_seconds":10,"copy_bytes_per_second":100}`)
	value, err = KnowledgeMoveBudget()
	if err != nil || value.MaxResources != 10 {
		t.Fatalf("valid budget rejected: %+v %v", value, err)
	}
}
