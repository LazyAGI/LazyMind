package common

import "net/http"

func init() {
	registerAdditionalErrorAlias("agent not allowed", "forbidden", http.StatusForbidden, 2000102)
	registerAdditionalErrorAlias("idempotency conflict", "Conflict", http.StatusConflict, 2000107)
	for _, source := range []string{
		"task_description is required", "invalid skill import url", "skill import url must use http or https",
	} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
	for _, pattern := range []string{
		"unsupported skill source type %q",
		"input_bindings[%q] requires an object with resource_id",
		"input_bindings[%q]", "input_bindings[%q].revision must be a positive integer",
		"input_bindings[%q].content_hash must be a string",
		"input_bindings[%q] requires material_id and resource_id",
		"input_files[%d]", "input_files[%d] requires material_id, name and mime_type",
		"input_files[%d].content_base64 must encode a non-empty file",
		"input_files[%d].content_hash does not match the file",
		"input_files[%d] must be an object",
		"input_files[%d] requires material_id, name, mime_type, and content_base64",
		"input_files[%d] content_base64 is invalid", "input_files[%d] content is empty",
		"input_files[%d] content_hash mismatch",
	} {
		registerAdditionalErrorPattern(pattern, "Invalid request", http.StatusBadRequest, 2000103)
	}
	for _, source := range []string{
		"collect handed-off task", "persist external task", "create workflow draft failed",
		"create external workflow task failed", "advance workflow step rejected",
		"load cached analysis failed", "copy cached analysis failed", "bind cached analysis failed", "read draft failed",
		"workflow setting scope is incomplete", "external workflow chat history not found",
	} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusInternalServerError, 2000000)
	}
}
