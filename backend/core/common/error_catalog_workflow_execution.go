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
	registerAdditionalError("artifact action unavailable", http.StatusUnprocessableEntity, 2005500)
	registerAdditionalError("edit skill contract unavailable", http.StatusUnprocessableEntity, 2005501)
	registerAdditionalError("edit skills unavailable", http.StatusUnprocessableEntity, 2005502)
	registerAdditionalError("workflow package does not support completed artifact edits; update the package", http.StatusUnprocessableEntity, 2005503)
	registerAdditionalError("update the workflow package to enable external ai editing", http.StatusUnprocessableEntity, 2005504)
	registerAdditionalError("connect an external agent before editing this artifact", http.StatusConflict, 2005505)
	registerAdditionalError("complete or resume the workflow before editing", http.StatusConflict, 2005506)
	registerAdditionalError("cannot queue external edit", http.StatusConflict, 2005507)
	registerAdditionalError("stale or invalid edit response", http.StatusConflict, 2005508)
	registerAdditionalError("edit generation limit reached", http.StatusConflict, 2005509)
	registerAdditionalError("edit changed during generation; reload before retrying", http.StatusConflict, 2005510)
	registerAdditionalError("external preview confirmation required", http.StatusConflict, 2005511)
	registerAdditionalError("stale or cancelled edit preview", http.StatusConflict, 2005512)
	registerAdditionalError("artifact version changed; reload edit context", http.StatusConflict, 2005513)
	registerAdditionalError("confirm or cancel the pending ai preview before a chat edit", http.StatusConflict, 2005514)
	registerAdditionalError("this conversation displays external workflow results; use the workflow controls or continue with the external agent", http.StatusConflict, 2005515)
	registerAdditionalError("a bound external agent document workflow is required", http.StatusConflict, 2005516)
	registerAdditionalError("model request belongs to an inactive attempt", http.StatusConflict, 2005517)
	registerAdditionalError("model request cannot be claimed", http.StatusConflict, 2005518)
	registerAdditionalError("model response is stale or invalid", http.StatusConflict, 2005519)
	registerAdditionalError("model response does not match its claimed request", http.StatusConflict, 2005520)
	registerAdditionalError("a completed generation cannot be replaced", http.StatusConflict, 2005521)
	registerAdditionalError("model request is not running", http.StatusConflict, 2005522)
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
		"A selected document changed before execution", http.StatusConflict, 2005524)
}
