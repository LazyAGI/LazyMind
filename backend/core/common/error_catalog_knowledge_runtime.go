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
}
