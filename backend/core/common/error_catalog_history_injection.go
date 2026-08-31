package common

import "net/http"

// History injection is a startup/maintenance facility rather than a public API.
// Keep its detailed filesystem, SQL, and bundle diagnostics in AppError.Detail
// while exposing one stable catalog code if an error reaches an API boundary.
func init() {
	const (
		message = "History injection failed"
		code    = 2002318
	)
	for _, alias := range []string{
		"history injection failed",
		"usage",
		"acl_db_driver and acl_db_dsn are required",
		"history injection output already exists",
		"history injection requires a database and target owner",
		"history injection sql_file is unsafe",
		"history injection payload path is unsafe",
		"history injection zip already exists",
		"resolve bootstrap admin for history injection",
		"auth login response has no access token",
		"auth me response has no user_id",
		"history injection sql contains an unterminated string literal",
		"inspect postgresql boolean columns",
	} {
		registerAdditionalErrorAlias(alias, message, http.StatusInternalServerError, code)
	}
	for _, template := range []string{
		"unknown history-injection command %q",
		"parse history injection manifest %s",
		"validate history injection manifest %s",
		"open history injection zip %s",
		"parse history injection manifest in %s",
		"validate history injection manifest in %s",
		"history injection zip %s has no root manifest.json",
		"history injection zip contains unsafe path %q",
		"history injection zip contains unsupported symlink %q",
		"history injection zip exceeds %d extracted bytes",
		"history injection export %s is required",
		"conversation %s was not found",
		"conversation %s has no %s workflow session",
		"workflow revision %s was not found",
		"query %s for history injection export",
		"history injection source payload %s",
		"history injection payload may not contain symlink %s",
		"history injection workspace may not contain symlink %s",
		"history injection zip source contains symlink %s",
		"apply history injection bundle %s",
		"materialized bundle id changed from %q to %q",
		"conversation %s already belongs to another user",
		"read injected conversation %s metadata",
		"decode injected conversation %s metadata",
		"encode injected conversation %s metadata",
		"normalize injected conversation %s",
		"execute sql statement %d",
		"workflow %s is not installed",
		"workflow revision %s belongs to a different workflow resource",
		"workflow revision %s has invalid compiled_graph",
		"runtime root %s is not configured",
		"payload checksum mismatch for %s",
		"history injection manifest schema_version %d is unsupported",
		"history injection manifest %s is required",
		"history injection payload target_root %q is unsupported",
		"history injection payload metadata is invalid for %q",
		"history injection sql contains unsafe table %q",
		"auth login returned http %d",
		"auth me returned http %d",
	} {
		registerAdditionalErrorPattern(template, message, http.StatusInternalServerError, code)
	}
}
