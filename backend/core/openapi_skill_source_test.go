package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gorilla/mux"
)

func TestOpenAPISkillSourceContract(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	data, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"skillListItemOpenAPIResponse", "skillDetailOpenAPIResponse"} {
		props := schemaPropertiesForTest(t, schemas, name)
		field, ok := props["origin_builtin_skill_uid"].(map[string]any)
		if !ok || field["type"] != "string" {
			t.Errorf("%s must expose origin_builtin_skill_uid string", name)
		}
	}
	for _, path := range []string{"/api/core/skill-market", "/api/core/skills:trash"} {
		op := openAPIOperationForTest(t, spec, "get", path)
		if _, ok := openAPIParameterNamesForTest(t, op)["source"]; ok {
			t.Errorf("%s advertises unsupported source filter", path)
		}
	}
	op := openAPIOperationForTest(t, spec, "get", "/api/core/skills")
	for _, value := range op["parameters"].([]any) {
		param := value.(map[string]any)
		if param["name"] != "source" {
			continue
		}
		if param["in"] != "query" {
			t.Fatalf("source must be query parameter: %#v", param)
		}
		schema := param["schema"].(map[string]any)
		if !reflect.DeepEqual(schema["enum"], []any{"builtin", "internal", "external"}) {
			t.Fatalf("source enum = %#v", schema["enum"])
		}
		return
	}
	t.Error("skill list missing source query parameter")
}
