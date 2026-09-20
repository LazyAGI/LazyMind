package common

import "testing"

func TestResolveEvolutionErrorsUsesStableCatalogMessages(t *testing.T) {
	for _, sample := range []struct {
		source  string
		status  int
		code    int
		message string
	}{
		{"--user-id and --report are required", 400, 2000103, "Invalid request"},
		{"report must be a new writable file", 400, 2000103, "Invalid request"},
		{"cannot encode report", 500, 2000000, "Internal server error"},
		{"capability validation incomplete; inspect the private report", 500, 2000000, "Internal server error"},
		{"authorized model configuration unavailable", 403, 2001597, "model config unavailable"},
		{"evolution model unavailable", 422, 2001597, "model config unavailable"},
	} {
		t.Run(sample.source, func(t *testing.T) {
			appErr := ResolveAppError(sample.source, sample.status)
			if appErr.Code != sample.code || appErr.HTTPStatus != sample.status {
				t.Fatalf("Code = %d, HTTPStatus = %d; want %d, %d", appErr.Code, appErr.HTTPStatus, sample.code, sample.status)
			}
			if appErr.Message != sample.message || appErr.Detail != nil {
				t.Fatalf("Message = %q, Detail = %#v; want %q without detail", appErr.Message, appErr.Detail, sample.message)
			}
		})
	}
}
