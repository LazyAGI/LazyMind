package main

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"testing"
)

func TestTaskRemovalOpenAPIContracts(t *testing.T) {
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
	preview := openAPIOperationForTest(t, spec, "get", "/api/core/task-center/tasks/{task_id}/removal-preview")
	if got := openAPIObjectResponseRefForTest(t, preview); got != "#/components/schemas/TaskRemovalPreview" {
		t.Fatalf("preview contract %s", got)
	}
	remove := openAPIOperationForTest(t, spec, "post", "/api/core/task-center/tasks/{task_id}:remove")
	if got := openAPIObjectResponseRefForTest(t, remove); got != "#/components/schemas/TaskRemovalResult" {
		t.Fatalf("removal contract %s", got)
	}
	body := remove["requestBody"].(map[string]any)
	if body["required"] == true {
		t.Fatal("old clients must support no request body")
	}
	schema := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema["$ref"] != "#/components/schemas/TaskRemovalRequest" {
		t.Fatal("missing confirmation version contract")
	}
}
