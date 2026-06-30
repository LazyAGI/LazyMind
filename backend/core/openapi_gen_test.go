package main

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestOpenAPISpecCoversAllRegisteredRoutes(t *testing.T) {
	r := mux.NewRouter()
	registerCoreRoutes(r)

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

	missing := make([]string, 0)
	err = r.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, err := route.GetPathTemplate()
		if err != nil || path == "" {
			return nil
		}
		if strings.HasPrefix(path, "/openapi") || path == "/docs" {
			return nil
		}
		methods, err := route.GetMethods()
		if err != nil {
			return nil
		}
		fullPath := apiPrefix + path
		pathItem, ok := paths[fullPath].(map[string]any)
		if !ok {
			for _, method := range methods {
				missing = append(missing, method+" "+fullPath)
			}
			return nil
		}
		for _, method := range methods {
			if _, ok := pathItem[strings.ToLower(method)].(map[string]any); !ok {
				missing = append(missing, method+" "+fullPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("openapi spec missing registered routes:\n%s", strings.Join(missing, "\n"))
	}
}

func TestOpenAPISpecIncludesEvalReportResultSchema(t *testing.T) {
	r := mux.NewRouter()
	registerCoreRoutes(r)

	specJSON, err := buildOpenAPISpecFromRouter(r)
	if err != nil {
		t.Fatalf("build openapi spec: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode openapi spec: %v", err)
	}
	op := openAPIOperationForTest(t, spec, "get", "/api/core/agent/threads/{thread_id}/results/eval-reports")
	responseRef := openAPIResponseRefForTest(t, op)
	if responseRef != "#/components/schemas/agentEvalReportResultOpenAPIResponse" {
		t.Fatalf("unexpected eval report item schema ref: %q", responseRef)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	props := schemaPropertiesForTest(t, schemas, "agentEvalReportResultOpenAPIResponse")
	for _, name := range []string{"artifact_id", "artifact_ref", "schema", "case_count", "data", "report_id", "bad_case_count", "trace_coverage"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("eval report schema missing property %q", name)
		}
	}
	coverageProps := schemaPropertiesForTest(t, schemas, "agentEvalReportTraceCoverageOpenAPIResponse")
	for _, name := range []string{"covered_count", "total_count", "rate"} {
		if _, ok := coverageProps[name]; !ok {
			t.Fatalf("trace coverage schema missing property %q", name)
		}
	}

	badCaseOp := openAPIOperationForTest(t, spec, "get", "/api/core/agent/threads/{thread_id}/results/eval-reports/{report_id}/bad-cases")
	badCaseResponseRef := openAPIObjectResponseRefForTest(t, badCaseOp)
	if badCaseResponseRef != "#/components/schemas/agentEvalReportBadCaseListOpenAPIResponse" {
		t.Fatalf("unexpected eval report bad case schema ref: %q", badCaseResponseRef)
	}
	params := openAPIParameterNamesForTest(t, badCaseOp)
	for _, name := range []string{"thread_id", "report_id", "page_token", "page_size", "keyword", "failure_type"} {
		if _, ok := params[name]; !ok {
			t.Fatalf("bad case operation missing parameter %q", name)
		}
	}
	badCaseProps := schemaPropertiesForTest(t, schemas, "agentEvalReportBadCaseListOpenAPIResponse")
	for _, name := range []string{"items", "total_size", "next_page_token"} {
		if _, ok := badCaseProps[name]; !ok {
			t.Fatalf("bad case list schema missing property %q", name)
		}
	}

	abtestsOp := openAPIOperationForTest(t, spec, "get", "/api/core/agent/threads/{thread_id}/results/abtests")
	abtestsResponseRef := openAPIResponseRefForTest(t, abtestsOp)
	if abtestsResponseRef != "#/components/schemas/agentABTestResultOpenAPIResponse" {
		t.Fatalf("unexpected abtest result item schema ref: %q", abtestsResponseRef)
	}
	abtestsParams := openAPIParameterNamesForTest(t, abtestsOp)
	if _, ok := abtestsParams["thread_id"]; !ok {
		t.Fatalf("abtest result operation missing parameter %q", "thread_id")
	}
	abtestsProps := schemaPropertiesForTest(t, schemas, "agentABTestResultOpenAPIResponse")
	for _, name := range []string{"artifact_id", "artifact_ref", "schema", "case_count", "data", "abtest_id", "case_details_summary", "file_url"} {
		if _, ok := abtestsProps[name]; !ok {
			t.Fatalf("abtest result schema missing property %q", name)
		}
	}

	abCaseOp := openAPIOperationForTest(t, spec, "get", "/api/core/agent/threads/{thread_id}/results/abtests/{abtest_id}/case-details")
	abCaseResponseRef := openAPIObjectResponseRefForTest(t, abCaseOp)
	if abCaseResponseRef != "#/components/schemas/agentABTestCaseDetailListOpenAPIResponse" {
		t.Fatalf("unexpected abtest case detail schema ref: %q", abCaseResponseRef)
	}
	abParams := openAPIParameterNamesForTest(t, abCaseOp)
	for _, name := range []string{"thread_id", "abtest_id", "page_token", "page_size", "keyword", "outcome"} {
		if _, ok := abParams[name]; !ok {
			t.Fatalf("abtest case detail operation missing parameter %q", name)
		}
	}
	abCaseProps := schemaPropertiesForTest(t, schemas, "agentABTestCaseDetailListOpenAPIResponse")
	for _, name := range []string{"items", "total_size", "next_page_token"} {
		if _, ok := abCaseProps[name]; !ok {
			t.Fatalf("abtest case detail list schema missing property %q", name)
		}
	}
}

func TestOpenAPISpecDocumentsFeedbackCancellation(t *testing.T) {
	r := mux.NewRouter()
	registerCoreRoutes(r)

	specJSON, err := buildOpenAPISpecFromRouter(r)
	if err != nil {
		t.Fatalf("build openapi spec: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode openapi spec: %v", err)
	}
	op := openAPIOperationForTest(t, spec, "post", "/api/core/conversations:feedBackChatHistory")
	requestBody, ok := op["requestBody"].(map[string]any)
	if !ok {
		t.Fatalf("requestBody missing")
	}
	content := requestBody["content"].(map[string]any)
	jsonContent := content["application/json"].(map[string]any)
	schema := jsonContent["schema"].(map[string]any)
	ref, _ := schema["$ref"].(string)
	if ref != "#/components/schemas/ConversationFeedbackRequest" {
		t.Fatalf("unexpected feedback request ref: %q", ref)
	}

	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	props := schemaPropertiesForTest(t, schemas, "ConversationFeedbackRequest")
	typeSchema, ok := props["type"].(map[string]any)
	if !ok {
		t.Fatalf("feedback type schema missing")
	}
	description, _ := typeSchema["description"].(string)
	if !strings.Contains(description, "FEED_BACK_TYPE_UNSPECIFIED") || !strings.Contains(description, "cancels feedback") {
		t.Fatalf("feedback type description does not document cancellation: %q", description)
	}
	oneOf, ok := typeSchema["oneOf"].([]any)
	if !ok || len(oneOf) != 2 {
		t.Fatalf("feedback type should document numeric and string forms, got %#v", typeSchema["oneOf"])
	}
}

func openAPIOperationForTest(t *testing.T, spec map[string]any, method, path string) map[string]any {
	t.Helper()
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths missing in openapi spec")
	}
	pathItem, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("path missing from openapi spec: %s", path)
	}
	op, ok := pathItem[method].(map[string]any)
	if !ok {
		t.Fatalf("operation missing from openapi spec: %s %s", method, path)
	}
	return op
}

func openAPIResponseRefForTest(t *testing.T, op map[string]any) string {
	t.Helper()
	schema := openAPIResponseSchemaForTest(t, op)
	items, ok := schema["items"].(map[string]any)
	if !ok {
		t.Fatalf("response schema items missing")
	}
	ref, ok := items["$ref"].(string)
	if !ok {
		t.Fatalf("response schema item ref missing")
	}
	return ref
}

func openAPIObjectResponseRefForTest(t *testing.T, op map[string]any) string {
	t.Helper()
	schema := openAPIResponseSchemaForTest(t, op)
	ref, ok := schema["$ref"].(string)
	if !ok {
		t.Fatalf("response schema ref missing")
	}
	return ref
}

func openAPIResponseSchemaForTest(t *testing.T, op map[string]any) map[string]any {
	t.Helper()
	responses, ok := op["responses"].(map[string]any)
	if !ok {
		t.Fatalf("responses missing")
	}
	response200, ok := responses["200"].(map[string]any)
	if !ok {
		t.Fatalf("200 response missing")
	}
	content, ok := response200["content"].(map[string]any)
	if !ok {
		t.Fatalf("response content missing")
	}
	jsonContent, ok := content["application/json"].(map[string]any)
	if !ok {
		t.Fatalf("application/json response missing")
	}
	schema, ok := jsonContent["schema"].(map[string]any)
	if !ok {
		t.Fatalf("response schema missing")
	}
	return schema
}

func openAPIParameterNamesForTest(t *testing.T, op map[string]any) map[string]struct{} {
	t.Helper()
	items, ok := op["parameters"].([]any)
	if !ok {
		t.Fatalf("parameters missing")
	}
	result := map[string]struct{}{}
	for _, item := range items {
		param, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := param["name"].(string)
		if name != "" {
			result[name] = struct{}{}
		}
	}
	return result
}

func schemaPropertiesForTest(t *testing.T, schemas map[string]any, schemaName string) map[string]any {
	t.Helper()
	schema, ok := schemas[schemaName].(map[string]any)
	if !ok {
		t.Fatalf("schema %s missing", schemaName)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema %s properties missing", schemaName)
	}
	return properties
}

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
		{"get", "/api/core/skills", false, true, true},
		{"get", "/api/core/skills/tags", false, false, true},
		{"get", "/api/core/skills/categories", false, false, true},
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
		{"post", "/api/core/skill/create", true, false, true},
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
		{"post", "/api/core/memory:generate", true, false, true},
		{"post", "/api/core/memory:confirm", false, false, true},
		{"post", "/api/core/memory:discard", false, false, true},
		{"put", "/api/core/user-preference", true, false, true},
		{"get", "/api/core/user-preference:draft-preview", false, false, true},
		{"post", "/api/core/user-preference:generate", true, false, true},
		{"post", "/api/core/user-preference:confirm", false, false, true},
		{"post", "/api/core/user-preference:discard", false, false, true},
		{"get", "/api/core/resource-versions", false, true, true},
		{"get", "/api/core/resource-versions/{version_id}", false, true, true},
		{"get", "/api/core/agent/threads", false, true, true},
		{"get", "/api/core/conversations/{name}:history", false, true, true},
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

	removedPaths := []string{
		"/api/core/evolution/suggestions",
		"/api/core/evolution/suggestions/{id}",
		"/api/core/evolution/suggestions/{id}:approve",
		"/api/core/evolution/suggestions/{id}:reject",
		"/api/core/evolution/suggestions:batchApprove",
		"/api/core/evolution/suggestions:batchReject",
		"/api/core/skill/suggestion",
		"/api/core/skill/remove",
		"/api/core/memory/suggestion",
		"/api/core/user_preference/suggestion",
	}
	for _, path := range removedPaths {
		if _, ok := paths[path]; ok {
			t.Fatalf("removed legacy suggestion path still present in openapi spec: %s", path)
		}
	}

	historyItem, ok := paths["/api/core/conversations/{name}:history"].(map[string]any)
	if !ok {
		t.Fatalf("path missing: /api/core/conversations/{name}:history")
	}
	historyGet, ok := historyItem["get"].(map[string]any)
	if !ok {
		t.Fatalf("get operation missing for conversation history")
	}
	historyParams, ok := historyGet["parameters"].([]any)
	if !ok {
		t.Fatalf("parameters missing for conversation history")
	}
	historyParamNames := make(map[string]string, len(historyParams))
	for _, item := range historyParams {
		p, ok := item.(map[string]any)
		if !ok {
			continue
		}
		historyParamNames[p["name"].(string)] = p["in"].(string)
	}
	for _, want := range []struct{ name, inVal string }{
		{"name", "path"},
		{"page_size", "query"},
		{"page_token", "query"},
	} {
		if got, ok := historyParamNames[want.name]; !ok || got != want.inVal {
			t.Fatalf("expected history parameter %q in %q, got %q (%v)", want.name, want.inVal, got, historyParamNames)
		}
	}
}

func TestOpenAPISpecAssignsMetadataFieldsToUserPreference(t *testing.T) {
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

	schemaProperties := func(schemaName string) map[string]any {
		t.Helper()
		schema, ok := schemas[schemaName].(map[string]any)
		if !ok {
			t.Fatalf("schema %s missing", schemaName)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema %s properties missing", schemaName)
		}
		return properties
	}

	memoryRequestProps := schemaProperties("memoryUpsertOpenAPIRequest")
	for _, name := range []string{"content", "auto_evo"} {
		if _, ok := memoryRequestProps[name]; !ok {
			t.Fatalf("memoryUpsertOpenAPIRequest expected property %q", name)
		}
	}
	for _, name := range []string{"agent_persona", "preferred_name", "response_style"} {
		if _, ok := memoryRequestProps[name]; ok {
			t.Fatalf("memoryUpsertOpenAPIRequest has user_preference-only property %q", name)
		}
	}

	preferenceRequestProps := schemaProperties("managedStateUpsertOpenAPIRequest")
	for _, name := range []string{"content", "agent_persona", "preferred_name", "response_style", "auto_evo"} {
		if _, ok := preferenceRequestProps[name]; !ok {
			t.Fatalf("managedStateUpsertOpenAPIRequest expected property %q", name)
		}
	}

	memoryResponseProps := schemaProperties("managedStateOpenAPIResponse")
	for _, name := range []string{"agent_persona", "preferred_name", "response_style"} {
		if _, ok := memoryResponseProps[name]; !ok {
			t.Fatalf("managedStateOpenAPIResponse expected property %q", name)
		}
	}

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths missing in openapi spec")
	}
	assertRequestSchemaRef := func(path, method, wantRef string) {
		t.Helper()
		pathItem, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("path missing from openapi spec: %s", path)
		}
		op, ok := pathItem[method].(map[string]any)
		if !ok {
			t.Fatalf("operation missing from openapi spec: %s %s", method, path)
		}
		requestBody, ok := op["requestBody"].(map[string]any)
		if !ok {
			t.Fatalf("requestBody missing for %s %s", method, path)
		}
		content, ok := requestBody["content"].(map[string]any)
		if !ok {
			t.Fatalf("requestBody content missing for %s %s", method, path)
		}
		jsonContent, ok := content["application/json"].(map[string]any)
		if !ok {
			t.Fatalf("application/json requestBody missing for %s %s", method, path)
		}
		schema, ok := jsonContent["schema"].(map[string]any)
		if !ok {
			t.Fatalf("requestBody schema missing for %s %s", method, path)
		}
		if got, _ := schema["$ref"].(string); got != wantRef {
			t.Fatalf("requestBody schema ref for %s %s = %q, want %q", method, path, got, wantRef)
		}
	}

	assertRequestSchemaRef("/api/core/memory", "put", "#/components/schemas/memoryUpsertOpenAPIRequest")
	assertRequestSchemaRef("/api/core/user-preference", "put", "#/components/schemas/managedStateUpsertOpenAPIRequest")
}

func TestOpenAPISpecCoversEvalSetOperations(t *testing.T) {
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
		method string
		path   string
		tag    string
	}{
		{"get", "/api/core/eval-sets", "eval-sets"},
		{"post", "/api/core/eval-sets", "eval-sets"},
		{"get", "/api/core/eval-sets/datasets", "eval-sets"},
		{"get", "/api/core/eval-sets/question-types", "eval-sets"},
		{"get", "/api/core/eval-sets/{eval_set_id}", "eval-sets"},
		{"patch", "/api/core/eval-sets/{eval_set_id}", "eval-sets"},
		{"delete", "/api/core/eval-sets/{eval_set_id}", "eval-sets"},
		{"get", "/api/core/eval-sets/{eval_set_id}/question-types", "eval-set-items"},
		{"get", "/api/core/eval-sets/{eval_set_id}/items", "eval-set-items"},
		{"post", "/api/core/eval-sets/{eval_set_id}/items", "eval-set-items"},
		{"patch", "/api/core/eval-sets/{eval_set_id}/items/{item_id}", "eval-set-items"},
		{"delete", "/api/core/eval-sets/{eval_set_id}/items/{item_id}", "eval-set-items"},
		{"post", "/api/core/eval-sets/{eval_set_id}/items:batchDelete", "eval-set-items"},
		{"get", "/api/core/eval-set-import-templates/{file_type}", "eval-set-imports"},
		{"post", "/api/core/eval-sets/imports:preview", "eval-set-imports"},
		{"post", "/api/core/eval-sets:import", "eval-set-imports"},
		{"post", "/api/core/eval-sets/{eval_set_id}/imports", "eval-set-imports"},
		{"get", "/api/core/eval-set-import-tasks/{task_id}", "eval-set-imports"},
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
		tags, ok := op["tags"].([]any)
		if !ok || len(tags) == 0 || tags[0] != tc.tag {
			t.Fatalf("expected tag %q for %s %s, got %#v", tc.tag, tc.method, tc.path, op["tags"])
		}
	}

	for _, legacyPath := range []string{"/api/core/qa-datasets", "/api/core/qa-dataset-import-tasks/{task_id}"} {
		if _, ok := paths[legacyPath]; ok {
			t.Fatalf("unexpected legacy qa dataset path in openapi spec: %s", legacyPath)
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

func TestOpenAPISpecIncludesToolOperations(t *testing.T) {
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
		method             string
		path               string
		expectedQueryNames []string
		expectedPathName   string
	}{
		{"get", "/api/core/tools", []string{"keyword", "page", "page_size"}, ""},
		{"post", "/api/core/tools/{tool_name}:disable", nil, "tool_name"},
		{"post", "/api/core/tools/{tool_name}:enable", nil, "tool_name"},
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
		tags, ok := op["tags"].([]any)
		if !ok || len(tags) == 0 || tags[0] != "tools" {
			t.Fatalf("expected tools tag for %s %s, got %#v", tc.method, tc.path, op["tags"])
		}
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
		if len(tc.expectedQueryNames) > 0 {
			params, ok := op["parameters"].([]any)
			if !ok || len(params) == 0 {
				t.Fatalf("parameters missing for %s %s", tc.method, tc.path)
			}
			queryNames := map[string]struct{}{}
			for _, item := range params {
				param, ok := item.(map[string]any)
				if !ok || param["in"] != "query" {
					continue
				}
				name, _ := param["name"].(string)
				queryNames[name] = struct{}{}
			}
			for _, name := range tc.expectedQueryNames {
				if _, ok := queryNames[name]; !ok {
					t.Fatalf("expected query parameter %q for %s %s, got %#v", name, tc.method, tc.path, params)
				}
			}
		}
		if tc.expectedPathName != "" {
			params, ok := op["parameters"].([]any)
			if !ok || len(params) == 0 {
				t.Fatalf("parameters missing for %s %s", tc.method, tc.path)
			}
			found := false
			for _, item := range params {
				param, ok := item.(map[string]any)
				if ok && param["name"] == tc.expectedPathName && param["in"] == "path" {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected %s path parameter for %s %s, got %#v", tc.expectedPathName, tc.method, tc.path, params)
			}
		}
	}

	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatalf("components missing in openapi spec")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("schemas missing in openapi spec")
	}
	groupSchema, ok := schemas["toolGroupOpenAPIResponse"].(map[string]any)
	if !ok {
		t.Fatalf("toolGroupOpenAPIResponse schema missing")
	}
	properties, ok := groupSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("toolGroupOpenAPIResponse properties missing")
	}
	for _, name := range []string{"name", "can_disable", "active", "disabled"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("toolGroupOpenAPIResponse expected property %q", name)
		}
	}
	listSchema, ok := schemas["toolListOpenAPIResponse"].(map[string]any)
	if !ok {
		t.Fatalf("toolListOpenAPIResponse schema missing")
	}
	listProperties, ok := listSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("toolListOpenAPIResponse properties missing")
	}
	for _, name := range []string{"tool_groups", "page", "page_size", "total"} {
		if _, ok := listProperties[name]; !ok {
			t.Fatalf("toolListOpenAPIResponse expected property %q", name)
		}
	}
}

func TestOpenAPISpecIncludesMCPOperations(t *testing.T) {
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
		method             string
		path               string
		requestRef         string
		responseRef        string
		hasIDParam         bool
		expectedQueryNames []string
	}{
		{"get", "/api/core/mcp_servers", "", "#/components/schemas/ListServersResponse", false, []string{"keyword", "page", "page_size"}},
		{"post", "/api/core/mcp_servers", "#/components/schemas/CreateServerRequest", "#/components/schemas/ServerResponse", false, nil},
		{"get", "/api/core/mcp_servers/{id}", "", "#/components/schemas/ServerResponse", true, nil},
		{"patch", "/api/core/mcp_servers/{id}", "#/components/schemas/UpdateServerRequest", "#/components/schemas/ServerResponse", true, nil},
		{"delete", "/api/core/mcp_servers/{id}", "", "#/components/schemas/mcpDeleteServerOpenAPIResponse", true, nil},
		{"post", "/api/core/mcp_servers/{id}:check", "", "#/components/schemas/CheckResponse", true, nil},
		{"post", "/api/core/mcp_servers/{id}:discover", "", "#/components/schemas/DiscoverResponse", true, nil},
		{"put", "/api/core/mcp_servers/{id}/tools", "#/components/schemas/UpdateToolsRequest", "#/components/schemas/ServerResponse", true, nil},
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
		tags, ok := op["tags"].([]any)
		if !ok || len(tags) == 0 || tags[0] != "mcp_servers" {
			t.Fatalf("expected mcp_servers tag for %s %s, got %#v", tc.method, tc.path, op["tags"])
		}
		if tc.hasIDParam {
			params, ok := op["parameters"].([]any)
			if !ok || len(params) == 0 {
				t.Fatalf("parameters missing for %s %s", tc.method, tc.path)
			}
			param, ok := params[0].(map[string]any)
			if !ok || param["name"] != "id" || param["in"] != "path" || param["required"] != true {
				t.Fatalf("expected id path parameter for %s %s, got %#v", tc.method, tc.path, params)
			}
		}
		if len(tc.expectedQueryNames) > 0 {
			params, ok := op["parameters"].([]any)
			if !ok || len(params) == 0 {
				t.Fatalf("parameters missing for %s %s", tc.method, tc.path)
			}
			queryNames := map[string]struct{}{}
			for _, item := range params {
				param, ok := item.(map[string]any)
				if !ok || param["in"] != "query" {
					continue
				}
				name, _ := param["name"].(string)
				queryNames[name] = struct{}{}
			}
			for _, name := range tc.expectedQueryNames {
				if _, ok := queryNames[name]; !ok {
					t.Fatalf("expected query parameter %q for %s %s, got %#v", name, tc.method, tc.path, params)
				}
			}
		}
		if tc.requestRef != "" {
			requestBody, ok := op["requestBody"].(map[string]any)
			if !ok {
				t.Fatalf("requestBody missing for %s %s", tc.method, tc.path)
			}
			content, ok := requestBody["content"].(map[string]any)
			if !ok {
				t.Fatalf("requestBody content missing for %s %s", tc.method, tc.path)
			}
			jsonContent, ok := content["application/json"].(map[string]any)
			if !ok {
				t.Fatalf("application/json requestBody missing for %s %s", tc.method, tc.path)
			}
			schema, ok := jsonContent["schema"].(map[string]any)
			if !ok {
				t.Fatalf("requestBody schema missing for %s %s", tc.method, tc.path)
			}
			if got, _ := schema["$ref"].(string); got != tc.requestRef {
				t.Fatalf("requestBody schema ref for %s %s = %q, want %q", tc.method, tc.path, got, tc.requestRef)
			}
		}
		responses, ok := op["responses"].(map[string]any)
		if !ok {
			t.Fatalf("responses missing for %s %s", tc.method, tc.path)
		}
		resp200, ok := responses["200"].(map[string]any)
		if !ok {
			t.Fatalf("200 response missing for %s %s", tc.method, tc.path)
		}
		content, ok := resp200["content"].(map[string]any)
		if !ok {
			t.Fatalf("response content missing for %s %s", tc.method, tc.path)
		}
		jsonContent, ok := content["application/json"].(map[string]any)
		if !ok {
			t.Fatalf("application/json response missing for %s %s", tc.method, tc.path)
		}
		schema, ok := jsonContent["schema"].(map[string]any)
		if !ok {
			t.Fatalf("response schema missing for %s %s", tc.method, tc.path)
		}
		if got, _ := schema["$ref"].(string); got != tc.responseRef {
			t.Fatalf("response schema ref for %s %s = %q, want %q", tc.method, tc.path, got, tc.responseRef)
		}
	}
}
