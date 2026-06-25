package agent

import (
	"net/http"
	"os"
	"strings"

	"lazymind/core/common"
)

const evoServiceDisabledMessage = "algorithm leap service is not started in this runtime"

// RequireEvoServiceEnabled keeps the algorithm leap API surface stable when the
// standalone evo service is intentionally disabled for local runtime profiles.
func RequireEvoServiceEnabled(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !evoServiceEnabled() {
			common.ReplyErrWithData(w, evoServiceDisabledMessage, map[string]any{
				"service": "evo-api",
				"status":  "not_started",
			}, http.StatusServiceUnavailable)
			return
		}
		next(w, r)
	}
}

func evoServiceEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_EVO_SERVICE_ENABLED"))) {
	case "", "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
