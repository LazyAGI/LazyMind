package main

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/mux"
)

func TestOpenAPISkillMarketPresentationContract(t *testing.T) {
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
	for _, name := range []string{"builtinSkillOpenAPIResponse", "marketItemOpenAPIResponse", "marketPublishOpenAPIRequest", "marketEditOpenAPIRequest"} {
		props := schemaPropertiesForTest(t, schemas, name)
		for _, field := range []string{"display_name", "display_description"} {
			value, ok := props[field].(map[string]any)
			if !ok || value["type"] != "string" {
				t.Errorf("%s missing %s string", name, field)
			}
		}
		if _, ok := props["tag_labels"]; !ok {
			t.Errorf("%s missing tag_labels", name)
		}
	}
}
