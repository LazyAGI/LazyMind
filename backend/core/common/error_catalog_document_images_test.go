package common

import (
	"net/http"
	"testing"
)

func TestResolveDocumentImageErrors(t *testing.T) {
	for _, sample := range []struct {
		source  string
		status  int
		code    int
		message string
	}{
		{"Unable to prepare document images", http.StatusInternalServerError, 2000000, "Internal server error"},
		{"Unable to prepare document images", http.StatusForbidden, 2000000, "Internal server error"},
		{"image export unavailable", http.StatusInternalServerError, 2000000, "Internal server error"},
		{"image unavailable", http.StatusNotFound, 2000106, "Resource not found"},
		{"document nesting limit", http.StatusBadRequest, 2000103, "Invalid request"},
		{"too many images", http.StatusBadRequest, 2000103, "Invalid request"},
	} {
		t.Run(sample.source, func(t *testing.T) {
			got := ResolveAppError(sample.source, sample.status)
			if got.Code != sample.code || got.HTTPStatus != sample.status || got.Message != sample.message || got.Detail != nil {
				t.Fatalf("resolved image error = %#v, want code %d, status %d, message %q without detail", got, sample.code, sample.status, sample.message)
			}
		})
	}
}
