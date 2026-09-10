package main

import "strconv"

func conversationGroupSchemas() map[string]any {
	name := map[string]any{"type": "string", "minLength": 1, "maxLength": 24, "description": "Trimmed Unicode group name; unique ignoring case within the current user."}
	scope := map[string]any{"type": "string", "maxLength": 500, "description": "Optional user-editable collection scope. Empty string clears it. Changes affect subsequent organization only."}
	return map[string]any{
		"ConversationGroup": objReq([]string{"id", "name", "scope", "version", "member_count", "created_by", "created_at", "updated_at"},
			prop("pinned", boolSchema()), prop("sort_order", int64Schema()), prop("id", strSchema()), prop("name", name), prop("scope", scope), prop("version", int64Schema()), prop("member_count", int64Schema()), prop("created_by", enumStringSchema("user", "organizer")), prop("created_run_id", strSchema()), prop("created_at", dateTimeSchema()), prop("updated_at", dateTimeSchema())),
		"ConversationGroupPlacementRequest": obj(prop("pinned", boolSchema()), prop("before_group_id", strSchema())),
		"ConversationGroupCreateRequest":    objReq([]string{"name"}, prop("name", name), prop("scope", scope)),
		"ConversationGroupUpdateRequest": obj(prop("name", name), prop("scope", scope), prop("organizer_run_id", map[string]any{
			"type": "string", "description": "Optional successful organizer run context for editing a group created by that run from its result panel. Preserves version-fenced undo of panel corrections.",
		})),
		"ConversationGroupResponse":           objReq([]string{"group"}, prop("group", refSchema("ConversationGroup"))),
		"ConversationGroupListResponse":       objReq([]string{"groups", "total_size"}, prop("groups", array(refSchema("ConversationGroup"))), prop("total_size", int64Schema())),
		"ConversationGroupMember":             objReq([]string{"conversation_id", "display_name", "membership_revision"}, prop("conversation_id", strSchema()), prop("display_name", strSchema()), prop("summary", strSchema()), prop("pinned_at", nullableSchema(dateTimeSchema())), prop("created_at", dateTimeSchema()), prop("updated_at", dateTimeSchema()), prop("membership_revision", int64Schema())),
		"ConversationGroupDetailResponse":     objReq([]string{"group", "conversations", "total_size", "next_page_token"}, prop("group", refSchema("ConversationGroup")), prop("conversations", array(refSchema("ConversationGroupMember"))), prop("total_size", int64Schema()), prop("next_page_token", strSchema())),
		"ConversationGroupAssignRequest":      objReq([]string{"conversation_id"}, prop("conversation_id", strSchema())),
		"ConversationGroupMembershipResponse": objReq([]string{"conversation_id", "group_id"}, prop("conversation_id", strSchema()), prop("group_id", nullableSchema(strSchema()))),
		"ConversationOrganizerItem":           objReq([]string{"conversation_id", "title", "summary", "state", "corrected"}, prop("conversation_id", strSchema()), prop("title", strSchema()), prop("summary", strSchema()), prop("group_id", nullableSchema(strSchema())), prop("state", enumStringSchema("grouped", "free", "missing", "deleted", "archived")), prop("corrected", boolSchema()), prop("skip_reason", strSchema()), prop("unassigned_reason", enumStringSchema("no_matching_group", "below_min_group_size", "no_messages", "no_task_intent", "summary_failed", "unsupported_conversation")), prop("summary_error_code", strSchema())),
		"ConversationOrganizerRun": objReq([]string{"id", "status", "stage", "progress", "organized_count", "free_count", "skipped_count", "can_cancel", "can_retry", "can_undo", "created_at", "updated_at"},
			prop("steps", array(objReq([]string{"id", "status", "current", "total", "completed"}, prop("id", enumStringSchema("preparation", "organization", "review", "application")), prop("status", enumStringSchema("pending", "active", "completed", "failed", "canceled")), prop("detail", strSchema()), prop("current", int64Schema()), prop("total", int64Schema()), prop("completed", int64Schema())))),
			prop("id", strSchema()), prop("status", enumStringSchema("pending", "running", "applying", "succeeded", "failed", "canceled", "undone", "confirmed")), prop("stage", strSchema()),
			prop("model_progress", objReq([]string{"state", "received_chars", "elapsed_seconds", "idle_seconds"}, prop("state", strSchema()), prop("received_chars", int64Schema()), prop("elapsed_seconds", int64Schema()), prop("idle_seconds", int64Schema()), prop("first_response_at", strSchema()), prop("last_activity_at", strSchema()))),
			prop("progress", objReq([]string{"current", "total"}, prop("current", int64Schema()), prop("total", int64Schema()), prop("batch_current", int64Schema()), prop("batch_total", int64Schema()), prop("preparation_current", int64Schema()), prop("preparation_total", int64Schema()), prop("preparation_batch_current", int64Schema()), prop("preparation_batch_completed", int64Schema()), prop("preparation_batch_total", int64Schema()))),
			prop("organized_count", int64Schema()), prop("free_count", int64Schema()), prop("skipped_count", int64Schema()), prop("error", objReq([]string{"code", "message"}, prop("code", strSchema()), prop("message", strSchema()))),
			prop("can_cancel", boolSchema()), prop("can_retry", boolSchema()), prop("can_restart", boolSchema()), prop("can_undo", boolSchema()), prop("created_at", dateTimeSchema()), prop("updated_at", dateTimeSchema()), prop("items", array(refSchema("ConversationOrganizerItem")))),
		"ConversationOrganizerRunResponse":       objReq([]string{"run"}, prop("run", refSchema("ConversationOrganizerRun"))),
		"ConversationOrganizerLatestResponse":    objReq([]string{"run", "latest_successful_run_id"}, prop("run", nullableSchema(refSchema("ConversationOrganizerRun"))), prop("latest_successful_run_id", nullableSchema(strSchema())), prop("free_conversation_count", int64Schema())),
		"ConversationOrganizerCorrectionRequest": map[string]any{"type": "object", "description": "Provide group_id (null for free) or new_group, exclusively. Changes are saved immediately and included in this run's undo.", "properties": map[string]any{"group_id": nullableSchema(strSchema()), "new_group": refSchema("ConversationGroupCreateRequest")}, "oneOf": []any{map[string]any{"required": []string{"group_id"}}, map[string]any{"required": []string{"new_group"}}}},
		"ConversationChatGroupRequest":           map[string]any{"type": "object", "additionalProperties": true, "description": "Existing chat payload with optional group_id for the initial message of a new conversation. Creation and membership are atomic. A stale group is rejected.", "properties": map[string]any{"group_id": strSchema(), "conversation_id": strSchema(), "action": strSchema(), "data": obj()}},
	}
}

func conversationGroupPaths() map[string]any {
	groupID := queryParams(param("path", "group_id", true, strSchema()))
	memberID := queryParams(param("path", "group_id", true, strSchema()), param("path", "conversation_id", true, strSchema()))
	runID := queryParams(param("path", "run_id", true, strSchema()))
	operation := func(id, summary string, params []map[string]any, body map[string]any, status int, schema string) map[string]any {
		value := op(summary, params, body, response(status, summary, refSchema(schema)))
		value["operationId"] = id
		value["tags"] = []string{"ConversationGroups"}
		value["responses"] = map[string]any{strconv.Itoa(status): response(status, summary, refSchema(schema)), "400": response(400, "Invalid input", refSchema("ErrorResponse")), "404": response(404, "Conversation, group or run is unavailable", refSchema("ErrorResponse")), "409": response(409, "Name, membership, scope version or task conflict", refSchema("ErrorResponse"))}
		return value
	}
	paths := map[string]any{
		"/conversation-groups": map[string]any{
			"get":  operation("listConversationGroups", "List active conversation groups", queryParams(param("query", "keyword", false, strSchema())), nil, 200, "ConversationGroupListResponse"),
			"post": operation("createConversationGroup", "Create a group without a minimum member count", nil, jsonBody(refSchema("ConversationGroupCreateRequest"), true), 201, "ConversationGroupResponse")},
		"/conversation-groups/{group_id}/placement": map[string]any{"patch": operation("updateConversationGroupPlacement", "Persist pinning and order within the user navigation", groupID, jsonBody(refSchema("ConversationGroupPlacementRequest"), true), 200, "ConversationGroupListResponse")},
		"/conversation-groups/{group_id}": map[string]any{
			"get":    operation("getConversationGroup", "Get a group and its paginated conversations", append(groupID, param("query", "page_size", false, intSchema()), param("query", "page_token", false, strSchema()), param("query", "keyword", false, strSchema())), nil, 200, "ConversationGroupDetailResponse"),
			"patch":  operation("updateConversationGroup", "Edit name or scope and increment group version", groupID, jsonBody(refSchema("ConversationGroupUpdateRequest"), true), 200, "ConversationGroupResponse"),
			"delete": operation("deleteConversationGroup", "Remove a group and preserve all conversations", groupID, nil, 200, "EmptyObject")},
		"/conversation-groups/{group_id}/conversations":                   map[string]any{"post": operation("assignConversationGroup", "Move a conversation into a group", groupID, jsonBody(refSchema("ConversationGroupAssignRequest"), true), 200, "ConversationGroupMembershipResponse")},
		"/conversation-groups/{group_id}/conversations/{conversation_id}": map[string]any{"delete": operation("removeConversationGroupMember", "Make a current member free", memberID, nil, 200, "ConversationGroupMembershipResponse")},
		"/conversation-organizer-runs":                                    map[string]any{"post": operation("startConversationOrganizer", "Freeze eligible free conversations and enqueue organization; return an existing active run on repeated start", nil, nil, 202, "ConversationOrganizerRunResponse")},
		"/conversation-organizer-runs:latest":                             map[string]any{"get": operation("getLatestConversationOrganizer", "Get the active run or most recent result", nil, nil, 200, "ConversationOrganizerLatestResponse")},
		"/conversation-organizer-runs/{run_id}":                           map[string]any{"get": operation("getConversationOrganizer", "Get progress and immediately persisted results", runID, nil, 200, "ConversationOrganizerRunResponse")},
		"/conversation-organizer-runs/{run_id}/items/{conversation_id}":   map[string]any{"patch": operation("correctConversationOrganizerItem", "Correct a result immediately", queryParams(param("path", "run_id", true, strSchema()), param("path", "conversation_id", true, strSchema())), jsonBody(refSchema("ConversationOrganizerCorrectionRequest"), true), 200, "ConversationOrganizerRunResponse")},
		"/conversations:chat":                                             map[string]any{"post": sseOp("Chat; optional group_id atomically assigns a newly created conversation", jsonBody(refSchema("ConversationChatGroupRequest"), true), response(200, "Chat event stream", strSchema()))},
	}
	for _, action := range []string{"cancel", "retry", "undo", "confirm"} {
		paths["/conversation-organizer-runs/{run_id}:"+action] = map[string]any{"post": operation(action+"ConversationOrganizer", action+" organization", runID, nil, 200, "ConversationOrganizerRunResponse")}
	}
	return paths
}
