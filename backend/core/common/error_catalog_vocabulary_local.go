package common

import "net/http"

func init() {
	registerAdditionalErrorAlias("anki endpoint cannot change while synced notes or pending operations exist", "Anki endpoint cannot change while synced notes or pending operations exist", http.StatusConflict, 2005310)
	registerAdditionalErrorAlias("anki endpoint changed during sync", "Anki endpoint changed during sync; retry with the current connection", http.StatusConflict, 2005311)
	registerAdditionalErrorPattern("anki sync operation %s", "Anki sync failed; your local word is preserved", http.StatusBadGateway, 2005312)
	registerAdditionalErrorPattern("unsupported queued anki operation %q", "Unsupported queued Anki sync operation", http.StatusInternalServerError, 2005313)
	registerAdditionalErrorAlias("wordbook does not support vocabulary review", "This collection does not support vocabulary review", http.StatusBadRequest, 2005314)
	registerAdditionalErrorAlias("review session uses a legacy anki provider", "This review session uses legacy Anki data; start a new local review", http.StatusConflict, 2005315)
	registerAdditionalErrorAlias("anki sync is unavailable on this deployment", "Anki sync is unavailable on this deployment; local vocabulary remains available", http.StatusServiceUnavailable, 2005316)
	registerAdditionalErrorAlias("anki sync is disabled", "Anki sync is disabled; enable it in external application settings", http.StatusConflict, 2005317)
}
