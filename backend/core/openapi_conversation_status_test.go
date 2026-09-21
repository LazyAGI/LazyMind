package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gorilla/mux"
)

func TestConversationStatusSchemaKeepsActivityAndOptionalTerminal(t *testing.T) {
	schema := generatedOpenAPISchemas(t)["ConversationRunningStatusItem"].(map[string]any)
	props := schema["properties"].(map[string]any)
	for field, want := range map[string][]any{
		"status":          {"running", "idle", "unknown"},
		"terminal_status": {"completed", "failed", "canceled"},
	} {
		if got := props[field].(map[string]any)["enum"]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s values = %v, want %v", field, got, want)
		}
	}
	if props["terminal_version"].(map[string]any)["type"] != "string" {
		t.Fatal("terminal_version must be a string")
	}
	if got := schema["required"]; !reflect.DeepEqual(got, []any{"conversation_id", "status"}) {
		t.Fatalf("terminal status must be optional: required=%v", got)
	}
}

func TestConversationResultReadAPIContract(t *testing.T) {
	r := mux.NewRouter()
	registerAllRoutes(r)
	var match mux.RouteMatch
	if !r.Match(httptest.NewRequest(http.MethodPost, "/conversations/test-conversation:readResult", nil), &match) {
		t.Fatal("result acknowledgement route is not mounted")
	}
	specJSON, err := buildOpenAPISpecFromRouter(r)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	properties := schemas["ConversationRunningStatusItem"].(map[string]any)["properties"].(map[string]any)
	read, ok := properties["terminal_read"].(map[string]any)
	if !ok || read["type"] != "boolean" {
		t.Fatal("terminal_read must expose the authoritative boolean")
	}
	request, ok := schemas["ConversationResultReadRequest"].(map[string]any)
	if !ok || !reflect.DeepEqual(request["required"], []any{"terminal_version"}) {
		t.Fatal("read confirmation must require the exact observed version")
	}
	paths := spec["paths"].(map[string]any)
	path, ok := paths[apiPrefix+"/conversations/{conversation_id}:readResult"].(map[string]any)
	if !ok {
		t.Fatal("result acknowledgement is missing from OpenAPI")
	}
	operation, ok := path["post"].(map[string]any)
	if !ok {
		t.Fatal("result acknowledgement must be POST")
	}
	responses := operation["responses"].(map[string]any)
	for _, status := range []string{"204", "400", "404", "409"} {
		if _, ok := responses[status]; !ok {
			t.Errorf("missing HTTP %s contract", status)
		}
	}
}
