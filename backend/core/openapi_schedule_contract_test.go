package main

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/mux"
)

func TestScheduleOpenAPIRequestContractsSurviveNotificationOverlay(t *testing.T) {
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
	paths := spec["paths"].(map[string]any)
	schedules := paths["/api/core/schedules"].(map[string]any)
	get := schedules["get"].(map[string]any)
	parameters, ok := get["parameters"].([]any)
	if !ok || len(parameters) != 1 {
		t.Fatalf("schedule GET lost include_disabled query: %#v", get)
	}
	parameter := parameters[0].(map[string]any)
	if parameter["name"] != "include_disabled" || parameter["in"] != "query" || parameter["schema"].(map[string]any)["type"] != "boolean" {
		t.Fatalf("unexpected schedule filter: %#v", parameter)
	}
	for _, entry := range []struct{ path, schema string }{
		{"/api/core/automation-groups", "AutomationGroupCreateRequest"},
		{"/api/core/schedules/{schedule_id}:move", "ScheduleMoveRequest"},
	} {
		operation := paths[entry.path].(map[string]any)["post"].(map[string]any)
		body, ok := operation["requestBody"].(map[string]any)
		if !ok || body["required"] != true {
			t.Fatalf("%s lost required JSON body: %#v", entry.path, operation)
		}
		schema := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		if schema["$ref"] != "#/components/schemas/"+entry.schema {
			t.Fatalf("%s unexpected request schema: %#v", entry.path, schema)
		}
	}
	post := schedules["post"].(map[string]any)
	body := post["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if body["properties"].(map[string]any)["notification"] == nil {
		t.Fatal("schedule POST lost its notification draft contract")
	}
}
