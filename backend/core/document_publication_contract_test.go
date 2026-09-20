package main

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/gorilla/mux"
)

func TestDocumentPublicationStageErrorsAreDeclared(t *testing.T) {
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
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	properties := schemas["documentActionErrorOpenAPIData"].(map[string]any)["properties"].(map[string]any)
	codes := properties["code"].(map[string]any)["enum"].([]any)
	for _, code := range []string{"DOCUMENT_CONVERSION_FAILED", "DOCUMENT_PROVIDERS_UNAVAILABLE",
		"PROVIDER_CREDENTIALS_UNAVAILABLE", "PUBLICATION_OUTCOME_UNKNOWN", "PROVIDER_SYNC_LOCAL_PERSIST_FAILED"} {
		if !slices.Contains(codes, any(code)) {
			t.Errorf("publication error code %s is missing from OpenAPI", code)
		}
	}
}
