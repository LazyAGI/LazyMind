package main

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"regexp"
	"testing"
)

func TestOrdinaryTaskOpenAPIContracts(t *testing.T) {
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
	for _, path := range []string{"/conversations/{conversation_id}/tasks", "/tasks/{task_id}", "/tasks/{task_id}/artifacts", "/tasks/{task_id}:stream", "/workflow-sessions/{session_id}/projection", "/workflow-sessions/{session_id}/events", "/conversations/{conversation_id}/events"} {
		operation := openAPIOperationForTest(t, spec, "get", "/api/core"+path)
		schema := openAPIParameterSchemaForTest(t, operation, "view")
		values, _ := schema["enum"].([]any)
		if len(values) != 2 || values[0] != "ordinary" {
			t.Fatalf("%s missing ordinary view", path)
		}
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	names := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	ordinarySchemas, _ := ordinaryTaskOpenAPI()
	for name := range ordinarySchemas {
		if !names.MatchString(name) {
			t.Errorf("invalid generated schema name: %q", name)
		}
	}
	if schemas["PublicDisplayProgress"] == nil {
		t.Fatal("missing public progress contract")
	}
	task := schemas["OrdinaryTaskView"].(map[string]any)["properties"].(map[string]any)
	for _, key := range []string{"display_key", "revision", "execution_id", "parallel_group_id", "process_steps", "plan_steps", "progress_pct", "capability_dependency", "sources", "stage_artifacts", "pages", "timing"} {
		if task[key] == nil {
			t.Errorf("missing public property %s", key)
		}
	}
	for _, key := range []string{"objective", "prompt", "think", "execution_log", "tool_calls"} {
		if task[key] != nil {
			t.Errorf("private property %s", key)
		}
	}
}
