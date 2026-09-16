package main

import "strconv"

// These schemas supplement route discovery: internal local execution is an
// authenticated service protocol, not a browser-supplied file-access grant.
func localExecutionSchemas() map[string]any {
	request := objReq([]string{"workspace_id", "call_id", "operation", "path", "execution_mode", "arguments_digest", "host_intent_id", "tool_name"},
		prop("workspace_id", strSchema()), prop("call_id", strSchema()), prop("tool_name", strSchema()),
		prop("history_id", strSchema()), prop("run_id", strSchema()), prop("task_id", strSchema()),
		prop("generation", strSchema()), prop("attempt_id", strSchema()), prop("lease_token", strSchema()),
		prop("execution_mode", enumStringSchema("host_access")), prop("arguments_digest", strSchema()), prop("host_intent_id", strSchema()), prop("capability", enumStringSchema("shell", "tool")), prop("tool_identity", strSchema()), prop("tool_origin", strSchema()), prop("command", strSchema()),
		prop("operation", enumStringSchema("tool", "shell", "read", "write", "delete")),
		prop("path", strSchema()))
	request["description"] = "Body identity never overrides authenticated user/conversation. host_access requires a host_intent_id, tool_name, arguments_digest and canonical absolute path, uses read/write/delete, and always enters approval without recomputing product policy. Shell uses capability=shell, operation=shell, an empty path and a command summary. Generic tools use capability=tool, operation=tool, an empty path and an opaque tool_identity. allow_future is permitted only by the frozen ask_as_needed snapshot and persists a conversation-scoped shell or stable tool-identity grant. Core performs no filesystem checks. Approval expires after five minutes."
	completion := objReq([]string{"status"}, prop("status", enumStringSchema("completed", "failed", "uncertain")),
		prop("reason", strSchema()))
	result := obj(prop("operation_id", strSchema()), prop("path", strSchema()),
		prop("decision", enumStringSchema("allowed", "pending", "denied")), prop("status", strSchema()),
		prop("expires_at", int64Schema()), prop("reason", strSchema()), prop("receipt", boolSchema()),
		prop("shell_granted", boolSchema()), prop("tool_granted", strSchema()), prop("execute_allowed", boolSchema()))
	return map[string]any{
		"WorkspaceOperationRequest":       request,
		"WorkspaceOperationBatchRequest":  objReq([]string{"calls"}, prop("calls", map[string]any{"type": "array", "minItems": 1, "maxItems": 16, "items": refSchema("WorkspaceOperationRequest")})),
		"WorkspaceOperationBatchResponse": objReq([]string{"code", "message", "data"}, prop("code", intSchema()), prop("message", strSchema()), prop("data", objReq([]string{"operations"}, prop("operations", map[string]any{"type": "array", "items": result})))),
		"LocalOperationCompletion":        map[string]any{"allOf": []any{refSchema("WorkspaceOperationRequest"), completion}},
		"WorkspaceOperationResponse": objReq([]string{"code", "message", "data"}, prop("code", intSchema()),
			prop("message", strSchema()), prop("data", result)),
	}
}

func localExecutionPaths() map[string]any {
	base := "/internal/conversations/{conversation_id}/workspace-operations"
	paths := map[string]any{}
	for _, action := range []string{"prepare-batch", "claim", "complete"} {
		path := base + "/{operation_id}:" + action
		params := []map[string]any{param("path", "conversation_id", true, strSchema())}
		if action == "prepare-batch" {
			path = base + ":" + action
		} else {
			params = append(params, param("path", "operation_id", true, strSchema()))
		}
		schema := "WorkspaceOperationRequest"
		if action == "complete" {
			schema = "LocalOperationCompletion"
		}
		responseSchema := "WorkspaceOperationResponse"
		if action == "prepare-batch" {
			schema, responseSchema = "WorkspaceOperationBatchRequest", "WorkspaceOperationBatchResponse"
		}
		success := response(200, "Operation state; only the first successful claim grants execute_allowed", refSchema(responseSchema))
		responses := map[string]any{"200": success}
		for _, status := range []int{400, 401, 403, 404, 409} {
			responses[strconv.Itoa(status)] = response(status, "Owner-scoped authorization or lifecycle error", refSchema("LocalWorkspaceErrorResponse"))
		}
		operation := op("Workspace operation "+action, params, jsonBody(refSchema(schema), true), success)
		operation["responses"] = responses
		operation["description"] = "Local/Desktop only. Requires authenticated user and X-LazyMind-Internal-Token. claim consumes a single attempt before local IO; never replay after an ambiguous claim. complete is idempotent and cannot grant IO."
		paths[path] = map[string]any{"post": operation}
	}
	params := []map[string]any{param("path", "conversation_id", true, strSchema()), param("path", "operation_id", true, strSchema())}
	for _, name := range []string{"run_id", "history_id", "task_id", "generation", "attempt_id"} {
		params = append(params, param("query", name, false, strSchema()))
	}
	paths[base+"/{operation_id}"] = map[string]any{"get": op("Workspace operation status", params, nil,
		response(200, "Current decision, without execution credentials", refSchema("WorkspaceOperationResponse")))}
	return paths
}
