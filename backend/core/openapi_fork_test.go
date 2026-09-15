package main

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/mux"
)

func TestOpenAPIForkContractIncludesReplayAndAnchorFields(t *testing.T) {
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
	op := openAPIOperationForTest(t, spec, "post", "/api/core/conversations/{conversation_id}/forks")
	header := false
	for _, raw := range op["parameters"].([]any) {
		p := raw.(map[string]any)
		if p["name"] == "Idempotency-Key" && p["in"] == "header" && p["required"] == true {
			header = true
		}
	}
	if !header {
		t.Fatal("idempotency header absent")
	}
	for _, code := range []string{"200", "201", "409", "413", "500"} {
		if op["responses"].(map[string]any)[code] == nil {
			t.Fatalf("missing response %s", code)
		}
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	errorCodes := schemas["ForkError"].(map[string]any)["properties"].(map[string]any)["code"].(map[string]any)["enum"].([]any)
	for _, code := range []string{"SOURCE_NOT_SETTLED", "SOURCE_NOT_COMPLETED"} {
		found := false
		for _, value := range errorCodes {
			found = found || value == code
		}
		if !found {
			t.Fatalf("missing Fork error code %s", code)
		}
	}
	for name, fields := range map[string][]string{"ForkCreateRequest": {"expected_prefix_revision", "confirmed_fields", "confirmed_values"}, "ForkResult": {"conversation", "fork_origin", "replayed"}, "ConversationItem": {"fork_origin", "fork_capability", "has_fork_descendants"}, "ForkConfigSnapshot": {"model", "thinking_depth", "local_fs_source_ids", "max_input_tokens"}} {
		props := schemas[name].(map[string]any)["properties"].(map[string]any)
		for _, field := range fields {
			if props[field] == nil {
				t.Fatalf("%s missing %s", name, field)
			}
		}
	}
}
