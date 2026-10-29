package common

import "net/http"

func init() {
	for _, source := range []string{"selected model unavailable", "selected model credential unavailable", "explanation too large"} {
		registerAdditionalErrorAlias(source, "Invalid request", http.StatusBadRequest, ErrCodeInvalidParams)
	}
	registerAdditionalErrorAlias("formula not found", "Resource not found", http.StatusNotFound, ErrCodeResourceAbsent)
}
