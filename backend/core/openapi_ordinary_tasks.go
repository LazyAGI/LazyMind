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
	process["model_switch"] = refSchema("ChatModelSwitchRecord")
	taskProperties := builder.components["OrdinaryTaskView"].(map[string]any)["properties"].(map[string]any)
	taskProperties["model_switch"] = refSchema("ChatModelSwitchRecord")
	taskProperties["failure_code"] = enumStringSchema("model_fallback_exhausted", "model_context_not_fit")
	// These shared Go aliases use the existing authoritative Chat schema names.
	for _, name := range []string{"ModelSwitchIdentity", "ModelSwitchedData", "ModelSwitchRecord"} {
		delete(builder.components, name)
	}
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

func subagentModelSchemas() map[string]any {
	window := func() map[string]any {
		return map[string]any{"type": "integer", "format": "int64", "minimum": 1, "nullable": true}
	}
	schemas := map[string]any{
		"TaskModelRuntimeEvent": objReq([]string{"schema_version", "event_id", "run_id", "type", "data"},
			prop("schema_version", map[string]any{"type": "integer", "enum": []int{1}}), prop("event_id", strSchema()), prop("run_id", strSchema()), prop("type", enumStringSchema("model_switched")), prop("data", refSchema("ChatModelSwitchedData"))),
		"TaskEvent": objReq([]string{"type"},
			prop("type", strSchema()), prop("task_id", strSchema()), prop("execution_id", strSchema()), prop("event_id", strSchema()), prop("model_output", refSchema("ChatModelOutput")), prop("runtime_event", refSchema("TaskModelRuntimeEvent")), prop("process_step", refSchema("PublicProcessStep")),
			prop("steps", array(strSchema())), prop("scope_version", intSchema()), prop("progress", intSchema()), prop("current_phase", strSchema()), prop("estimated_sec", intSchema()), prop("slot", strSchema()), prop("content_type", strSchema()), prop("seq", intSchema()), prop("value", obj()), prop("sources", array(obj())), prop("status", strSchema()), prop("summary", strSchema()), prop("message", strSchema()),
			prop("tool_calls", array(obj())), prop("tool_results", array(obj())), prop("durable_tool_results", array(obj())), prop("writing_subtasks", array(obj())), prop("text", strSchema()), prop("think", strSchema()), prop("stream_id", strSchema()), prop("chunk_index", int64Schema()), prop("delta", strSchema()), prop("v2_artifact_id", strSchema()), prop("v2_revision_id", strSchema())),
		"TaskModelCandidate":             objReq([]string{"candidate_id", "source", "model_id", "provider_group_id", "credential_revision", "credential_count", "max_input_tokens", "llm_config"}, prop("candidate_id", strSchema()), prop("source", enumStringSchema("own", "shared", "cloud")), prop("model_id", strSchema()), prop("provider_group_id", strSchema()), prop("credential_revision", int64Schema()), prop("credential_count", intSchema()), prop("max_input_tokens", window()), prop("llm_config", obj())),
		"TaskModelFallbackInitialSwitch": objReq([]string{"from", "reason_code", "from_max_input_tokens"}, prop("from", refSchema("ChatModelSwitchIdentity")), prop("reason_code", enumStringSchema("model_config_unavailable")), prop("from_max_input_tokens", window())),
		"SubagentRunRequest": objReq([]string{"task_id", "agent_type", "workspace_path", "resume"},
			prop("task_id", strSchema()), prop("agent_type", strSchema()), prop("workspace_path", strSchema()), prop("resume", boolSchema()), prop("params", obj()), prop("tools", array(strSchema())), prop("llm_config", obj()), prop("tool_config", obj()), prop("user_env_vars", obj()), prop("task_spec", obj()), prop("initial_steps", array(objReq([]string{"seq", "role", "content"}, prop("seq", intSchema()), prop("execution_id", strSchema()), prop("role", strSchema()), prop("content", obj())))), prop("workspace_execution", obj()),
			prop("model_fallback_current_id", strSchema()), prop("model_fallback_candidates", array(refSchema("TaskModelCandidate"))), prop("model_fallback_initial_switch", refSchema("TaskModelFallbackInitialSwitch"))),
	}
	schemas["TaskEvent"].(map[string]any)["description"] = "Ordinary task model_output uses execution_id as run_id. type=model_output carries pure begin/commit/rollback controls; only commit may include the Host model_switched fact. Model text/think carry run/call identity without phase. SDK observations and Chat run_finished are not Task events. Developer reconnect begins with task_start and committed DB entries retaining call identity, followed by one closed cursor (0 when none), current open cache preview and ordered live events. The DB section is authoritative hydration. Terminal restores any open preview before done/error."
	schemas["SubagentRunRequest"].(map[string]any)["description"] = "Core-to-Chat private execution input, rebuilt from the database task owner's current authorized models before every ordinary execution. Credentials and candidates never enter task params, public events or durable model_runtime records. InheritedModelOrigin is Core memory only and cannot be supplied by clients."
	return schemas

}
