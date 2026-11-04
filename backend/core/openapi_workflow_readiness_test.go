package main

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/mux"
)

func TestWorkflowReadinessOpenAPIContract(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	raw, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	operation := openAPIOperationForTest(t, spec, "get", "/api/core/chat/workflow-readiness")
	parameters, ok := operation["parameters"].([]any)
	if !ok || len(parameters) != 2 {
		t.Fatalf("missing invocation query parameters: %+v", operation)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	properties := schemas["WorkflowReadiness"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"ready", "workflow_ref", "revision_id", "issues", "editable", "repair_url"} {
		if properties[field] == nil {
			t.Fatalf("missing readiness field: %s", field)
		}
	}
}
