package common

import "net/http"

func init() {
	registerAdditionalErrorAlias("work artifact revision not found", "Resource not found", http.StatusNotFound, 2000106)
	registerAdditionalErrorAlias("work artifact revision is not published", "Invalid request", http.StatusBadRequest, 2000103)
	for _, source := range []string{
		"multipart file is required", "one file is required", "one file part named file is required",
		"exactly one file is accepted per original upload", "original url must be http(s) without credentials",
		"download filename or supported mime required", "folders have no original", "original must be a regular file",
		"original is not a pdf", "pdf is incomplete", "original is not a valid zip container",
		"office container does not match filename", "original mime mismatch", "encoded response cannot preserve original bytes",
		"device paths are not supported", "regular file required", "image format does not match filename",
		"empty file or upload size limit exceeded", "upload batch exceeds 1 gib",
		"upload size limit exceeded", "zip upload size limit exceeded",
	} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
	registerAdditionalErrorPattern("file %d", "Invalid request", http.StatusBadRequest, 2000103)
	for _, source := range []string{
		"original url uses a non-public address", "original host resolves to a non-public address",
		"artifact does not belong to current tenant or identity",
	} {
		registerAdditionalErrorAlias(source, "forbidden", http.StatusForbidden, 2000102)
	}
	registerAdditionalErrorAlias("original exceeds size limit", "file exceeds max size", http.StatusRequestEntityTooLarge, 2001380)
	for _, source := range []string{
		"original host has no addresses", "too many original redirects", "artifact original unavailable",
		"artifact has no original bytes", "artifact original metadata mismatch",
	} {
		registerAdditionalErrorAlias(source, "Upstream service error", http.StatusBadGateway, 2000110)
	}
	registerAdditionalErrorPattern("original download returned http %d", "Upstream service error", http.StatusBadGateway, 2000110)
	for _, source := range []string{
		"artifact revision is newer than its published delivery", "artifact revision was not fixed",
		"dataset no longer accepts task publication", "document ownership changed before publication",
		"document changed before task publication", "task no longer owns this document",
		"task ownership changed before publication", "transfer target is no longer active",
		"dataset changed during ownership backfill",
	} {
		registerAdditionalErrorAlias(source, "Conflict", http.StatusConflict, 2000107)
	}
	for _, source := range []string{
		"document ownership backfill is not ready", "ownership backfill databases are unavailable",
		"original service requires database", "transaction and prepared original required",
		"historical document has unknown owner", "historical document association crosses tenant boundary",
	} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusInternalServerError, 2000000)
	}
	for _, source := range []string{"ambiguous core ownership for kb %s", "historical document %s has no original metadata"} {
		registerAdditionalErrorPattern(source, "Internal server error", http.StatusInternalServerError, 2000000)
	}
}
