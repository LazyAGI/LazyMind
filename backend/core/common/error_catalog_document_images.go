package common

import "net/http"

func init() {
	for _, source := range []string{"unable to prepare document images", "image export unavailable"} {
		registerAdditionalErrorAlias(source, "Internal server error", http.StatusInternalServerError, 2000000)
	}
	registerAdditionalErrorAlias("image unavailable", "Resource not found", http.StatusNotFound, 2000106)
	for _, source := range []string{"document nesting limit", "too many images"} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, 2000103)
	}
}
