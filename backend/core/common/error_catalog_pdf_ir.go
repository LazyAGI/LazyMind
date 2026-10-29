package common

import "net/http"

// PDF interpretation uses the existing translated infrastructure error classes.
// The document status response retains its actionable parser diagnostics.
func init() {
	for _, source := range []string{
		"pdf ir status unavailable", "enqueue pdf parsing failed",
		"create pdf ir artifact directory failed", "create pdf snapshot failed",
		"save mineru raw result failed", "save pdf ir failed",
	} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusInternalServerError, 2000000)
	}
	for _, source := range []string{
		"mineru request failed", "mineru api rejected request", "mineru parsing failed",
		"mineru result has invalid layout data", "untrusted mineru artifact url",
		"mineru response exceeds size limit", "archive too large", "unsafe archive member",
		"ambiguous content lists", "missing content list",
	} {
		registerAdditionalErrorAlias(source, "Upstream service error", http.StatusBadGateway, 2000110)
	}
	registerAdditionalErrorPattern("mineru http %d", "Upstream service error", http.StatusBadGateway, 2000110)
	for _, source := range []string{
		"pdf source changed; request parsing again", "pdf changed while preparing parsing", "pdf source changed during parsing",
	} {
		registerAdditionalErrorAlias(source, "Conflict", http.StatusConflict, 2000107)
	}
	for _, source := range []string{"pdf source unavailable", "pdf document unavailable"} {
		registerAdditionalErrorAlias(source, "Resource not found", http.StatusNotFound, 2000106)
	}
	registerAdditionalErrorAlias("pdf ir permission revoked", "forbidden", http.StatusForbidden, 2000102)
	registerAdditionalErrorAlias("pdf ir account identity unavailable or disabled", "unauthorized", http.StatusUnauthorized, 2000104)
	registerAdditionalErrorAlias("selected mineru credential is missing", "unauthorized", http.StatusUnauthorized, 2000104)
	for _, source := range []string{
		"pdf ir requires an original pdf", "pdf exceeds 128 mib limit",
		"select a mineru ocr credential in model settings",
		"selected local/custom mineru service does not support pdf ir; no cloud upload was performed",
	} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
	registerAdditionalErrorAlias("mineru configuration unavailable", "Internal server error", http.StatusServiceUnavailable, 2000000)
}
