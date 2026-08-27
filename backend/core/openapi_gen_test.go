package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/chat"
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
		if skipOpenAPIRoute(path) {
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

func TestOpenAPISpecIncludesSkillMarketDelete(t *testing.T) {
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

	for _, path := range []string{
		"/api/core/admin/skill-market/{market_item_id}",
		"/api/core/skill-market/admin/items/{market_item_id}",
	} {
		op := openAPIOperationForTest(t, spec, "delete", path)
		if got := openAPIObjectResponseRefForTest(t, op); got != "#/components/schemas/marketDeleteOpenAPIResponse" {
			t.Fatalf("DELETE %s response ref = %q", path, got)
		}
		if _, ok := openAPIParameterNamesForTest(t, op)["market_item_id"]; !ok {
			t.Fatalf("DELETE %s missing market_item_id path parameter", path)
		}
	}
}

func TestOpenAPIConversationItemIncludesThinkingDepthEnum(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	specJSON, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatalf("build openapi spec: %v", err)
	}
	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode openapi spec: %v", err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	properties := schemaPropertiesForTest(t, schemas, "ConversationItem")
	depth, ok := properties["thinking_depth"].(map[string]any)
	if !ok {
		t.Fatalf("ConversationItem thinking_depth schema missing: %#v", properties["thinking_depth"])
	}
	rawEnum, ok := depth["enum"].([]any)
	if !ok || !reflect.DeepEqual(rawEnum, []any{"low", "medium", "high", "max"}) {
		t.Fatalf("ConversationItem thinking_depth enum=%#v", depth["enum"])
	}
}

func TestOpenAPIChatEntryDefaultsEnumsMatchValidation(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	specJSON, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatalf("build openapi spec: %v", err)
	}
	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode openapi spec: %v", err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	assertEnum := func(schemaName, propertyName string, want []any) {
		t.Helper()
		properties := schemaPropertiesForTest(t, schemas, schemaName)
		property, ok := properties[propertyName].(map[string]any)
		if !ok || !reflect.DeepEqual(property["enum"], want) {
			t.Fatalf("%s.%s enum=%#v, want %#v", schemaName, propertyName, property["enum"], want)
		}
	}

	thinkingDepths := []any{"low", "medium", "high", "max"}
	workflowModes := []any{"auto", "dynamic"}
	executors := []any{
		chat.ChatExecutorLazyMind,
		chat.ChatExecutorCodex,
		chat.ChatExecutorCursor,
		chat.ChatExecutorWorkBuddy,
	}
	for _, schemaName := range []string{
		"chatEntryDefaultsOpenAPI",
		"chatEntryDefaultsPatchOpenAPIRequest",
	} {
		assertEnum(schemaName, "thinking_depth", thinkingDepths)
	}
	for _, schemaName := range []string{
		"chatConversationDefaultsOpenAPI",
		"chatConversationDefaultsPatchOpenAPIRequest",
	} {
		assertEnum(schemaName, "workflow_mode", workflowModes)
		assertEnum(schemaName, "chat_executor", executors)
	}
}

func TestOpenAPIEpisodeRoutesExposeOnlyPublicContract(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	specJSON, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatalf("build openapi spec: %v", err)
	}
	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode openapi spec: %v", err)
	}
	paths := spec["paths"].(map[string]any)
	for _, operation := range []struct {
		method string
		path   string
	}{
		{"get", "/api/core/memory/episodes"},
		{"get", "/api/core/memory/episodes/{episode_id}"},
		{"delete", "/api/core/memory/episodes/{episode_id}"},
	} {
		openAPIOperationForTest(t, spec, operation.method, operation.path)
	}
	for _, internalPath := range []string{
		"/api/core/internal/memory/episodes",
		"/api/core/internal/memory/episodes/{episode_id}",
		"/api/core/internal/memory/episodes:searchCandidates",
		"/api/core/internal/memory/episodes:listRecent",
		"/api/core/internal/memory/episodes:recordHits",
	} {
		if _, exists := paths[internalPath]; exists {
			t.Fatalf("internal Episode route leaked into OpenAPI: %s", internalPath)
		}
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	episodeProperties := schemaPropertiesForTest(t, schemas, "EpisodeMemory")
	for _, field := range []string{
		"id",
		"conversation_id",
		"source_kind",
		"episode_type",
		"summary",
		"occurred_at_ms",
		"recorded_at_ms",
		"hit_count",
	} {
		if _, exists := episodeProperties[field]; !exists {
			t.Fatalf("EpisodeMemory schema missing field %q", field)
		}
	}
	episodeSchema := schemas["EpisodeMemory"].(map[string]any)
	requiredEpisodeFields, ok := episodeSchema["required"].([]any)
	if !ok || len(requiredEpisodeFields) != len(episodeProperties) {
		t.Fatalf("EpisodeMemory fields must all be required: %#v", episodeSchema)
	}
	for field, expected := range map[string][]string{
		"source_kind":  {"chat_explicit", "memory_review"},
		"episode_type": {"decision", "progress", "result", "blocker", "event"},
	} {
		property := episodeProperties[field].(map[string]any)
		rawEnum, enumOK := property["enum"].([]any)
		if !enumOK || len(rawEnum) != len(expected) {
			t.Fatalf("EpisodeMemory %s enum = %#v", field, property["enum"])
		}
	}
	for _, operation := range []struct {
		method   string
		path     string
		statuses []string
	}{
		{"get", "/api/core/memory/episodes", []string{"200", "400", "401", "500"}},
		{"get", "/api/core/memory/episodes/{episode_id}", []string{"200", "401", "404", "500"}},
		{"delete", "/api/core/memory/episodes/{episode_id}", []string{"204", "401", "500"}},
	} {
		op := openAPIOperationForTest(t, spec, operation.method, operation.path)
		responses := op["responses"].(map[string]any)
		for _, status := range operation.statuses {
			if _, exists := responses[status]; !exists {
				t.Fatalf(
					"Episode operation %s %s missing response %s",
					operation.method,
					operation.path,
					status,
				)
			}
		}
	}
	for _, privateField := range []string{
		"user_id",
		"search_text",
		"tokenizer_version",
		"normalized_summary",
		"lexical_score",
	} {
		if _, exists := episodeProperties[privateField]; exists {
			t.Fatalf("public EpisodeMemory schema must not expose %q", privateField)
		}
	}
}

func TestOpenAPICurrentMemoryContractAndPrivateRouteIsolation(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	specJSON, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatalf("build openapi spec: %v", err)
	}
	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode openapi spec: %v", err)
	}
	for _, operation := range []struct {
		method string
		path   string
	}{
		{"get", "/api/core/memory/soul"},
		{"patch", "/api/core/memory/soul"},
		{"get", "/api/core/memory/soul/avatar"},
		{"put", "/api/core/memory/soul/avatar"},
		{"delete", "/api/core/memory/soul/avatar"},
		{"get", "/api/core/memory/profile"},
		{"patch", "/api/core/memory/profile"},
		{"get", "/api/core/memory/profile/avatar"},
		{"put", "/api/core/memory/profile/avatar"},
		{"delete", "/api/core/memory/profile/avatar"},
		{"get", "/api/core/memory/preferences"},
		{"put", "/api/core/memory/preferences:order"},
		{"get", "/api/core/memory/preferences/{name}"},
		{"delete", "/api/core/memory/preferences/{name}"},
	} {
		op := openAPIOperationForTest(t, spec, operation.method, operation.path)
		if _, tagged := op["tags"]; tagged {
			t.Fatalf(
				"Current Memory operation must remain in DefaultApi: %s %s",
				operation.method,
				operation.path,
			)
		}
	}

	paths := spec["paths"].(map[string]any)
	for path := range paths {
		if strings.HasPrefix(path, "/api/core/internal/") ||
			strings.HasPrefix(path, "/api/core/remote-fs/") {
			t.Fatalf("private Core route leaked into public OpenAPI: %s", path)
		}
	}
	for _, privatePath := range []string{
		"/api/core/internal/workflow-sessions/{session_id}/projection",
		"/api/core/internal/memory/episodes",
		"/api/core/remote-fs/list",
		"/api/core/remote-fs/content",
	} {
		if _, exists := paths[privatePath]; exists {
			t.Fatalf("private Core route leaked into public OpenAPI: %s", privatePath)
		}
	}

	detailOperation := openAPIOperationForTest(
		t,
		spec,
		"get",
		"/api/core/memory/preferences/{name}",
	)
	names := openAPIParameterNamesForTest(t, detailOperation)
	if _, exists := names["name"]; !exists {
		t.Fatalf("Preference detail path parameter must be named name: %#v", names)
	}

	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	documentSchema := schemas["CurrentMemoryDocument"].(map[string]any)
	documentValue := documentSchema["additionalProperties"].(map[string]any)
	documentValueOptions := documentValue["oneOf"].([]any)
	nullableString := documentValueOptions[0].(map[string]any)
	if nullableString["type"] != "string" || nullableString["nullable"] != true {
		t.Fatalf(
			"CurrentMemoryDocument null must be declared by a nullable string branch: %#v",
			documentValueOptions,
		)
	}
	for name := range schemas {
		if strings.HasPrefix(strings.ToLower(name), "remotefs") {
			t.Fatalf("private RemoteFS schema leaked into public OpenAPI: %s", name)
		}
	}
	soulData := schemaPropertiesForTest(t, schemas, "CurrentMemorySoulData")
	if _, exists := soulData["etag"]; exists {
		t.Fatal("Soul response must not expose etag")
	}
	soulUpdatedAt := soulData["updated_at"].(map[string]any)
	if soulUpdatedAt["type"] != "integer" || soulUpdatedAt["format"] != "int64" {
		t.Fatalf("Soul updated_at must be epoch milliseconds: %#v", soulUpdatedAt)
	}
	for _, field := range []string{"document", "template_version", "presentation"} {
		if _, exists := soulData[field]; !exists {
			t.Fatalf("Soul response missing %s", field)
		}
	}
	profileData := schemaPropertiesForTest(t, schemas, "CurrentMemoryProfileData")
	if _, exists := profileData["etag"]; exists {
		t.Fatal("Profile response must not expose etag")
	}
	profileUpdatedAt := profileData["updated_at"].(map[string]any)
	if profileUpdatedAt["type"] != "integer" || profileUpdatedAt["format"] != "int64" {
		t.Fatalf("Profile updated_at must be epoch milliseconds: %#v", profileUpdatedAt)
	}
	for _, field := range []string{"document", "template_version", "presentation"} {
		if _, exists := profileData[field]; !exists {
			t.Fatalf("Profile response missing %s", field)
		}
	}
	for _, removed := range []string{
		"CurrentMemorySoulIdentity",
		"CurrentMemorySoulMission",
		"CurrentMemorySoulInteraction",
		"CurrentMemorySoulEpistemic",
		"CurrentMemorySoulDocument",
		"CurrentMemoryProfileIdentity",
		"CurrentMemoryProfileLocale",
		"CurrentMemoryProfileProfessional",
		"CurrentMemoryProfileDocument",
	} {
		if _, exists := schemas[removed]; exists {
			t.Fatalf("fixed business schema %s must not remain in OpenAPI", removed)
		}
	}
	avatarData := schemaPropertiesForTest(t, schemas, "CurrentMemoryAvatarData")
	if _, exists := avatarData["updated_at"]; !exists {
		t.Fatal("Avatar response missing updated_at")
	}
	preferenceList := schemaPropertiesForTest(
		t,
		schemas,
		"CurrentMemoryPreferenceListData",
	)
	preferenceUpdatedAt := preferenceList["updated_at"].(map[string]any)
	if preferenceUpdatedAt["type"] != "integer" ||
		preferenceUpdatedAt["format"] != "int64" {
		t.Fatalf(
			"Preference list updated_at must be epoch milliseconds: %#v",
			preferenceUpdatedAt,
		)
	}
	residentUsage := preferenceList["resident_index_usage"].(map[string]any)
	if residentUsage["$ref"] !=
		"#/components/schemas/CurrentMemoryPreferenceResidentIndexUsage" {
		t.Fatalf("Preference resident usage schema = %#v", residentUsage)
	}
	publicItem := schemaPropertiesForTest(t, schemas, "CurrentMemoryPreferenceItem")
	if _, exists := publicItem["ref"]; exists {
		t.Fatal("public Preference item must not expose ref")
	}
	orderRequest := schemaPropertiesForTest(
		t,
		schemas,
		"CurrentMemoryPreferenceOrderRequest",
	)
	if _, exists := orderRequest["expected_etag"]; !exists {
		t.Fatal("Preference reorder request missing expected_etag")
	}
	if _, exists := orderRequest["etag"]; exists {
		t.Fatal("Preference reorder request must not expose an etag field")
	}
	detailData := schemaPropertiesForTest(
		t,
		schemas,
		"CurrentMemoryPreferenceDetailData",
	)
	referenceSchema := detailData["reference"].(map[string]any)
	if nullable, _ := referenceSchema["nullable"].(bool); !nullable {
		t.Fatalf("Preference reference must be nullable: %#v", referenceSchema)
	}
	allOf, ok := referenceSchema["allOf"].([]any)
	if !ok || len(allOf) != 1 {
		t.Fatalf(
			"Preference nullable reference must wrap its component reference: %#v",
			referenceSchema,
		)
	}
	operationsRequest := schemaPropertiesForTest(
		t,
		schemas,
		"CurrentMemoryOperationsRequest",
	)
	operations := operationsRequest["operations"].(map[string]any)
	items := operations["items"].(map[string]any)
	if items["$ref"] != "#/components/schemas/CurrentMemoryOperation" {
		t.Fatalf("operations items schema = %#v", items)
	}
	operation := schemaPropertiesForTest(
		t,
		schemas,
		"CurrentMemoryOperation",
	)
	if _, exists := operation["value"]; !exists {
		t.Fatal("CurrentMemoryOperation must expose an optional value")
	}
}

func TestOpenAPISpecIncludesDatasetSourceFilter(t *testing.T) {
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

	op := openAPIOperationForTest(t, spec, "get", "/api/core/datasets")
	params := openAPIParameterNamesForTest(t, op)
	if _, ok := params["source"]; !ok {
		t.Fatalf("dataset list must document the source query parameter")
	}
	sourceSchema := openAPIParameterSchemaForTest(t, op, "source")
	if !reflect.DeepEqual(sourceSchema["enum"], []any{"manual", "cloud", "official_installed"}) {
		t.Fatalf("unexpected dataset source values: %#v", sourceSchema["enum"])
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	datasetProps := schemaPropertiesForTest(t, schemas, "Dataset")
	if _, ok := datasetProps["source_type"]; !ok {
		t.Fatalf("Dataset schema must document source_type")
	}
}

func TestOpenAPISpecIncludesKnowledgeMarketSurface(t *testing.T) {
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

	installOp := openAPIOperationForTest(t, spec, "post", "/api/core/knowledge-market/items/{market_item_id}:install")
	if got := openAPIObjectResponseRefForTest(t, installOp); got != "#/components/schemas/knowledgeMarketInstallOpenAPIResponse" {
		t.Fatalf("install response ref = %q", got)
	}

	tasksOp := openAPIOperationForTest(t, spec, "get", "/api/core/knowledge-market/tasks")
	if got := openAPIObjectResponseRefForTest(t, tasksOp); got != "#/components/schemas/knowledgeMarketTaskListOpenAPIResponse" {
		t.Fatalf("tasks list response ref = %q", got)
	}
	statusSchema := openAPIParameterSchemaForTest(t, tasksOp, "status")
	if !reflect.DeepEqual(statusSchema["enum"], []any{"pending", "running", "succeeded", "failed", "canceled"}) {
		t.Fatalf("unexpected tasks status values: %#v", statusSchema["enum"])
	}

	taskOp := openAPIOperationForTest(t, spec, "get", "/api/core/knowledge-market/tasks/{job_id}")
	if got := openAPIObjectResponseRefForTest(t, taskOp); got != "#/components/schemas/knowledgeMarketTaskDetailOpenAPIResponse" {
		t.Fatalf("task detail response ref = %q", got)
	}
	taskParams := openAPIParameterNamesForTest(t, taskOp)
	if _, ok := taskParams["job_id"]; !ok {
		t.Fatalf("task detail must document the job_id path parameter")
	}

	installsOp := openAPIOperationForTest(t, spec, "get", "/api/core/knowledge-market/installs")
	if got := openAPIObjectResponseRefForTest(t, installsOp); got != "#/components/schemas/knowledgeMarketInstallsOpenAPIResponse" {
		t.Fatalf("installs response ref = %q", got)
	}

	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	listProps := schemaPropertiesForTest(t, schemas, "knowledgeMarketListItemOpenAPIResponse")
	if _, ok := listProps["doc_count"]; ok {
		t.Fatalf("list item schema must not document doc_count")
	}
	detailProps := schemaPropertiesForTest(t, schemas, "knowledgeMarketDetailOpenAPIResponse")
	for _, stale := range []string{"package_sha256", "package_size", "doc_count", "files"} {
		if _, ok := detailProps[stale]; ok {
			t.Fatalf("detail schema must not document %s", stale)
		}
	}
	if _, ok := detailProps["package_revision"]; !ok {
		t.Fatalf("detail schema must document package_revision")
	}
}

func TestOpenAPISpecChatChunkDeltaModeValues(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)

	specJSON, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatalf("build openapi spec: %v", err)
	}
	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		t.Fatalf("decode openapi spec: %v", err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	properties := schemaPropertiesForTest(t, schemas, "ChatChunkResponse")
	deltaMode, ok := properties["delta_mode"].(map[string]any)
	if !ok {
		t.Fatalf("ChatChunkResponse delta_mode property = %#v", properties["delta_mode"])
	}
	if !reflect.DeepEqual(deltaMode["enum"], []any{"append", "replace"}) {
		t.Fatalf("unexpected delta_mode values: %#v", deltaMode["enum"])
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
	for _, schemaName := range []string{"skillRevisionOpenAPIResponse"} {
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

func TestOpenAPISpecPromptFacetsIncludeCategoryTotal(t *testing.T) {
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
	for _, schemaName := range []string{"promptFacetOpenAPIResponse", "PromptFacets"} {
		properties := schemaPropertiesForTest(t, schemas, schemaName)
		categoryTotal, ok := properties["category_total"].(map[string]any)
		if !ok || categoryTotal["type"] != "integer" || categoryTotal["format"] != "int64" {
			t.Fatalf("schema %s category_total property = %#v, want int64", schemaName, properties["category_total"])
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

func TestOpenAPISpecUsesGatewaySafeExternalChatProviderRoutes(t *testing.T) {
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
	for _, route := range []struct {
		method string
		path   string
	}{
		{"get", "/api/core/external-chat/hosts/{provider}/status"},
		{"post", "/api/core/external-chat/hosts/{provider}/claim"},
		{"get", "/api/core/external-chat/providers/{provider}/sessions"},
		{"post", "/api/core/external-chat/providers/{provider}/sessions/{thread_id}/binding"},
		{"post", "/api/core/external-chat/providers/{provider}/sessions:sync"},
	} {
		openAPIOperationForTest(t, spec, route.method, route.path)
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
		{"get", "/api/core/skill-market/tags", false, false, true},
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
		{"get", "/api/core/user/chat-settings", false, false, true},
		{"patch", "/api/core/user/chat-settings", true, false, true},
		{"get", "/api/core/user/ui-preferences", false, false, true},
		{"patch", "/api/core/user/ui-preferences", true, false, true},
		{"get", "/api/core/skill-review:summary", false, false, false},
		{"post", "/api/core/skill-review:run", false, false, false},
		{"get", "/api/core/skill-review/tasks", false, false, false},
		{"get", "/api/core/agent/threads", false, true, true},
		{"get", "/api/core/conversations/{name}:history", false, true, true},
		{"get", "/api/core/conversations/{name}:trail", false, true, true},
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
		"/api/core/skill-review-results/{review_result_id}",
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

	trailItem, ok := paths["/api/core/conversations/{name}:trail"].(map[string]any)
	if !ok {
		t.Fatalf("path missing: /api/core/conversations/{name}:trail")
	}
	trailGet, ok := trailItem["get"].(map[string]any)
	if !ok {
		t.Fatalf("get operation missing for conversation trail")
	}
	trailParams, ok := trailGet["parameters"].([]any)
	if !ok {
		t.Fatalf("parameters missing for conversation trail")
	}
	trailParamNames := make(map[string]string, len(trailParams))
	for _, item := range trailParams {
		p, ok := item.(map[string]any)
		if !ok {
			continue
		}
		trailParamNames[p["name"].(string)] = p["in"].(string)
	}
	for _, want := range []struct{ name, inVal string }{
		{"name", "path"},
		{"page_size", "query"},
		{"page_token", "query"},
	} {
		if got, ok := trailParamNames[want.name]; !ok || got != want.inVal {
			t.Fatalf("expected trail parameter %q in %q, got %q (%v)", want.name, want.inVal, got, trailParamNames)
		}
	}
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
	for _, name := range []string{"chat_preference_notice_dismissed", "developer_mode_active", "schedules_enabled", "skills_enabled", "workflows_enabled"} {
		if _, ok := properties[name]; !ok {
			t.Fatalf("userUIPreferencesPatchOpenAPIRequest expected property %q", name)
		}
	}
	if required, ok := schema["required"].([]any); ok && len(required) > 0 {
		t.Fatalf("userUIPreferencesPatchOpenAPIRequest fields should all be optional, got required=%v", required)
	}

	chatPatch, ok := schemas["userChatSettingsPatchOpenAPIRequest"].(map[string]any)
	if !ok {
		t.Fatal("userChatSettingsPatchOpenAPIRequest missing")
	}
	chatPatchProperties, ok := chatPatch["properties"].(map[string]any)
	if !ok {
		t.Fatal("userChatSettingsPatchOpenAPIRequest properties missing")
	}
	for _, name := range []string{"enable_workflow", "workflow_mode", "enable_subagent", "quick_question", "new_task"} {
		if _, ok := chatPatchProperties[name]; !ok {
			t.Fatalf("userChatSettingsPatchOpenAPIRequest expected property %q", name)
		}
	}
	if required, ok := chatPatch["required"].([]any); ok && len(required) > 0 {
		t.Fatalf("userChatSettingsPatchOpenAPIRequest fields should all be optional, got required=%v", required)
	}
	chatResponse, ok := schemas["userChatSettingsOpenAPIResponse"].(map[string]any)
	if !ok {
		t.Fatal("userChatSettingsOpenAPIResponse missing")
	}
	chatResponseProperties, ok := chatResponse["properties"].(map[string]any)
	if !ok {
		t.Fatal("userChatSettingsOpenAPIResponse properties missing")
	}
	for _, name := range []string{"quick_question", "new_task"} {
		if _, ok := chatResponseProperties[name]; !ok {
			t.Fatalf("userChatSettingsOpenAPIResponse expected property %q", name)
		}
	}
}

func TestOpenAPISpecIncludesLLMAndVLMMaxInputTokens(t *testing.T) {
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
	itemSchema, ok := schemas["listModelProviderGroupModelsOpenAPIItem"].(map[string]any)
	if !ok {
		t.Fatalf("listModelProviderGroupModelsOpenAPIItem schema missing")
	}
	properties, ok := itemSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("listModelProviderGroupModelsOpenAPIItem properties missing")
	}
	maxInputTokens, ok := properties["max_input_tokens"].(map[string]any)
	if !ok {
		t.Fatalf("max_input_tokens property missing")
	}
	if got := maxInputTokens["type"]; got != "string" {
		t.Fatalf("max_input_tokens type = %v, want string", got)
	}
	if got := maxInputTokens["nullable"]; got != true {
		t.Fatalf("max_input_tokens nullable = %v, want true", got)
	}

	selectedItemSchema, ok := schemas["selectedModelOpenAPIItem"].(map[string]any)
	if !ok {
		t.Fatalf("selectedModelOpenAPIItem schema missing")
	}
	selectedProperties, ok := selectedItemSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("selectedModelOpenAPIItem properties missing")
	}
	selectedMaxInputTokens, ok := selectedProperties["max_input_tokens"].(map[string]any)
	if !ok {
		t.Fatalf("selected models max_input_tokens property missing")
	}
	if got := selectedMaxInputTokens["type"]; got != "string" {
		t.Fatalf("selected models max_input_tokens type = %v, want string", got)
	}
	if got := selectedMaxInputTokens["nullable"]; got != true {
		t.Fatalf("selected models max_input_tokens nullable = %v, want true", got)
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

func TestOpenAPIShowcaseCaseIncludesSkillSourceURL(t *testing.T) {
	schemas := generatedOpenAPISchemas(t)
	schema := schemas["ShowcaseCase"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	if sourceURL, ok := properties["source_url"].(map[string]any); !ok || sourceURL["type"] != "string" {
		t.Fatalf("ShowcaseCase source_url = %#v, want required string", properties["source_url"])
	}
	if provider, ok := properties["provider"].(map[string]any); !ok || provider["type"] != "string" {
		t.Fatalf("ShowcaseCase provider = %#v, want required string", properties["provider"])
	}
	required := schema["required"].([]any)
	foundSourceURL := false
	foundProvider := false
	for _, field := range required {
		switch field {
		case "source_url":
			foundSourceURL = true
		case "provider":
			foundProvider = true
		}
	}
	if !foundSourceURL || !foundProvider {
		t.Fatalf("ShowcaseCase required fields = %#v", required)
	}
}

func generatedOpenAPISchemas(t *testing.T) map[string]any {
	t.Helper()
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
	return spec["components"].(map[string]any)["schemas"].(map[string]any)
}

func TestOpenAPIBuiltinSkillIncludesOptionalProvider(t *testing.T) {
	schemas := generatedOpenAPISchemas(t)
	schema := schemas["builtinSkillOpenAPIResponse"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	provider, ok := properties["provider"].(map[string]any)
	if !ok || provider["type"] != "string" {
		t.Fatalf("builtin provider = %#v, want optional string", properties["provider"])
	}
	for _, name := range schema["required"].([]any) {
		if name == "provider" {
			t.Fatal("builtin provider must remain optional for old catalogs")
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
		{"patch", "/api/core/mcp_servers:enabled", "#/components/schemas/BulkUpdateServerEnabledRequest", "#/components/schemas/BulkUpdateServerEnabledResponse", false, nil},
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
