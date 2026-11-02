package main

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/mux"
)

func TestDocumentImageExportOpenAPIContract(t *testing.T) {
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
	for _, route := range []struct{ method, path string }{
		{"post", "/workflow-artifacts/{artifact_id}/image-exports"},
		{"get", "/workflow-artifacts/{artifact_id}/image-exports/{export_id}"},
		{"post", "/workflow-artifacts/{artifact_id}/image-exports/{export_id}:retry"},
		{"get", "/workflow-artifacts/{artifact_id}/image-exports/{export_id}/files/{file_id}"},
	} {
		openAPIOperationForTest(t, spec, route.method, "/api/core"+route.path)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	request := schemas["DocumentImageExportRequest"].(map[string]any)["properties"].(map[string]any)
	if request["document"] == nil || request["base_revision"] == nil || request["base_draft_version"] == nil {
		t.Fatal("missing snapshot preconditions")
	}
	result := schemas["DocumentImageExportState"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"items", "status", "completed", "total", "ready", "expires_at"} {
		if result[field] == nil {
			t.Fatalf("missing %s", field)
		}
	}
	for _, private := range []string{"source", "local_path", "cache_key", "payload_json"} {
		if result[private] != nil {
			t.Fatalf("private field leaked: %s", private)
		}
	}
}
