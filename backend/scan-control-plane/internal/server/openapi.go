package server

import (
	"encoding/json"
	"net/http"
	"strings"

	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
	"gopkg.in/yaml.v3"
)

const (
	scanFrontendPrefix  = "/api/scan"
	scanOpenAPIJSONPath = scanFrontendPrefix + "/openapi.json"
	openAPIJSONPath     = "/openapi.json"
)

func (h *Handler) docs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	specURL := openAPIJSONPath
	if strings.HasPrefix(strings.TrimSpace(r.URL.Path), scanFrontendPrefix) {
		specURL = scanOpenAPIJSONPath
	}
	_, _ = w.Write([]byte(docsHTML(specURL)))
}

func (h *Handler) openapi(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, OpenAPISpec())
}

func (h *Handler) openapiYAML(w http.ResponseWriter, _ *http.Request) {
	body, err := yaml.Marshal(OpenAPISpec())
	if err != nil {
		writeError(w, &sourceengine.EngineError{Code: sourceengine.ErrCodeInternal, Message: "marshal OpenAPI yaml failed"})
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func docsHTML(specURL string) string {
	specURLJSON, _ := json.Marshal(specURL)
	payload, _ := json.Marshal(map[string]any{
		"title": "Scan Control Plane API - Swagger UI",
	})
	return "<!DOCTYPE html>\n" +
		"<html lang=\"zh-CN\"><head><meta charset=\"UTF-8\">" +
		"<title>Scan Control Plane API - Swagger UI</title>" +
		"<link rel=\"stylesheet\" href=\"https://unpkg.com/swagger-ui-dist@5.11.0/swagger-ui.css\">" +
		"</head><body><div id=\"swagger-ui\"></div>" +
		"<script src=\"https://unpkg.com/swagger-ui-dist@5.11.0/swagger-ui-bundle.js\"></script>" +
		"<script src=\"https://unpkg.com/swagger-ui-dist@5.11.0/swagger-ui-standalone-preset.js\"></script>" +
		"<script>window.__META__=" + string(payload) + ";" +
		"window.onload=function(){window.ui=SwaggerUIBundle({url:" + string(specURLJSON) + ",dom_id:'#swagger-ui',presets:[SwaggerUIBundle.presets.apis,SwaggerUIStandalonePreset],layout:'StandaloneLayout'});};</script>" +
		"</body></html>"
}

func OpenAPISpec() map[string]any {
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "scan-control-plane",
			"version": "0.3.0",
		},
		"paths":      openAPIPaths(),
		"components": map[string]any{"schemas": openAPISchemas()},
	}
}

func openAPIPaths() map[string]any {
	return map[string]any{
		"/api/scan/connectors": map[string]any{
			"get": operation("listConnectors", "", "ConnectorListResponse"),
		},
		"/api/scan/binding-targets/tree/children": map[string]any{
			"post": operation("listBindingTargetChildren", "BindingTargetChildrenRequest", "TreeNodePage"),
		},
		"/api/scan/binding-targets/tree/search": map[string]any{
			"post": operation("searchBindingTargets", "BindingTargetSearchRequest", "TreeNodePage"),
		},
		"/api/scan/binding-targets/validate": map[string]any{
			"post": operation("validateBindingTarget", "ValidateBindingTargetRequest", "ValidateBindingTargetResponse"),
		},
		"/api/scan/sources": map[string]any{
			"post": createdOperation("createSource", "CreateSourceRequest", "CreateSourceResponse"),
			"get":  withQueryParameters(operation("listSources", "", "SourceListResponse"), sourceListQueryParameters()),
		},
		"/api/scan/sources/{source_id}": map[string]any{
			"get":    pathOperation("getSource", "", "GetSourceResponse", "source_id"),
			"put":    pathOperation("updateSource", "UpdateSourceRequest", "UpdateSourceResponse", "source_id"),
			"delete": pathOperation("deleteSource", "", "DeleteSourceResponse", "source_id"),
		},
		"/api/scan/sources/{source_id}/bindings": map[string]any{
			"post": createdPathOperation("createSourceBinding", "SourceBindingRequest", "BindingMutationResponse", "source_id"),
		},
		"/api/scan/sources/{source_id}/bindings/{binding_id}": map[string]any{
			"put":    pathOperation("updateSourceBinding", "SourceBindingRequest", "BindingMutationResponse", "source_id", "binding_id"),
			"delete": pathOperation("deleteSourceBinding", "", "DeleteBindingResponse", "source_id", "binding_id"),
		},
		"/api/scan/sources/{source_id}/sync": map[string]any{
			"post": pathOperation("triggerSourceSync", "TriggerSourceSyncRequest", "TriggerSourceSyncResponse", "source_id"),
		},
		"/api/scan/sources/{source_id}/tree/children": map[string]any{
			"post": pathOperation("listSourceTreeChildren", "SourceTreeChildrenRequest", "TreeNodePage", "source_id"),
		},
		"/api/scan/sources/{source_id}/tree/search": map[string]any{
			"post": pathOperation("searchSourceTree", "SourceTreeSearchRequest", "TreeNodePage", "source_id"),
		},
		"/api/scan/sources/{source_id}/documents": map[string]any{
			"get": withQueryParameters(pathOperation("listSourceDocuments", "", "SourceDocumentListResponse", "source_id"), documentQueryParameters()),
		},
		"/api/scan/sources/{source_id}/summary": map[string]any{
			"get": pathOperation("getSourceSummary", "", "SourceSummaryResponse", "source_id"),
		},
		"/api/scan/sources/{source_id}/tasks/generate": map[string]any{
			"post": pathOperation("generateParseTasks", "GenerateTasksRequest", "GenerateTasksResponse", "source_id"),
		},
		"/api/scan/sources/{source_id}/tasks/expedite": map[string]any{
			"post": pathOperation("expediteParseTasks", "ExpediteTasksRequest", "ExpediteTasksResponse", "source_id"),
		},
		"/api/scan/parse-tasks": map[string]any{
			"get": withQueryParameters(operation("listParseTasks", "", "ParseTaskListResponse"), parseTaskQueryParameters()),
		},
		"/api/scan/parse-tasks/stats": map[string]any{
			"get": withQueryParameters(operation("getParseTaskStats", "", "ParseTaskStatsResponse"), parseTaskStatsQueryParameters()),
		},
		"/api/scan/parse-tasks/{task_id}": map[string]any{
			"get": pathOperation("getParseTask", "", "ParseTaskDetailResponse", "task_id"),
		},
		"/api/scan/parse-tasks/{task_id}/retry": map[string]any{
			"post": pathOperation("retryParseTask", "RetryParseTaskRequest", "ParseTaskDetailResponse", "task_id"),
		},
		"/api/scan/admin/deleting": map[string]any{
			"get": withQueryParameters(operation("listDeletingResources", "", "DeletingResourceListResponse"), adminQueryParameters()),
		},
		"/api/scan/admin/compensations": map[string]any{
			"get": withQueryParameters(operation("listCompensations", "", "CompensationListResponse"), adminQueryParameters()),
		},
		"/api/scan/admin/compensations/{operation_id}/retry": map[string]any{
			"post": pathOperation("retryCompensation", "", "CompensationResponse", "operation_id"),
		},
		"/api/scan/admin/dead-letters": map[string]any{
			"get": withQueryParameters(operation("listDeadLetters", "", "DeadLetterListResponse"), adminQueryParameters()),
		},
		"/api/scan/admin/dead-letters/{dead_letter_id}/retry": map[string]any{
			"post": pathOperation("retryDeadLetter", "RetryParseTaskRequest", "ParseTaskDetailResponse", "dead_letter_id"),
		},
		"/api/scan/admin/sources/{source_id}/bindings/{binding_id}/reconcile": map[string]any{
			"post": pathOperation("reconcileBinding", "ReconcileBindingRequest", "ReconcileBindingResponse", "source_id", "binding_id"),
		},
	}
}

func operation(operationID, requestSchema, responseSchema string) map[string]any {
	return statusOperation(operationID, requestSchema, responseSchema, "200")
}

func createdOperation(operationID, requestSchema, responseSchema string) map[string]any {
	return statusOperation(operationID, requestSchema, responseSchema, "201")
}

func statusOperation(operationID, requestSchema, responseSchema, successStatus string) map[string]any {
	op := map[string]any{
		"operationId": operationID,
		"tags":        []string{"scan"},
		"responses": map[string]any{
			successStatus: response(responseSchema),
			"default": map[string]any{
				"description": "standard error",
				"content":     jsonContent("ErrorResponse"),
			},
		},
	}
	if requestSchema != "" {
		op["requestBody"] = map[string]any{
			"required": true,
			"content":  jsonContent(requestSchema),
		}
	}
	return op
}

func pathOperation(operationID, requestSchema, responseSchema string, pathParams ...string) map[string]any {
	op := operation(operationID, requestSchema, responseSchema)
	op["parameters"] = pathParameters(pathParams...)
	return op
}

func withQueryParameters(op map[string]any, queryParams []map[string]any) map[string]any {
	if len(queryParams) == 0 {
		return op
	}
	raw, _ := op["parameters"].([]map[string]any)
	params := append([]map[string]any{}, raw...)
	params = append(params, queryParams...)
	op["parameters"] = params
	return op
}

func createdPathOperation(operationID, requestSchema, responseSchema string, pathParams ...string) map[string]any {
	op := createdOperation(operationID, requestSchema, responseSchema)
	op["parameters"] = pathParameters(pathParams...)
	return op
}

func pathParameters(pathParams ...string) []map[string]any {
	params := make([]map[string]any, 0, len(pathParams))
	for _, name := range pathParams {
		params = append(params, map[string]any{
			"name":     name,
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		})
	}
	return params
}

func response(schema string) map[string]any {
	return map[string]any{
		"description": "ok",
		"content":     jsonContent(schema),
	}
}

func jsonContent(schema string) map[string]any {
	return map[string]any{
		"application/json": map[string]any{
			"schema": schemaRef(schema),
			"examples": map[string]any{
				"default": map[string]any{"value": map[string]any{}},
			},
		},
	}
}

func schemaRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func openAPISchemas() map[string]any {
	return map[string]any{
		"ErrorResponse":                 object([]string{"code", "message", "details"}, props("code", stringSchema(), "message", stringSchema(), "details", objectSchema())),
		"ConnectorSpec":                 connectorSpecSchema(),
		"ConnectorListResponse":         object([]string{"items"}, props("items", arrayOf("ConnectorSpec"))),
		"TreeNode":                      treeNodeSchema(),
		"TreeNodePage":                  treeNodePageSchema(),
		"BindingTargetChildrenRequest":  object([]string{"connector_type"}, treeRequestProps()),
		"BindingTargetSearchRequest":    object([]string{"connector_type", "keyword"}, mergeProps(treeRequestProps(), props("keyword", stringSchema()))),
		"ValidateBindingTargetRequest":  object([]string{"connector_type", "target_type", "target_ref"}, targetProps()),
		"ValidateBindingTargetResponse": object([]string{"target_type", "target_ref", "target_fingerprint", "display_name", "root_object_key"}, targetResponseProps()),
		"CreateSourceRequest":           createSourceRequestSchema(),
		"CreateSourceResponse":          object([]string{"source", "bindings"}, props("source", schemaRef("SourceResponse"), "bindings", arrayOf("SourceBindingResponse"))),
		"SourceResponse":                sourceResponseSchema(),
		"SourceListResponse":            object([]string{"items", "total"}, props("items", arrayOf("SourceListItem"), "total", integerSchema())),
		"SourceListItem":                sourceListItemSchema(),
		"GetSourceResponse":             object([]string{"source"}, props("source", schemaRef("SourceResponse"), "bindings", arrayOf("SourceBindingResponse"), "summary", objectSchema())),
		"UpdateSourceRequest":           updateSourceRequestSchema(),
		"UpdateSourceResponse":          object([]string{"source", "bindings"}, props("source", schemaRef("SourceResponse"), "bindings", arrayOf("SourceBindingResponse"), "created_binding_ids", stringArray(), "updated_binding_ids", stringArray(), "removed_binding_ids", stringArray(), "job_ids", stringArray())),
		"DeleteSourceResponse":          object([]string{"deleted", "source_id"}, props("deleted", boolSchema(), "source_id", stringSchema(), "removed_binding_ids", stringArray(), "removed_dataset_id", stringSchema())),
		"SourceBindingRequest":          sourceBindingRequestSchema(),
		"SourceBindingResponse":         sourceBindingResponseSchema(),
		"SchedulePolicy":                schedulePolicySchema(),
		"ScheduleRule":                  scheduleRuleSchema(),
		"BindingMutationResponse":       object([]string{"binding"}, props("binding", schemaRef("SourceBindingResponse"), "old_generation", integerSchema(), "new_generation", integerSchema(), "job_ids", stringArray())),
		"DeleteBindingResponse":         object([]string{"deleted", "binding_id"}, props("deleted", boolSchema(), "binding_id", stringSchema(), "removed_core_parent_document_id", stringSchema(), "cancelled_task_count", integerSchema())),
		"TriggerSourceSyncRequest":      object(nil, props("request_id", stringSchema(), "binding_id", stringSchema(), "scope_type", stringSchema(), "scope_ref", objectSchema())),
		"TriggerSourceSyncResponse":     object([]string{"run_ids", "job_ids"}, props("run_ids", stringArray(), "job_ids", stringArray(), "intents", arrayOf("SyncRunIntent"))),
		"SyncRunIntent":                 syncRunIntentSchema(),
		"SourceTreeChildrenRequest":     object(nil, mergeProps(sourceTreeRequestProps(), props("use_cache", boolSchema()))),
		"SourceTreeSearchRequest":       object([]string{"keyword"}, mergeProps(sourceTreeRequestProps(), props("keyword", stringSchema()))),
		"SourceDocumentListResponse":    object([]string{"items", "total", "page", "page_size"}, props("items", arrayOf("SourceDocumentItem"), "total", integerSchema(), "page", integerSchema(), "page_size", integerSchema(), "summary", objectSchema())),
		"SourceDocumentItem":            sourceDocumentItemSchema(),
		"SourceSummaryResponse":         sourceSummarySchema(),
		"GenerateTasksRequest":          generateTasksRequestSchema(),
		"GenerateTasksResponse":         generateTasksResponseSchema(),
		"GenerateTaskScope":             generateTaskScopeSchema(),
		"ExpediteTasksRequest":          expediteTasksRequestSchema(),
		"ExpediteTasksResponse":         expediteTasksResponseSchema(),
		"ParseTaskListResponse":         object([]string{"items", "total"}, props("items", arrayOf("ParseTaskResponse"), "total", integerSchema())),
		"ParseTaskResponse":             parseTaskResponseSchema(),
		"ParseTaskDetailResponse":       object([]string{"task"}, props("task", schemaRef("ParseTaskResponse"), "document", schemaRef("TaskDocument"), "state", schemaRef("TaskDocumentState"), "object", schemaRef("TaskObject"))),
		"TaskDocument":                  taskDocumentSchema(),
		"TaskDocumentState":             taskDocumentStateSchema(),
		"TaskObject":                    taskObjectSchema(),
		"ParseTaskStatsResponse":        parseTaskStatsResponseSchema(),
		"RetryParseTaskRequest":         object(nil, props("force", boolSchema())),
		"DeletingResourceListResponse":  object([]string{"items", "total"}, props("items", arrayOf("DeletingResource"), "total", integerSchema())),
		"DeletingResource":              deletingResourceSchema(),
		"CompensationListResponse":      object([]string{"items", "total"}, props("items", arrayOf("Compensation"), "total", integerSchema())),
		"Compensation":                  compensationSchema(),
		"DeadLetterListResponse":        object([]string{"items", "total"}, props("items", arrayOf("DeadLetter"), "total", integerSchema())),
		"DeadLetter":                    deadLetterSchema(),
		"ReconcileBindingRequest":       object(nil, props("request_id", stringSchema())),
		"ReconcileBindingResponse":      reconcileBindingResponseSchema(),
		"ListMode":                      enumSchema("page", "all_current_level"),
		"SourceStatus":                  enumSchema("ACTIVE", "PAUSED", "DELETING", "ERROR"),
		"BindingStatus":                 enumSchema("ACTIVE", "PAUSED", "DELETING", "ERROR"),
		"SyncMode":                      enumSchema("manual", "scheduled", "watch"),
		"SourceState":                   enumSchema("NEW", "MODIFIED", "DELETED", "UNCHANGED"),
		"SyncState":                     enumSchema("IDLE", "SCHEDULED", "PENDING", "RUNNING", "FAILED"),
		"TaskAction":                    enumSchema("CREATE", "REPARSE", "DELETE"),
		"ParseTaskStatus":               enumSchema("PENDING", "RUNNING", "SUBMITTED", "SUCCEEDED", "FAILED", "SUPERSEDED"),
	}
}

func connectorSpecSchema() map[string]any {
	return object([]string{"connector_type", "target_types", "supports_search", "supports_delta", "supports_dual_role_object", "supports_export_formats", "max_page_size"}, props(
		"connector_type", stringSchema(),
		"target_types", stringArray(),
		"supports_search", boolSchema(),
		"supports_delta", boolSchema(),
		"supports_dual_role_object", boolSchema(),
		"supports_export_formats", stringArray(),
		"max_page_size", integerSchema(),
	))
}

func treeNodeSchema() map[string]any {
	return object([]string{"key", "display_name", "is_document", "is_container", "has_children", "selectable"}, props(
		"key", stringSchema(),
		"node_ref", stringSchema(),
		"display_name", stringSchema(),
		"connector_type", stringSchema(),
		"target_type", stringSchema(),
		"target_ref", stringSchema(),
		"source_id", stringSchema(),
		"binding_id", stringSchema(),
		"tree_key", stringSchema(),
		"object_key", stringSchema(),
		"parent_key", stringSchema(),
		"is_document", boolSchema(),
		"is_container", boolSchema(),
		"has_children", boolSchema(),
		"selectable", boolSchema(),
		"source_state", stringSchema(),
		"sync_state", stringSchema(),
		"parse_queue_state", stringSchema(),
		"provider_meta", objectSchema(),
	))
}

func treeNodePageSchema() map[string]any {
	return object([]string{"items", "next_cursor", "has_more", "list_complete", "truncated"}, props(
		"items", arrayOf("TreeNode"),
		"next_cursor", stringSchema(),
		"has_more", boolSchema(),
		"list_complete", boolSchema(),
		"truncated", boolSchema(),
		"search_mode", stringSchema(),
	))
}

func createSourceRequestSchema() map[string]any {
	return object([]string{"request_id", "name", "bindings"}, props(
		"request_id", stringSchema(),
		"name", stringSchema(),
		"bindings", arrayOf("SourceBindingRequest"),
		"include_extensions", stringArray(),
		"exclude_extensions", stringArray(),
		"source_options", objectSchema(),
	))
}

func updateSourceRequestSchema() map[string]any {
	return object([]string{"config_version"}, props(
		"config_version", integerSchema(),
		"name", stringSchema(),
		"bindings", arrayOf("SourceBindingRequest"),
		"include_extensions", stringArray(),
		"exclude_extensions", stringArray(),
		"source_options", objectSchema(),
	))
}

func sourceResponseSchema() map[string]any {
	return object([]string{"source_id", "name", "dataset_id", "status", "config_version"}, props(
		"source_id", stringSchema(),
		"name", stringSchema(),
		"dataset_id", stringSchema(),
		"status", schemaRef("SourceStatus"),
		"config_version", integerSchema(),
	))
}

func sourceListItemSchema() map[string]any {
	return object([]string{"source_id", "name", "dataset_id", "status", "config_version", "binding_count", "updated_at"}, props(
		"source_id", stringSchema(),
		"tenant_id", stringSchema(),
		"created_by", stringSchema(),
		"name", stringSchema(),
		"dataset_id", stringSchema(),
		"status", schemaRef("SourceStatus"),
		"source_options", objectSchema(),
		"include_extensions", stringArray(),
		"exclude_extensions", stringArray(),
		"config_version", integerSchema(),
		"binding_count", integerSchema(),
		"summary", objectSchema(),
		"deleted_at", stringSchema(),
		"created_at", stringSchema(),
		"updated_at", stringSchema(),
	))
}

func sourceBindingRequestSchema() map[string]any {
	return object([]string{"connector_type", "target_type", "target_ref", "sync_mode"}, props(
		"binding_id", stringSchema(),
		"connector_type", stringSchema(),
		"target_type", stringSchema(),
		"target_ref", stringSchema(),
		"display_name", stringSchema(),
		"agent_id", stringSchema(),
		"auth_connection_id", stringSchema(),
		"provider_options", objectSchema(),
		"sync_mode", schemaRef("SyncMode"),
		"schedule_policy", schemaRef("SchedulePolicy"),
		"include_extensions", stringArray(),
		"exclude_extensions", stringArray(),
	))
}

func sourceBindingResponseSchema() map[string]any {
	return object([]string{"binding_id", "connector_type", "target_type", "tree_key", "binding_generation", "core_parent_document_id"}, props(
		"binding_id", stringSchema(),
		"source_id", stringSchema(),
		"connector_type", stringSchema(),
		"target_type", stringSchema(),
		"target_ref", stringSchema(),
		"target_fingerprint", stringSchema(),
		"tree_key", stringSchema(),
		"binding_generation", integerSchema(),
		"core_parent_document_id", stringSchema(),
		"sync_mode", schemaRef("SyncMode"),
		"schedule_policy", schemaRef("SchedulePolicy"),
		"next_sync_at", stringSchema(),
		"status", schemaRef("BindingStatus"),
	))
}

func schedulePolicySchema() map[string]any {
	return object([]string{"timezone", "calendar", "rules"}, props(
		"timezone", stringSchema(),
		"calendar", enumSchema("weekly"),
		"rules", arrayOf("ScheduleRule"),
	))
}

func scheduleRuleSchema() map[string]any {
	return object([]string{"days", "time"}, props(
		"days", map[string]any{"type": "array", "items": enumSchema("everyday", "workday", "non_workday", "mon", "tue", "wed", "thu", "fri", "sat", "sun")},
		"time", stringSchema(),
	))
}

func sourceDocumentItemSchema() map[string]any {
	return object(nil, props(
		"document_id", stringSchema(),
		"source_id", stringSchema(),
		"binding_id", stringSchema(),
		"object_key", stringSchema(),
		"display_name", stringSchema(),
		"name", stringSchema(),
		"path", stringSchema(),
		"directory", stringSchema(),
		"file_type", stringSchema(),
		"size_bytes", integerSchema(),
		"source_version", stringSchema(),
		"baseline_version", stringSchema(),
		"source_state", schemaRef("SourceState"),
		"sync_state", schemaRef("SyncState"),
		"pending_action", stringSchema(),
		"parse_queue_state", stringSchema(),
		"parse_status", stringSchema(),
		"parse_state", stringSchema(),
		"has_update", boolSchema(),
		"update_type", stringSchema(),
		"update_desc", stringSchema(),
		"core_document_id", stringSchema(),
		"modified_at", stringSchema(),
		"last_synced_at", stringSchema(),
		"last_error", objectSchema(),
	))
}

func sourceSummarySchema() map[string]any {
	return object([]string{"source_id", "total_objects", "document_objects"}, props(
		"source_id", stringSchema(),
		"total_objects", integerSchema(),
		"document_objects", integerSchema(),
		"new_count", integerSchema(),
		"modified_count", integerSchema(),
		"deleted_count", integerSchema(),
		"unchanged_count", integerSchema(),
		"storage_bytes", integerSchema(),
		"total_document_count", integerSchema(),
		"parsed_document_count", integerSchema(),
		"pending_pull_count", integerSchema(),
		"pending_task_count", integerSchema(),
		"running_task_count", integerSchema(),
		"failed_task_count", integerSchema(),
		"bindings", arrayOf("SourceSummaryResponse"),
	))
}

func syncRunIntentSchema() map[string]any {
	return object([]string{"run_id", "source_id", "binding_id", "binding_generation", "status", "trigger_type", "scope_type", "created"}, props(
		"run_id", stringSchema(),
		"job_id", stringSchema(),
		"source_id", stringSchema(),
		"binding_id", stringSchema(),
		"binding_generation", integerSchema(),
		"status", stringSchema(),
		"trigger_type", stringSchema(),
		"scope_type", stringSchema(),
		"scope_ref", objectSchema(),
		"created", boolSchema(),
	))
}

func generateTasksRequestSchema() map[string]any {
	return object(nil, props(
		"request_id", stringSchema(),
		"binding_id", stringSchema(),
		"object_keys", stringArray(),
		"document_ids", stringArray(),
		"paths", stringArray(),
		"scopes", arrayOf("GenerateTaskScope"),
		"mode", stringSchema(),
		"priority", integerSchema(),
		"trigger_policy", stringSchema(),
		"updated_only", boolSchema(),
		"selection_token", stringSchema(),
	))
}

func generateTasksResponseSchema() map[string]any {
	return object([]string{"requested_count", "accepted_count", "duplicate_count", "already_active_count", "skipped_count", "task_ids"}, props(
		"requested_count", integerSchema(),
		"accepted_count", integerSchema(),
		"duplicate_count", integerSchema(),
		"already_active_count", integerSchema(),
		"skipped_count", integerSchema(),
		"task_ids", stringArray(),
		"job_id", stringSchema(),
		"job_ids", stringArray(),
		"run_ids", stringArray(),
		"queued_sync_count", integerSchema(),
	))
}

func generateTaskScopeSchema() map[string]any {
	return object(nil, props(
		"key", stringSchema(),
		"object_key", stringSchema(),
		"node_ref", stringSchema(),
		"path", stringSchema(),
		"is_document", boolSchema(),
		"is_container", boolSchema(),
	))
}

func expediteTasksRequestSchema() map[string]any {
	return object(nil, props(
		"binding_id", stringSchema(),
		"task_ids", stringArray(),
		"document_ids", stringArray(),
		"object_keys", stringArray(),
		"priority", integerSchema(),
	))
}

func expediteTasksResponseSchema() map[string]any {
	return object([]string{"updated_count", "skipped_count", "task_ids"}, props(
		"updated_count", integerSchema(),
		"skipped_count", integerSchema(),
		"task_ids", stringArray(),
		"skipped_items", stringArray(),
	))
}

func parseTaskResponseSchema() map[string]any {
	return object([]string{"task_id", "source_id", "binding_id", "object_key", "document_id", "task_action", "target_version_id", "binding_generation", "status"}, props(
		"task_id", stringSchema(),
		"source_id", stringSchema(),
		"binding_id", stringSchema(),
		"object_key", stringSchema(),
		"document_id", stringSchema(),
		"task_action", schemaRef("TaskAction"),
		"target_version_id", stringSchema(),
		"binding_generation", integerSchema(),
		"status", schemaRef("ParseTaskStatus"),
		"retry_count", integerSchema(),
		"next_run_at", stringSchema(),
		"core_task_id", stringSchema(),
		"core_document_id", stringSchema(),
	))
}

func taskDocumentSchema() map[string]any {
	return object([]string{"document_id", "source_id", "binding_id", "object_key"}, props(
		"document_id", stringSchema(),
		"source_id", stringSchema(),
		"binding_id", stringSchema(),
		"object_key", stringSchema(),
		"core_document_id", stringSchema(),
		"current_version_id", stringSchema(),
		"desired_version_id", stringSchema(),
		"source_version", stringSchema(),
		"display_name", stringSchema(),
		"parse_status", stringSchema(),
		"created_at", stringSchema(),
		"updated_at", stringSchema(),
	))
}

func taskDocumentStateSchema() map[string]any {
	return object([]string{"source_id", "binding_id", "binding_generation", "object_key", "source_state", "sync_state", "document_list_visible", "selectable"}, props(
		"source_id", stringSchema(),
		"binding_id", stringSchema(),
		"binding_generation", integerSchema(),
		"object_key", stringSchema(),
		"source_version", stringSchema(),
		"baseline_version", stringSchema(),
		"source_state", schemaRef("SourceState"),
		"sync_state", schemaRef("SyncState"),
		"pending_action", stringSchema(),
		"document_list_visible", boolSchema(),
		"selectable", boolSchema(),
		"parse_queue_state", stringSchema(),
		"document_id", stringSchema(),
		"active_task_id", stringSchema(),
		"last_detected_at", stringSchema(),
		"last_synced_at", stringSchema(),
		"last_error", objectSchema(),
		"created_at", stringSchema(),
		"updated_at", stringSchema(),
	))
}

func taskObjectSchema() map[string]any {
	return object([]string{"source_id", "binding_id", "object_key", "display_name", "is_document", "is_container"}, props(
		"source_id", stringSchema(),
		"binding_id", stringSchema(),
		"object_key", stringSchema(),
		"display_name", stringSchema(),
		"source_version", stringSchema(),
		"is_document", boolSchema(),
		"is_container", boolSchema(),
		"created_at", stringSchema(),
		"updated_at", stringSchema(),
	))
}

func parseTaskStatsResponseSchema() map[string]any {
	return object([]string{"total", "by_status", "by_action", "retryable_failed_count"}, props(
		"total", integerSchema(),
		"by_status", objectSchema(),
		"by_action", objectSchema(),
		"retryable_failed_count", integerSchema(),
	))
}

func deletingResourceSchema() map[string]any {
	return object([]string{"resource_type", "source_id", "status", "updated_at"}, props(
		"resource_type", stringSchema(),
		"source_id", stringSchema(),
		"binding_id", stringSchema(),
		"status", schemaRef("BindingStatus"),
		"deleted_at", stringSchema(),
		"last_error", objectSchema(),
		"updated_at", stringSchema(),
	))
}

func compensationSchema() map[string]any {
	return object([]string{"operation_id", "status", "compensation_status", "updated_at"}, props(
		"operation_id", stringSchema(),
		"source_id", stringSchema(),
		"dataset_id", stringSchema(),
		"status", stringSchema(),
		"compensation_status", stringSchema(),
		"compensation_error", objectSchema(),
		"updated_at", stringSchema(),
	))
}

func deadLetterSchema() map[string]any {
	return object([]string{"dead_letter_id", "task_id", "source_id", "binding_id", "binding_generation", "object_key", "document_id", "task_action", "target_version_id", "retry_count", "failed_at"}, props(
		"dead_letter_id", stringSchema(),
		"task_id", stringSchema(),
		"source_id", stringSchema(),
		"binding_id", stringSchema(),
		"binding_generation", integerSchema(),
		"object_key", stringSchema(),
		"document_id", stringSchema(),
		"task_action", schemaRef("TaskAction"),
		"target_version_id", stringSchema(),
		"retry_count", integerSchema(),
		"error_code", stringSchema(),
		"last_error", objectSchema(),
		"failed_at", stringSchema(),
		"created_at", stringSchema(),
	))
}

func reconcileBindingResponseSchema() map[string]any {
	return object([]string{"run_id", "source_id", "binding_id", "binding_generation", "status", "trigger_type", "scope_type"}, props(
		"run_id", stringSchema(),
		"source_id", stringSchema(),
		"binding_id", stringSchema(),
		"binding_generation", integerSchema(),
		"status", stringSchema(),
		"trigger_type", stringSchema(),
		"scope_type", stringSchema(),
	))
}

func treeRequestProps() map[string]any {
	return props(
		"connector_type", stringSchema(),
		"target_type", stringSchema(),
		"target_ref", stringSchema(),
		"node_ref", stringSchema(),
		"agent_id", stringSchema(),
		"auth_connection_id", stringSchema(),
		"provider_options", objectSchema(),
		"include_files", boolSchema(),
		"list_mode", schemaRef("ListMode"),
		"page_size", integerSchema(),
		"cursor", stringSchema(),
		"max_items", integerSchema(),
	)
}

func targetProps() map[string]any {
	return props(
		"connector_type", stringSchema(),
		"target_type", stringSchema(),
		"target_ref", stringSchema(),
		"agent_id", stringSchema(),
		"auth_connection_id", stringSchema(),
		"provider_options", objectSchema(),
	)
}

func targetResponseProps() map[string]any {
	return props(
		"target_type", stringSchema(),
		"target_ref", stringSchema(),
		"target_fingerprint", stringSchema(),
		"display_name", stringSchema(),
		"root_object_key", stringSchema(),
		"provider_meta", objectSchema(),
	)
}

func sourceTreeRequestProps() map[string]any {
	return props(
		"binding_id", stringSchema(),
		"tree_key", stringSchema(),
		"parent_key", stringSchema(),
		"include_documents", boolSchema(),
		"include_containers", boolSchema(),
		"state_filter", stringArray(),
		"list_mode", schemaRef("ListMode"),
		"page_size", integerSchema(),
		"cursor", stringSchema(),
		"max_items", integerSchema(),
	)
}

func documentQueryParameters() []map[string]any {
	return []map[string]any{
		queryParameter("binding_id", stringSchema()),
		queryParameter("keyword", stringSchema()),
		queryParameter("state_filter", stringArray()),
		queryParameter("parse_status", stringArray()),
		queryParameter("page", integerSchema()),
		queryParameter("page_size", integerSchema()),
	}
}

func parseTaskQueryParameters() []map[string]any {
	return []map[string]any{
		queryParameter("source_id", stringSchema()),
		queryParameter("binding_id", stringSchema()),
		queryParameter("document_id", stringSchema()),
		queryParameter("status", stringArray()),
		queryParameter("task_action", stringArray()),
		queryParameter("page", integerSchema()),
		queryParameter("page_size", integerSchema()),
	}
}

func parseTaskStatsQueryParameters() []map[string]any {
	return []map[string]any{
		queryParameter("source_id", stringSchema()),
		queryParameter("binding_id", stringSchema()),
		queryParameter("document_id", stringSchema()),
	}
}

func adminQueryParameters() []map[string]any {
	return []map[string]any{
		queryParameter("source_id", stringSchema()),
		queryParameter("binding_id", stringSchema()),
		queryParameter("page", integerSchema()),
		queryParameter("page_size", integerSchema()),
	}
}

func sourceListQueryParameters() []map[string]any {
	return []map[string]any{
		queryParameter("keyword", stringSchema()),
		queryParameter("status", schemaRef("SourceStatus")),
		queryParameter("page", integerSchema()),
		queryParameter("page_size", integerSchema()),
	}
}

func queryParameter(name string, schema map[string]any) map[string]any {
	return map[string]any{
		"name":   name,
		"in":     "query",
		"schema": schema,
	}
}

func object(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func props(values ...any) map[string]any {
	out := make(map[string]any, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		out[values[i].(string)] = values[i+1]
	}
	return out
}

func mergeProps(left, right map[string]any) map[string]any {
	out := make(map[string]any, len(left)+len(right))
	for key, value := range left {
		out[key] = value
	}
	for key, value := range right {
		out[key] = value
	}
	return out
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func integerSchema() map[string]any {
	return map[string]any{"type": "integer", "format": "int64"}
}

func boolSchema() map[string]any {
	return map[string]any{"type": "boolean"}
}

func objectSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}

func stringArray() map[string]any {
	return map[string]any{"type": "array", "items": stringSchema()}
}

func arrayOf(schema string) map[string]any {
	return map[string]any{"type": "array", "items": schemaRef(schema)}
}

func enumSchema(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}
