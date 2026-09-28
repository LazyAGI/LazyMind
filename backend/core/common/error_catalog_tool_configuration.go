package common

import "net/http"

func init() {
	registerAdditionalErrorAlias("configuration unavailable", "tool config unavailable", http.StatusServiceUnavailable, 2001994)
	registerAdditionalErrorAlias("cannot prepare configuration", "Could not prepare tool configuration", http.StatusBadRequest, 2003140)
	registerAdditionalErrorAlias("too many actions", "Too many tool configuration actions", http.StatusBadRequest, 2003136)
	registerAdditionalErrorAlias("acknowledgement failed", "Could not acknowledge tool configuration", http.StatusInternalServerError, 2003137)
	registerAdditionalErrorAlias("unknown service", "Unknown tool service", http.StatusBadRequest, 2003138)
	registerAdditionalErrorAlias("configuration changed concurrently; retry", "Tool configuration changed; retry", http.StatusConflict, 2003139)
	for _, source := range []string{"resolve %s connection %s", "read %s connection %s token", "read %s connection %s"} {
		registerAdditionalErrorPattern(source, "Upstream service error", http.StatusBadGateway, 2000110)
	}
}
