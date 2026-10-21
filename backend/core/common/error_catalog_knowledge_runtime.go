package common

import "net/http"

func init() {
	for _, source := range []string{"authenticated execution missing", "execution missing", "knowledge execution is inactive or outside allowed scope", "account identity unavailable or disabled", "account identity mismatch", "expired or invalid credential", "identity changed", "tool not allowed or missing call id", "tool no longer allowed", "operation is not authorized", "dispatch execution mismatch"} {
		registerAdditionalErrorAlias(source, "forbidden", http.StatusForbidden, ErrCodeForbidden)
	}
	for _, source := range []string{"file_ref required", "artifact_id required", "artifact_id and fixed revision_id required", "empty targets", "empty sources", "arguments must be an object", "call id reused with different arguments", "confirmation expired", "confirmation belongs to another execution", "decision identity or snapshot mismatch", "knowledge decision history binding changed", "invalid knowledge decision persistence", "operation does not require confirmation", "decision already made", "resources changed; prepare and confirm again"} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
	for _, source := range []string{"missing pinned step", "confirmation history unavailable", "knowledge runtime dependencies missing", "knowledge execution guard unavailable"} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusInternalServerError, 2000000)
	}

	for _, source := range []string{"permission denied", "source access denied", "tool not allowed", "knowledge operation execution missing or invalid"} {
		registerAdditionalErrorAlias(source, "forbidden", http.StatusForbidden, ErrCodeForbidden)
	}
	for _, source := range []string{"there are no failed items to retry", "batch is already processing", "choose chat history or work task", "conversation_id and history_id required", "selected source no longer exists", "idempotency-key required (max 128 characters)", "read-only tools do not create operations", "external code reference"} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
	for _, source := range []string{"document status unavailable", "retry service unavailable", "tool visibility unavailable", "task steps unavailable", "missing reparse receipt", "target unavailable", "source task unavailable", "source conversation unavailable", "source answer unavailable", "verified identity missing", "document reparse failed", "document processing submission failed"} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusInternalServerError, 2000000)
	}
	for _, source := range []string{"import attempt superseded", "document already bound by a newer attempt"} {
		registerAdditionalErrorAlias(source, "Conflict", http.StatusConflict, 2000107)
	}
	registerAdditionalErrorPattern("folder %s metadata", "Internal server error", http.StatusInternalServerError, 2000000)
}
