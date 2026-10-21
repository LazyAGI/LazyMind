package main

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/mux"
)

func TestOpenAPIArchiveUndoContract(t *testing.T) {
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
	if required := response["required"].([]any); len(required) != 3 {
		t.Fatal("archive receipt must require id, version and newly archived flag")
	}
	for _, field := range []string{"conversation_id", "archived_at", "newly_archived"} {
		if response["properties"].(map[string]any)[field] == nil {
			t.Fatalf("archive receipt missing %s", field)
		}
	}
	op := openAPIOperationForTest(t, spec, "post", "/api/core/conversations/{conversation_id}:unarchive")
	if op["requestBody"].(map[string]any)["required"] == true {
		t.Fatal("manual unarchive must still accept an empty body")
	}
}
