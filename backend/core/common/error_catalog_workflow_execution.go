package common

import "net/http"

func init() {
	for _, message := range []string{
		"workflow tasks unavailable",
		"cannot read queued edits",
		"cannot load artifacts",
		"workflow input checkpoint must have one selected question artifact",
		"workflow input checkpoint requires a questions array",
		"workflow input question requires text",
		"package blob hash mismatch",
	} {
		registerAdditionalErrorAlias(message, "Internal server error", http.StatusInternalServerError, ErrCodeInternal)
	}
	for _, message := range []string{
		"companion artifacts are server-owned",
		"a stable command_id is required",
		"media inputs must use resource:<id> from workflow.input.import",
		"media inputs are empty or exceed 20 mib",
		"media input resource content hash does not match",
		"media conditioning requires a png, jpeg, gif or webp image",
		"cannot remove every page",
		"linked page counts differ",
		"duplicate item",
		"new item needs a value",
		"content_type and non-null value required",
		"image value requires a non-empty image url, an object with path/url, or an uploaded image via local_path",
		"runtime.external_execution is unsupported; declare runtime.execution",
		"execution step must be a mapping",
		"an assistant message is required",
		"package code exceeds tool execution limit",
	} {
		registerAdditionalErrorAlias(message, "Invalid request", http.StatusBadRequest, ErrCodeInvalidParams)
	}
	for _, message := range []string{
		"artifact action unavailable",
		"edit skill contract unavailable",
		"edit skills unavailable",
		"workflow package does not support completed artifact edits; update the package",
		"update the workflow package to enable external ai editing",
	} {
		registerAdditionalErrorAlias(message, message, http.StatusUnprocessableEntity, ErrCodeInvalidParams)
	}
	for _, message := range []string{
		"connect an external agent before editing this artifact",
		"complete or resume the workflow before editing",
		"cannot queue external edit",
		"stale or invalid edit response",
		"edit generation limit reached",
		"edit changed during generation; reload before retrying",
		"external preview confirmation required",
		"stale or cancelled edit preview",
		"artifact version changed; reload edit context",
		"confirm or cancel the pending ai preview before a chat edit",
		"this conversation displays external workflow results; use the workflow controls or continue with the external agent",
		"a bound external agent document workflow is required",
		"model request belongs to an inactive attempt",
		"model request cannot be claimed",
		"model response is stale or invalid",
		"model response does not match its claimed request",
		"a completed generation cannot be replaced",
		"model request is not running",
	} {
		registerAdditionalErrorAlias(message, message, http.StatusConflict, ErrCodeConflict)
	}
	for _, message := range []string{"artifact edit not found", "media input resource is unavailable to this user"} {
		registerAdditionalErrorAlias(message, "Resource not found", http.StatusNotFound, ErrCodeResourceAbsent)
	}
	for _, pattern := range []string{
		"updated %s required",
		"linked list %s required",
		"slot %s is not editable",
		"invalid single slot %s",
		"unknown item in %s",
		"execution step field %q is unsupported; prompts, outputs and acceptance belong in scenario/state.yml",
	} {
		registerAdditionalErrorPattern(pattern, "Invalid request", http.StatusBadRequest, ErrCodeInvalidParams)
	}
	registerAdditionalErrorPattern("selected document %q changed before execution",
		"A selected document changed before execution", http.StatusConflict, ErrCodeConflict)
}
