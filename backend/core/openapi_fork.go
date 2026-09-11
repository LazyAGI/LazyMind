package main

func forkOpenAPISchemas(s map[string]any) map[string]any {
	s["ConversationForkCapability"] = objReq([]string{"supported"}, prop("supported", boolSchema()), prop("reason_code", strSchema()))
	s["ConversationForkOrigin"] = objReq([]string{"source_conversation_id", "source_history_id", "source_status", "can_locate"},
		prop("conversation_id", strSchema()), prop("source_conversation_id", strSchema()), prop("source_history_id", strSchema()),
		prop("source_seq", intSchema()), prop("source_history_revision", strSchema()), prop("source_prefix_revision", strSchema()),
		prop("source_title_snapshot", strSchema()), prop("forked_at", dateTimeSchema()),
		prop("source_status", enumStringSchema("available", "changed", "deleted", "node_deleted", "unavailable")), prop("can_locate", boolSchema()))
	s["ForkModelSelection"] = objReq([]string{"mode"}, prop("mode", enumStringSchema("fixed", "auto")), prop("model_id", strSchema()))
	s["ForkConfigSnapshot"] = obj(prop("version", intSchema()), prop("completeness", strSchema()), prop("model", refSchema("ChatModelRoute")),
		prop("thinking_depth", strSchema()), prop("chat_executor", strSchema()), prop("enable_workflow", boolSchema()),
		prop("enable_subagent", boolSchema()), prop("workflow_mode", strSchema()), prop("filters", obj()), prop("reasoning", boolSchema()), prop("use_memory", boolSchema()), prop("mode", strSchema()))
	s["ForkConfigSnapshot"].(map[string]any)["properties"].(map[string]any)["local_fs_source_ids"] = array(strSchema())
	s["ForkConfigSnapshot"].(map[string]any)["properties"].(map[string]any)["max_input_tokens"] = strSchema()
	s["ForkConfigIssue"] = objReq([]string{"field", "reason", "requires_confirmation"}, prop("field", strSchema()), prop("reason", strSchema()), prop("suggested_value", map[string]any{}), prop("requires_confirmation", boolSchema()))
	s["ForkAttachment"] = objReq([]string{"name", "status"}, prop("name", strSchema()), prop("status", enumStringSchema("available", "unavailable")), prop("reason", strSchema()), prop("reference", obj()))
	s["ForkPreviewRequest"] = objReq([]string{"source_history_id"}, prop("source_history_id", strSchema()))
	s["ForkCreateRequest"] = objReq([]string{"source_history_id", "expected_prefix_revision"}, prop("source_history_id", strSchema()), prop("expected_prefix_revision", strSchema()), prop("replacement_model", refSchema("ForkModelSelection")), prop("confirmed_fields", array(strSchema())), prop("confirmed_values", map[string]any{"type": "object", "additionalProperties": true}))
	s["ForkPreview"] = objReq([]string{"source_history_id", "source_seq", "source_title", "source_history_revision", "prefix_revision", "excerpt", "inherited_turn_count", "inherited_message_count", "config_snapshot_summary", "config_status", "config_issues", "attachments_summary", "warnings", "can_fork"},
		prop("source_history_id", strSchema()), prop("source_seq", intSchema()), prop("source_title", strSchema()), prop("source_history_revision", strSchema()), prop("prefix_revision", strSchema()), prop("excerpt", strSchema()),
		prop("inherited_turn_count", intSchema()), prop("inherited_message_count", intSchema()), prop("config_snapshot_summary", refSchema("ForkConfigSnapshot")), prop("config_status", strSchema()),
		prop("config_issues", array(refSchema("ForkConfigIssue"))), prop("attachments_summary", array(refSchema("ForkAttachment"))), prop("warnings", array(strSchema())), prop("can_fork", boolSchema()), prop("reason_code", strSchema()))
	s["ForkResult"] = objReq([]string{"conversation", "fork_origin", "inherited_turn_count", "warnings", "replayed"}, prop("conversation", refSchema("ConversationItem")), prop("fork_origin", refSchema("ConversationForkOrigin")), prop("inherited_turn_count", intSchema()), prop("warnings", array(strSchema())), prop("replayed", boolSchema()))
	s["ForkError"] = objReq([]string{"code", "message", "request_id"}, prop("code", enumStringSchema("SOURCE_UNAVAILABLE", "SOURCE_NOT_SETTLED", "SOURCE_NOT_COMPLETED", "SOURCE_CHANGED", "CONFIG_CONFIRMATION_REQUIRED", "MODEL_UNAVAILABLE", "CONFIG_UNSUPPORTED", "ANSWER_SELECTION_REQUIRED", "FORK_UNSUPPORTED", "IDEMPOTENCY_CONFLICT", "FORK_RESULT_UNAVAILABLE", "FORK_TOO_LARGE", "INVALID_REQUEST", "FORK_FAILED")), prop("message", strSchema()), prop("request_id", strSchema()))
	return s
}

func forkOpenAPIPaths(paths map[string]any) map[string]any {
	for _, config := range []struct{ Path, ID, Input, Output string }{
		{"fork-preview", "previewConversationFork", "ForkPreviewRequest", "ForkPreview"},
		{"forks", "createConversationFork", "ForkCreateRequest", "ForkResult"},
	} {
		params := []map[string]any{param("path", "conversation_id", true, strSchema())}
		if config.Path == "forks" {
			params = append(params, param("header", "Idempotency-Key", true, strSchema()))
		}
		operation := op(config.ID, params, jsonBody(refSchema(config.Input), true), response(200, "Success", refSchema(config.Output)))
		operation["operationId"] = config.ID
		operation["tags"] = []string{"Conversations"}
		responses := operation["responses"].(map[string]any)
		if config.Path == "forks" {
			responses["201"] = response(201, "Created", refSchema(config.Output))
		}
		for _, status := range []string{"400", "404", "409", "413", "500"} {
			responses[status] = map[string]any{"description": "Fork failed", "content": map[string]any{"application/json": map[string]any{"schema": refSchema("ForkError")}}}
		}
		paths["/conversations/{conversation_id}/"+config.Path] = map[string]any{"post": operation}
	}
	return paths
}
