package main

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/mux"
)

func TestOpenAPISpecCoversEvolutionSkillMemoryPreferenceOperations(t *testing.T) {
	r := mux.NewRouter()
	registerAllRoutes(r)

	specJSON, err := buildOpenAPISpecFromRouter(r)
	if err != nil {
		t.Fatalf("build openapi spec: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode openapi spec: %v", err)
	}

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths missing in openapi spec")
	}

	cases := []struct {
		method          string
		path            string
		expectRequest   bool
		expectParams    bool
		expectResponses bool
	}{
		{"get", "/api/core/evolution/suggestions", false, true, true},
		{"get", "/api/core/evolution/suggestions/{id}", false, true, true},
		{"post", "/api/core/evolution/suggestions/{id}:approve", false, true, true},
		{"post", "/api/core/evolution/suggestions/{id}:reject", false, true, true},
		{"post", "/api/core/evolution/suggestions:batchApprove", true, false, true},
		{"post", "/api/core/evolution/suggestions:batchReject", true, false, true},
		{"get", "/api/core/skills", false, true, true},
		{"post", "/api/core/skills", true, false, true},
		{"get", "/api/core/skills/{skill_id}", false, true, true},
		{"patch", "/api/core/skills/{skill_id}", true, true, true},
		{"delete", "/api/core/skills/{skill_id}", false, true, true},
		{"get", "/api/core/skills/{skill_id}:draft-preview", false, true, true},
		{"post", "/api/core/skills/{skill_id}:generate", true, true, true},
		{"post", "/api/core/skills/{skill_id}:confirm", false, true, true},
		{"post", "/api/core/skills/{skill_id}:discard", false, true, true},
		{"post", "/api/core/skills/{skill_id}:share", true, true, true},
		{"get", "/api/core/skills/{skill_id}:shares", false, true, true},
		{"get", "/api/core/skill-shares/incoming", false, true, true},
		{"get", "/api/core/skill-shares/outgoing", false, true, true},
		{"get", "/api/core/skill-shares/{share_item_id}", false, true, true},
		{"post", "/api/core/skill-shares/{share_item_id}:accept", false, true, true},
		{"post", "/api/core/skill-shares/{share_item_id}:reject", false, true, true},
		{"post", "/api/core/skill/suggestion", true, false, true},
		{"post", "/api/core/skill/create", true, false, true},
		{"post", "/api/core/skill/remove", true, false, true},
		{"get", "/api/core/personalization-items", false, false, true},
		{"get", "/api/core/model_providers", false, true, true},
		{"get", "/api/core/model_providers/features", false, false, true},
		{"get", "/api/core/model_providers:with_groups", false, false, true},
		{"post", "/api/core/model_providers/{model_provider_id}/groups/{group_id}:check", true, false, true},
		{"get", "/api/core/model_providers/models", false, true, true},
		{"get", "/api/core/model_providers/selected_models", false, false, true},
		{"put", "/api/core/model_providers/selected_models", true, false, true},
		{"get", "/api/core/model_providers/{model_provider_id}/groups", false, true, true},
		{"post", "/api/core/model_providers/{model_provider_id}/groups", true, true, true},
		{"patch", "/api/core/model_providers/{model_provider_id}/groups/{group_id}", true, true, true},
		{"delete", "/api/core/model_providers/{model_provider_id}/groups/{group_id}", false, true, true},
		{"get", "/api/core/model_providers/{model_provider_id}/groups/{group_id}/models", false, true, true},
		{"post", "/api/core/model_providers/{model_provider_id}/groups/{group_id}/models", true, true, true},
		{"delete", "/api/core/model_providers/{model_provider_id}/groups/{group_id}/models/{model_id}", false, true, true},
		{"get", "/api/core/personalization-setting", false, false, true},
		{"put", "/api/core/personalization-setting", true, false, true},
		{"put", "/api/core/memory", true, false, true},
		{"get", "/api/core/memory:draft-preview", false, false, true},
		{"post", "/api/core/memory/suggestion", true, false, true},
		{"post", "/api/core/memory:generate", true, false, true},
		{"post", "/api/core/memory:confirm", false, false, true},
		{"post", "/api/core/memory:discard", false, false, true},
		{"put", "/api/core/user-preference", true, false, true},
		{"get", "/api/core/user-preference:draft-preview", false, false, true},
		{"post", "/api/core/user_preference/suggestion", true, false, true},
		{"post", "/api/core/user-preference:generate", true, false, true},
		{"post", "/api/core/user-preference:confirm", false, false, true},
		{"post", "/api/core/user-preference:discard", false, false, true},
		{"get", "/api/core/agent/threads", false, true, true},
	}

	for _, tc := range cases {
		pathItem, ok := paths[tc.path].(map[string]any)
		if !ok {
			t.Fatalf("path missing from openapi spec: %s", tc.path)
		}
		op, ok := pathItem[tc.method].(map[string]any)
		if !ok {
			t.Fatalf("operation missing from openapi spec: %s %s", tc.method, tc.path)
		}

		if tc.expectRequest {
			if _, ok := op["requestBody"].(map[string]any); !ok {
				t.Fatalf("requestBody missing for %s %s", tc.method, tc.path)
			}
		}
		if tc.expectParams {
			params, ok := op["parameters"].([]any)
			if !ok || len(params) == 0 {
				t.Fatalf("parameters missing for %s %s", tc.method, tc.path)
			}
		}
		if tc.expectResponses {
			responses, ok := op["responses"].(map[string]any)
			if !ok {
				t.Fatalf("responses missing for %s %s", tc.method, tc.path)
			}
			resp200, ok := responses["200"].(map[string]any)
			if !ok {
				t.Fatalf("200 response missing for %s %s", tc.method, tc.path)
			}
			content, ok := resp200["content"].(map[string]any)
			if !ok || len(content) == 0 {
				t.Fatalf("response schema missing for %s %s", tc.method, tc.path)
			}
		}
	}

	pathItem, ok := paths["/api/core/evolution/suggestions"].(map[string]any)
	if !ok {
		t.Fatalf("path missing from openapi spec: /api/core/evolution/suggestions")
	}
	getOp, ok := pathItem["get"].(map[string]any)
	if !ok {
		t.Fatalf("operation missing from openapi spec: get /api/core/evolution/suggestions")
	}
	params, ok := getOp["parameters"].([]any)
	if !ok {
		t.Fatalf("parameters missing for get /api/core/evolution/suggestions")
	}

	paramNames := make(map[string]struct{}, len(params))
	for _, item := range params {
		param, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := param["name"].(string)
		if name != "" {
			paramNames[name] = struct{}{}
		}
	}

	for _, name := range []string{"page", "page_size", "evolution_id", "resource_type", "resource_key", "keyword"} {
		if _, ok := paramNames[name]; !ok {
			t.Fatalf("expected query parameter %q on get /api/core/evolution/suggestions", name)
		}
	}
	for _, name := range []string{"user_id", "skill_id", "memory_id", "user_preference_id", "preference_id"} {
		if _, ok := paramNames[name]; ok {
			t.Fatalf("unexpected removed query parameter %q on get /api/core/evolution/suggestions", name)
		}
	}
}

func TestOpenAPISpecUsesEvalSetDatasetIDsContract(t *testing.T) {
	r := mux.NewRouter()
	registerAllRoutes(r)

	specJSON, err := buildOpenAPISpecFromRouter(r)
	if err != nil {
		t.Fatalf("build openapi spec: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode openapi spec: %v", err)
	}

	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatalf("components missing in openapi spec")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("schemas missing in openapi spec")
	}

	assertSchemaProperties := func(schemaName string, required []string, forbidden []string) {
		t.Helper()
		schema, ok := schemas[schemaName].(map[string]any)
		if !ok {
			t.Fatalf("schema %s missing", schemaName)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema %s properties missing", schemaName)
		}
		for _, name := range required {
			if _, ok := properties[name]; !ok {
				t.Fatalf("schema %s expected property %q", schemaName, name)
			}
		}
		for _, name := range forbidden {
			if _, ok := properties[name]; ok {
				t.Fatalf("schema %s has removed property %q", schemaName, name)
			}
		}
	}

	assertSchemaProperties("CreateEvalSetRequest", []string{"dataset_ids"}, []string{"dataset_id"})
	assertSchemaProperties("UpdateEvalSetRequest", []string{"dataset_ids"}, []string{"dataset_id"})
	assertSchemaProperties("CreateEvalSetByImportRequest", []string{"dataset_ids"}, []string{"dataset_id"})
	assertSchemaProperties("EvalSetResponse", []string{"dataset_ids", "dataset_names"}, []string{"dataset_id", "dataset_name"})
	assertSchemaProperties("EvalSetImportTaskResponse", []string{"dataset_ids", "dataset_names"}, []string{"dataset_id", "dataset_name"})

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths missing in openapi spec")
	}
	pathItem, ok := paths["/api/core/eval-sets"].(map[string]any)
	if !ok {
		t.Fatalf("path missing from openapi spec: /api/core/eval-sets")
	}
	getOp, ok := pathItem["get"].(map[string]any)
	if !ok {
		t.Fatalf("get /api/core/eval-sets missing")
	}
	params, ok := getOp["parameters"].([]any)
	if !ok {
		t.Fatalf("parameters missing for get /api/core/eval-sets")
	}
	paramNames := make(map[string]struct{}, len(params))
	for _, item := range params {
		param, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := param["name"].(string)
		if name != "" {
			paramNames[name] = struct{}{}
		}
	}
	if _, ok := paramNames["dataset_ids"]; !ok {
		t.Fatalf("expected dataset_ids query parameter")
	}
	if _, ok := paramNames["dataset_id"]; ok {
		t.Fatalf("unexpected removed dataset_id query parameter")
	}
}

func TestOpenAPISpecIncludesListDocumentsByDatasets(t *testing.T) {
	r := mux.NewRouter()
	registerAllRoutes(r)

	specJSON, err := buildOpenAPISpecFromRouter(r)
	if err != nil {
		t.Fatalf("build openapi spec: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode openapi spec: %v", err)
	}

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths missing in openapi spec")
	}
	pathItem, ok := paths["/api/core/documents:listByDatasets"].(map[string]any)
	if !ok {
		t.Fatalf("path missing from openapi spec: /api/core/documents:listByDatasets")
	}
	postOp, ok := pathItem["post"].(map[string]any)
	if !ok {
		t.Fatalf("post /api/core/documents:listByDatasets missing")
	}
	if _, ok := postOp["requestBody"].(map[string]any); !ok {
		t.Fatalf("requestBody missing for post /api/core/documents:listByDatasets")
	}
	responses, ok := postOp["responses"].(map[string]any)
	if !ok {
		t.Fatalf("responses missing for post /api/core/documents:listByDatasets")
	}
	if _, ok := responses["200"].(map[string]any); !ok {
		t.Fatalf("200 response missing for post /api/core/documents:listByDatasets")
	}

	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatalf("components missing in openapi spec")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("schemas missing in openapi spec")
	}
	schema, ok := schemas["ListDatasetDocumentsRequest"].(map[string]any)
	if !ok {
		t.Fatalf("ListDatasetDocumentsRequest schema missing")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("ListDatasetDocumentsRequest properties missing")
	}
	for _, name := range []string{"dataset_ids", "keyword", "page_size", "page_token"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("ListDatasetDocumentsRequest expected property %q", name)
		}
	}
}
