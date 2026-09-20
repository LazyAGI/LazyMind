package common

import "net/http"

func init() {
	// The trusted validation CLI retains its diagnostics; API boundaries use
	// existing public error codes and messages if these errors propagate there.
	for _, source := range []string{
		"--user-id and --report are required",
		"cannot read isolated inputs",
		"report must be a new writable file",
	} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
	for _, source := range []string{
		"database unavailable",
		"cannot allocate validation nonce",
		"cannot prepare validation",
		"cannot encode report",
		"cannot save report",
		"cannot persist report",
		"configuration changed or evidence could not be saved; rerun validation",
		"capability validation incomplete; inspect the private report",
	} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusInternalServerError, 2000000)
	}
	for _, source := range []string{
		"authorized model configuration unavailable",
		"evolution model unavailable",
	} {
		registerAdditionalErrorAlias(source, "model config unavailable", http.StatusServiceUnavailable, 2001597)
	}
}
