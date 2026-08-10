package common

import "net/http"

// Memory and episode APIs predate the audited Core error catalog. Keep their
// implementation details out of the public error contract by mapping them to
// the existing shared error classes.
func init() {
	for _, source := range []string{
		"stored current memory avatar is invalid",
		"invalid current memory request",
		"stored current memory document is invalid",
		"invalid current memory document",
		"invalid iso datetime",
		"schema_version must be a positive integer",
		"document must not contain the reserved version field",
		"document must contain at least one leaf field",
		"presentation section requires path and labels",
		"presentation must declare at least one title field",
		"presentation is missing fields",
		"mapping keys must be non-empty strings without dots",
		"episode_id is required",
		"terms must be a non-empty array",
		"limit must be between 1 and 100",
		"episode_ids must be a non-empty array",
		"page_size must be between 1 and 100",
		"episode_type is invalid",
		"source_kind is invalid",
		"summary is required",
		"summary must be at most 200 characters",
		"search_text is required",
		"tokenizer_version is required",
		"occurred_at_ms must be positive",
		"invalid memory path",
		"invalid remote fs path",
		"unsupported remote fs operation",
		"remote fs operation unsupported",
		"conversation_id, user_id, and last_history_id are required",
	} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}

	registerAdditionalErrorAlias("conversation_id is required", "conversation_id is required", http.StatusBadRequest, 2000224)
	registerAdditionalErrorAlias("memory user_id is required", "user_id required", http.StatusBadRequest, 2000900)

	for _, source := range []string{
		"current memory resource not found",
		"current memory entry not found",
		"episode not found",
		"memory path not found",
	} {
		registerAdditionalErrorAlias(source, "Resource not found", http.StatusNotFound, 2000106)
	}

	for _, source := range []string{
		"current memory update conflict",
		"current memory content conflict",
		"memory path conflict",
		"remote fs conflict",
	} {
		registerAdditionalErrorAlias(source, "Conflict", http.StatusConflict, 2000107)
	}
	registerAdditionalError("preference etag conflict", http.StatusConflict, 2001992)

	for _, source := range []string{
		"revision/plugin views are read-only",
		"revision/workflow views are read-only",
		"copy across skill and memory mounts is not allowed",
		"move across skill and memory mounts is not allowed",
		"memory mount is protected",
	} {
		registerAdditionalErrorAlias(source, "forbidden", http.StatusForbidden, 2000102)
	}

	for _, source := range []string{
		"current memory operation failed",
		"memory module is not configured",
		"memory store db is not configured",
		"episode repository unavailable",
		"episode repository requires a database",
		"episode initialization requires a database",
		"create episode failed",
		"delete episode failed",
		"search episode candidates failed",
		"list conversation episodes failed",
		"list recent episodes failed",
		"record episode hits failed",
		"list episodes failed",
		"get episode failed",
		"create episode",
		"get episode",
		"list conversation episodes",
		"list recent episodes",
		"count episodes",
		"list episodes",
		"delete episode",
		"record episode hits",
		"encode page token",
		"search postgres episodes",
		"inspect sqlite episode fts schema",
		"inspect sqlite episode fts triggers",
		"initialize sqlite episode fts schema",
		"inspect sqlite episode fts missing rows",
		"inspect sqlite episode fts stale rows",
		"rebuild sqlite episode fts index",
		"search sqlite episode fts",
	} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusInternalServerError, 2000000)
	}

	for _, pattern := range []string{
		"%s must be a positive integer",
		"%s must be a positive integer, got %q",
		"unsupported %s operation path %q",
		"clear operation on %q must not include value",
		"%s string path %q only supports set or clear",
		"%s null path %q only supports set or clear",
		"%s list path %q changed type",
		"operation %q on %q requires value",
		"operation %q on %q requires a non-empty value",
		"%s list path %q only supports add, remove, or clear",
		"presentation references unknown section %q",
		"presentation section %q must reference a mapping",
		"presentation references unknown field %q",
		"presentation field %q is outside section %q",
		"presentation field %q is duplicated",
		"presentation field %q has invalid metadata",
		"presentation fallback %q requires zh-cn and en-us",
		"field %q must be a string, null, or list of strings",
		"limit must be between 1 and %d",
		"episode search does not support %q",
	} {
		registerAdditionalErrorPattern(pattern, "Invalid request", http.StatusBadRequest, 2000103)
	}
	registerAdditionalErrorPattern("%s must be configured for core internal apis", "Internal server error", http.StatusInternalServerError, 2000000)
	registerAdditionalErrorAlias("memory review failed", "Internal server error", http.StatusInternalServerError, 2000000)
}
