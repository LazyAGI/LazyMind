package main

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/mux"
)

func TestOpenAPISkillListDocumentsOptionalNameOnlySearch(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	body, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(body, &spec); err != nil {
		t.Fatal(err)
	}
	operation := spec["paths"].(map[string]any)["/api/core/skills"].(map[string]any)["get"].(map[string]any)
	for _, value := range operation["parameters"].([]any) {
		parameter := value.(map[string]any)
		if parameter["name"] == "name_only" {
			if parameter["in"] != "query" || parameter["required"] == true || parameter["schema"].(map[string]any)["type"] != "boolean" {
				t.Fatalf("unexpected name_only contract: %v", parameter)
			}
			return
		}
	}
	t.Fatal("skill list is missing the name_only parameter")
}
