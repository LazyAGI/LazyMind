package main

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/gorilla/mux"
)

func TestOpenAPIConversationSelectionAndArchiveUndoContract(t *testing.T) {
	r := mux.NewRouter()
	registerCoreRoutes(r)
	raw, err := buildOpenAPISpecFromRouter(r)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	list := openAPIOperationForTest(t, spec, "get", "/api/core/conversations")
	for name, kind := range map[string]string{"selection_snapshot": "boolean", "selection_group_id": "string"} {
		if schema := openAPIParameterSchemaForTest(t, list, name); schema["type"] != kind {
			t.Fatalf("%s must have type %s: %v", name, kind, schema)
		}
		for _, value := range list["parameters"].([]any) {
			parameter := value.(map[string]any)
			if parameter["name"] == name && (parameter["in"] != "query" || parameter["required"] == true) {
				t.Fatalf("%s must remain an optional query parameter: %v", name, parameter)
			}
		}
	}
	complete := schemaPropertiesForTest(t, schemas, "ConversationListResponse")["selection_complete"].(map[string]any)
	if complete["type"] != "boolean" {
		t.Fatal("selection_complete must be a boolean")
	}
	for name, field := range map[string]string{"ConversationArchiveRequest": "only_if_active", "ConversationUnarchiveRequest": "expected_archived_at"} {
		schema := schemas[name].(map[string]any)
		if schema["properties"].(map[string]any)[field] == nil {
			t.Fatalf("%s missing %s", name, field)
		}
		if required, _ := schema["required"].([]any); len(required) > 0 {
			t.Fatalf("%s must remain optional for existing callers", field)
		}
	}
	response := schemas["ConversationArchiveResponse"].(map[string]any)
	required := response["required"].([]any)
	if len(required) != 4 {
		t.Fatal("archive receipt must require id, version, newly archived flag and affected IDs")
	}
	ids := response["properties"].(map[string]any)["archived_ids"].(map[string]any)
	if ids["type"] != "array" || ids["items"].(map[string]any)["type"] != "string" {
		t.Fatal("archived_ids must be an array of strings")
	}
	for _, field := range []string{"conversation_id", "archived_at", "newly_archived", "archived_ids"} {
		if response["properties"].(map[string]any)[field] == nil || !slices.Contains(required, any(field)) {
			t.Fatalf("archive receipt must require %s", field)
		}
	}
	op := openAPIOperationForTest(t, spec, "post", "/api/core/conversations/{conversation_id}:unarchive")
	if op["requestBody"].(map[string]any)["required"] == true {
		t.Fatal("manual unarchive must still accept an empty body")
	}
}
