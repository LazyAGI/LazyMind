package main

import (
	"encoding/json"
	"reflect"
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

func TestOpenAPISpecRevisionSchemasIncludeHeadMarker(t *testing.T) {
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
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	for _, schemaName := range []string{"RevisionSummary", "skillRevisionOpenAPIResponse"} {
		schema, ok := schemas[schemaName].(map[string]any)
		if !ok {
			t.Fatalf("schema %s missing", schemaName)
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("schema %s properties missing", schemaName)
		}
		isHead, ok := properties["is_head"].(map[string]any)
		if !ok || isHead["type"] != "boolean" {
			t.Fatalf("schema %s is_head property = %#v, want boolean", schemaName, properties["is_head"])
		}
	}
}

func TestOpenAPISpecIncludesAgentEvoContracts(t *testing.T) {
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
	for _, tc := range []struct {
		method string
		path   string
	}{
		{"get", "/api/core/agent/threads/{thread_id}/events:stream"},
		{"get", "/api/core/agent/threads/{thread_id}/event-trace:stream"},
		{"get", "/api/core/agent/threads/{thread_id}/steps"},
		{"get", "/api/core/agent/threads/{thread_id}/gates"},
		{"get", "/api/core/agent/threads/{thread_id}/gates/{step}/versions/{version}"},
		{"get", "/api/core/agent/threads/{thread_id}/gates/{step}/versions/{version}:download"},
		{"get", "/api/core/agent/threads/{thread_id}/results/traces:compare"},
		{"get", "/api/core/agent/threads/{thread_id}/results/traces/{trace_id}"},
		{"get", "/api/core/agent/threads/{thread_id}/messages"},
		{"post", "/api/core/agent/threads/{thread_id}/messages"},
		{"post", "/api/core/agent/threads/{thread_id}/start"},
		{"post", "/api/core/agent/threads/{thread_id}/pause"},
		{"post", "/api/core/agent/threads/{thread_id}/cancel"},
		{"post", "/api/core/agent/threads/{thread_id}/retry"},
		{"post", "/api/core/agent/threads/{thread_id}/continue"},
		{"get", "/api/core/agent/candidates"},
		{"get", "/api/core/agent/candidates/{candidate_id:.*}"},
		{"get", "/api/core/agent/router/status"},
		{"get", "/api/core/agent/router/algorithms"},
		{"post", "/api/core/agent/router/algorithms"},
		{"post", "/api/core/agent/router/algorithms/{algorithm_id}/action"},
		{"delete", "/api/core/agent/router/algorithms/{algorithm_id}"},
		{"get", "/api/core/agent/router/ab-strategy"},
		{"put", "/api/core/agent/router/ab-strategy"},
	} {
		openAPIOperationForTest(t, spec, tc.method, tc.path)
	}

	eventTraceOp := openAPIOperationForTest(t, spec, "get", "/api/core/agent/threads/{thread_id}/event-trace:stream")
	eventTraceParams := openAPIParameterNamesForTest(t, eventTraceOp)
	if _, ok := eventTraceParams["step_id"]; !ok {
		t.Fatalf("event trace stream must document required step_id query")
	}

	gateOp := openAPIOperationForTest(t, spec, "get", "/api/core/agent/threads/{thread_id}/gates/{step}/versions/{version}")
	gateParams := openAPIParameterNamesForTest(t, gateOp)
	for _, name := range []string{"thread_id", "step", "version"} {
		if _, ok := gateParams[name]; !ok {
			t.Fatalf("gate operation missing parameter %q", name)
		}
	}
	gateSchema := openAPIResponseSchemaForTest(t, gateOp)
	if gateSchema["type"] != "object" || gateSchema["additionalProperties"] != true {
		t.Fatalf("gate response should document direct Evo object, got %#v", gateSchema)
	}

	downloadOp := openAPIOperationForTest(t, spec, "get", "/api/core/agent/threads/{thread_id}/gates/{step}/versions/{version}:download")
	formatSchema := openAPIParameterSchemaForTest(t, downloadOp, "format")
	if !reflect.DeepEqual(formatSchema["enum"], []any{"json"}) {
		t.Fatalf("download format enum mismatch: %#v", formatSchema["enum"])
	}
	responses := downloadOp["responses"].(map[string]any)
	response200 := responses["200"].(map[string]any)
	content := response200["content"].(map[string]any)
	binaryContent, ok := content["application/octet-stream"].(map[string]any)
	if !ok {
		t.Fatalf("download operation should expose application/octet-stream response, got %#v", content)
	}
	binarySchema := binaryContent["schema"].(map[string]any)
	if binarySchema["type"] != "string" || binarySchema["format"] != "binary" {
		t.Fatalf("unexpected download response schema: %#v", binarySchema)
	}

	traceCompareOp := openAPIOperationForTest(t, spec, "get", "/api/core/agent/threads/{thread_id}/results/traces:compare")
	traceCompareParams := openAPIParameterNamesForTest(t, traceCompareOp)
	for _, name := range []string{"thread_id", "a", "b"} {
		if _, ok := traceCompareParams[name]; !ok {
			t.Fatalf("trace compare operation missing parameter %q", name)
		}
	}

	paths := spec["paths"].(map[string]any)
	for _, gateDetailPath := range []string{
		"/api/core/agent/threads/{thread_id}/gates/eval/versions/{version}/bad-cases",
		"/api/core/agent/threads/{thread_id}/gates/abtest/versions/{version}/case-details",
	} {
		if _, ok := paths[gateDetailPath]; !ok {
			t.Fatalf("gate detail path missing from openapi spec: %s", gateDetailPath)
		}
	}
	for _, legacyPath := range []string{
		"/api/core/agent/threads/{thread_id}:events",
		"/api/core/agent/threads/{thread_id}:messages",
		"/api/core/agent/threads/{thread_id}:start",
		"/api/core/agent/threads/{thread_id}:pause",
		"/api/core/agent/threads/{thread_id}:cancel",
		"/api/core/agent/threads/{thread_id}:retry",
		"/api/core/agent/threads/{thread_id}:continue",
		"/api/core/agent/threads/{thread_id}:history",
		"/api/core/agent/threads/{thread_id}/rounds",
		"/api/core/agent/threads/{thread_id}/records",
		"/api/core/agent/threads/{thread_id}/steps/{step_id}/records",
		"/api/core/agent/threads/{thread_id}/results/eval-reports/{report_id}/bad-cases",
		"/api/core/agent/threads/{thread_id}/results/{kind}:download",
		"/api/core/agent/threads/{thread_id}/results/datasets",
		"/api/core/agent/threads/{thread_id}/results/abtests/{abtest_id}/case-details",
		"/api/core/agent/threads/{thread_id}/results/traces-compare",
		"/api/core/agent/reports/{report_id}:content",
		"/api/core/agent/diffs/{apply_id}/{filename:.*}",
		"/api/core/agent/files:content",
	} {
		if _, ok := paths[legacyPath]; ok {
			t.Fatalf("legacy agent result path still present in openapi spec: %s", legacyPath)
		}
	}

	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	for _, legacySchema := range []string{
		"agentABTestCaseDetailListItemOpenAPIResponse",
		"agentABTestCaseDetailListOpenAPIResponse",
		"agentABTestResultOpenAPIResponse",
		"agentEvalReportBadCaseListItemOpenAPIResponse",
		"agentEvalReportBadCaseListOpenAPIResponse",
		"agentEvalReportResultOpenAPIResponse",
		"agentTraceCompareOpenAPIResponse",
		"agentTraceDetailOpenAPIResponse",
		"agentTraceSummaryOpenAPIResponse",
	} {
		if _, ok := schemas[legacySchema]; ok {
			t.Fatalf("legacy agent result schema still present in openapi spec: %s", legacySchema)
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

func openAPIParameterSchemaForTest(t *testing.T, op map[string]any, name string) map[string]any {
	t.Helper()
	items, ok := op["parameters"].([]any)
	if !ok {
		t.Fatalf("parameters missing")
	}
	for _, item := range items {
		param, ok := item.(map[string]any)
		if !ok || param["name"] != name {
			continue
		}
		schema, ok := param["schema"].(map[string]any)
		if !ok {
			t.Fatalf("parameter %q schema missing", name)
		}
		return schema
	}
	t.Fatalf("parameter %q missing", name)
	return nil
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
		{"get", "/api/core/user/ui-preferences", false, false, true},
		{"patch", "/api/core/user/ui-preferences", true, false, true},
		{"patch", "/api/core/personal-resource/{resource_type}", true, true, true},
		{"get", "/api/core/personal-resource/{resource_type}:file", false, true, true},
		{"put", "/api/core/personal-resource/{resource_type}:file", true, true, true},
		{"put", "/api/core/personal-resource/{resource_type}:draft", true, true, true},
		{"get", "/api/core/personal-resource/{resource_type}:draft-preview", false, true, true},
		{"post", "/api/core/personal-resource/{resource_type}:generate", true, true, true},
		{"post", "/api/core/personal-resource/{resource_type}/draft-review/{review_id}/actions", true, true, true},
		{"post", "/api/core/personal-resource/{resource_type}/draft-review/{review_id}:undo", true, true, true},
		{"post", "/api/core/personal-resource/{resource_type}:commit", true, true, true},
		{"post", "/api/core/personal-resource/{resource_type}:discard", false, true, true},
		{"get", "/api/core/personal-resource/{resource_type}/revisions", false, true, true},
		{"get", "/api/core/personal-resource/{resource_type}/revisions/{revision_id}", false, true, true},
		{"post", "/api/core/personal-resource/{resource_type}:rollback", true, true, true},
		{"get", "/api/core/skill-review:summary", false, false, false},
		{"post", "/api/core/skill-review:run", false, false, false},
		{"get", "/api/core/skill-review/tasks", false, false, false},
		{"get", "/api/core/skill-review-results/{review_result_id}", false, false, false},
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
		"/api/core/memory",
		"/api/core/memory:draft-preview",
		"/api/core/memory:generate",
		"/api/core/memory:confirm",
		"/api/core/memory:discard",
		"/api/core/user-preference",
		"/api/core/user-preference:draft-preview",
		"/api/core/user-preference:generate",
		"/api/core/user-preference:confirm",
		"/api/core/user-preference:discard",
		"/api/core/skill-review-results",
		"/api/core/skill-review-results/{review_result_id}:accept",
		"/api/core/skill-review-results/{review_result_id}:reject",
		"/api/core/memory-review-results",
		"/api/core/resource-versions",
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

func TestOpenAPISpecAssignsMetadataFieldsToPersonalResourcePatch(t *testing.T) {
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

	draftRequestProps := schemaProperties("personalResourceWriteDraftOpenAPIRequest")
	for _, name := range []string{"content", "expected_draft_version"} {
		if _, ok := draftRequestProps[name]; !ok {
			t.Fatalf("personalResourceWriteDraftOpenAPIRequest expected property %q", name)
		}
	}
	for _, name := range []string{"agent_persona", "preferred_name", "response_style"} {
		if _, ok := draftRequestProps[name]; ok {
			t.Fatalf("personalResourceWriteDraftOpenAPIRequest must not include property %q", name)
		}
	}
	patchRequestProps := schemaProperties("personalResourcePatchOpenAPIRequest")
	for _, name := range []string{"auto_evo", "agent_persona", "preferred_name", "response_style"} {
		if _, ok := patchRequestProps[name]; !ok {
			t.Fatalf("personalResourcePatchOpenAPIRequest expected property %q", name)
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

	assertRequestSchemaRef("/api/core/personal-resource/{resource_type}:file", "put", "#/components/schemas/personalResourceWriteDraftOpenAPIRequest")
	assertRequestSchemaRef("/api/core/personal-resource/{resource_type}:draft", "put", "#/components/schemas/personalResourceWriteDraftOpenAPIRequest")
}

func TestOpenAPISpecMarksUIPreferencesPatchFieldsOptional(t *testing.T) {
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
	schema, ok := schemas["userUIPreferencesPatchOpenAPIRequest"].(map[string]any)
	if !ok {
		t.Fatalf("userUIPreferencesPatchOpenAPIRequest missing")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("userUIPreferencesPatchOpenAPIRequest properties missing")
	}
	for _, name := range []string{"chat_preference_notice_dismissed", "developer_mode_active"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("userUIPreferencesPatchOpenAPIRequest expected property %q", name)
		}
	}
	if required, ok := schema["required"].([]any); ok && len(required) > 0 {
		t.Fatalf("userUIPreferencesPatchOpenAPIRequest fields should all be optional, got required=%v", required)
	}
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
		{"get", "/api/core/eval-sets/{eval_set_id}/items:invalidReferences", "eval-set-items"},
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

func TestOpenAPISpecIncludesLocaleHeaderForLocalizedCatalogs(t *testing.T) {
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
	paths := spec["paths"].(map[string]any)
	for _, path := range []string{
		"/api/core/tools",
		"/api/core/model_providers",
		"/api/core/model_providers:with_groups",
	} {
		pathItem, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("path missing from openapi spec: %s", path)
		}
		op, ok := pathItem["get"].(map[string]any)
		if !ok {
			t.Fatalf("GET operation missing from openapi spec: %s", path)
		}
		found := false
		for _, raw := range op["parameters"].([]any) {
			parameter, ok := raw.(map[string]any)
			if ok && parameter["in"] == "header" && parameter["name"] == "Accept-Language" {
				found = true
				if parameter["required"] != false {
					t.Fatalf("Accept-Language should be optional for %s", path)
				}
			}
		}
		if !found {
			t.Fatalf("Accept-Language header missing for %s", path)
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
