package main

import (
	"lazymind/core/common/taskdisplay"
	"strconv"
)

type ordinaryTaskDetailData struct {
	Task taskdisplay.OrdinaryTaskView `json:"task"`
}
type ordinaryTaskDetailResponse struct {
	Code    int                    `json:"code"`
	Message string                 `json:"message"`
	Data    ordinaryTaskDetailData `json:"data"`
}
type ordinaryTaskListData struct {
	Tasks []taskdisplay.OrdinaryTaskView `json:"tasks"`
	Runs  []taskdisplay.OrdinaryRunView  `json:"runs"`
}
type ordinaryTaskListResponse struct {
	Code    int                  `json:"code"`
	Message string               `json:"message"`
	Data    ordinaryTaskListData `json:"data"`
}
type ordinaryArtifactData struct {
	Artifacts  []taskdisplay.PublicArtifact `json:"artifacts"`
	Page       taskdisplay.CollectionPage   `json:"page"`
	DisplayKey string                       `json:"display_key"`
	Revision   int64                        `json:"revision"`
}
type ordinaryArtifactResponse struct {
	Code    int                  `json:"code"`
	Message string               `json:"message"`
	Data    ordinaryArtifactData `json:"data"`
}
type ordinaryWorkflowData struct {
	SchemaVersion int                            `json:"schema_version"`
	SessionID     string                         `json:"session_id"`
	Status        string                         `json:"status"`
	Revision      int64                          `json:"revision"`
	Cursor        int64                          `json:"cursor"`
	Tasks         []taskdisplay.OrdinaryTaskView `json:"tasks"`
	Runs          []taskdisplay.OrdinaryRunView  `json:"runs"`
}
type ordinaryWorkflowResponse struct {
	Code    int                  `json:"code"`
	Message string               `json:"message"`
	Data    ordinaryWorkflowData `json:"data"`
}

func ordinaryTaskOpenAPI() (map[string]any, map[string]any) {
	builder := newSchemaBuilder()
	detail := builder.schemaFromSource(schemaSource{Type: ordinaryTaskDetailResponse{}})
	list := builder.schemaFromSource(schemaSource{Type: ordinaryTaskListResponse{}})
	artifacts := builder.schemaFromSource(schemaSource{Type: ordinaryArtifactResponse{}})
	workflow := builder.schemaFromSource(schemaSource{Type: ordinaryWorkflowResponse{}})
	snapshot := builder.schemaFromSource(schemaSource{Type: taskdisplay.SnapshotEvent{}})
	// This optional public payload can be attached to existing executor progress.
	// It adds no generation behavior and contains no executor lease or prompt.
	builder.components["PublicDisplayProgress"] = objReq([]string{"schema_version", "event_key"},
		prop("schema_version", map[string]any{"type": "integer", "enum": []int{1}}),
		prop("event_key", map[string]any{"type": "string", "minLength": 1, "maxLength": 128}),
		prop("process_steps", map[string]any{"type": "array", "maxItems": 100, "items": refSchema("PublicProcessStep")}),
		prop("sources", map[string]any{"type": "array", "maxItems": 1000, "items": refSchema("PublicSource")}))
	process := builder.components["PublicProcessStep"].(map[string]any)["properties"].(map[string]any)
	process["status"] = enumStringSchema("pending", "running", "succeeded", "failed", "interrupted", "canceled")
	process["step_id"] = map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_.:-]{1,128}$"}
	process["title"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 100}
	process["summary"] = map[string]any{"type": "string", "maxLength": 300}
	process["revision"] = map[string]any{"type": "integer", "format": "int64", "minimum": 1}
	process["order"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 1000}
	builder.components["OrdinaryTaskView"].(map[string]any)["properties"].(map[string]any)["plan_steps"] = map[string]any{
		"type": "array", "minItems": 3, "maxItems": 5,
		"items":       map[string]any{"type": "string", "minLength": 1, "maxLength": 100},
		"description": "Public subtask plan from scope-v2 plan events. Planned actions only; no inferred execution status or timing.",
	}
	builder.components["OrdinaryTaskView"].(map[string]any)["properties"].(map[string]any)["progress_pct"] = map[string]any{
		"type": "integer", "minimum": 0, "maximum": 100,
		"description": "Reported overall subtask progress, used to estimate plan position. Does not establish individual process step states. Omitted when unavailable.",
	}
	builder.components["OrdinaryDisplayError"] = objReq([]string{"code", "message", "data"},
		prop("code", intSchema()), prop("message", strSchema()),
		prop("data", objReq([]string{"request_id"}, prop("request_id", strSchema()))))
	view := param("query", "view", false, enumStringSchema("ordinary", "developer"))
	pageParams := []map[string]any{
		view,
		param("query", "collection", false, enumStringSchema("process_steps", "sources", "stage_artifacts")),
		param("query", "cursor", false, strSchema()),
		param("query", "limit", false, map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 100}),
	}
	read := func(summary string, schema map[string]any, params []map[string]any) map[string]any {
		operation := op(summary, params, nil, response(200, "Public snapshot when view=ordinary; omitting view preserves the legacy response", schema))
		operation["description"] = "view=ordinary returns only public task presentation fields. Raw objectives, prompts, reasoning and tool logs are excluded. Unknown timings are null. Cursors are bound to display_key, collection and revision; a 409 requires a fresh first page."
		responses := operation["responses"].(map[string]any)
		for _, code := range []int{400, 401, 404, 409, 500, 503} {
			responses[strconv.Itoa(code)] = response(code, "Invalid request, missing resource or expired cursor", refSchema("OrdinaryDisplayError"))
		}
		return operation
	}
	paths := map[string]any{
		"/conversations/{conversation_id}/tasks":     map[string]any{"get": read("List public task snapshots and explicit final outputs", list, append([]map[string]any{view}, param("query", "summary_only", false, boolSchema())))},
		"/tasks/{task_id}":                           map[string]any{"get": read("Read a public task snapshot", detail, pageParams)},
		"/tasks/{task_id}/artifacts":                 map[string]any{"get": read("Read public stage artifacts", artifacts, pageParams)},
		"/workflow-sessions/{session_id}/projection": map[string]any{"get": read("Read public workflow attempts, including hosted attempts", workflow, append(append([]map[string]any{}, pageParams...), param("query", "display_key", false, strSchema())))},
	}
	for path, schema := range map[string]map[string]any{
		"/tasks/{task_id}:stream":                 snapshot,
		"/workflow-sessions/{session_id}/events":  workflow,
		"/conversations/{conversation_id}/events": obj(prop("type", strSchema()), prop("replayed", boolSchema()), prop("payload", obj())),
	} {
		paths[path] = map[string]any{"get": map[string]any{
			"summary":     "Stream public task snapshots or lifecycle notifications",
			"description": "With view=ordinary, every task connection starts with a current durable task_snapshot; workflow connections emit snapshot events. Reconnects hydrate snapshots without replaying raw execution logs. Workflow cursor expiration emits resync_required followed by snapshot. Conversation events carry only lifecycle identity and status fields. Omitting view preserves legacy SSE.",
			"parameters":  []map[string]any{view},
			"responses":   map[string]any{"200": map[string]any{"description": "Server-sent events", "content": map[string]any{"text/event-stream": map[string]any{"schema": schema}}}},
		}}
	}
	return builder.components, paths
}
