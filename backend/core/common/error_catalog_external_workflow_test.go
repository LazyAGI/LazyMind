package common

import (
	"net/http"
	"testing"
)

func TestResolveWorkflowInputBindingValidationErrors(t *testing.T) {
	for _, sample := range []struct {
		name    string
		message string
		detail  any
	}{
		{"missing material", "input_bindings requires a nonempty material ID", nil},
		{"empty list", `input_bindings["materials"] must not be an empty list`, `input_bindings["materials"] must not be an empty list`},
		{"invalid list item", `input_bindings["materials"][12] requires an object with resource_id`, `input_bindings["materials"][12] requires an object with resource_id`},
		{"invalid hash", `input_bindings["materials"].content_hash must be a nonempty string`, `input_bindings["materials"].content_hash must be a nonempty string`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
				appErr := ResolveAppError(sample.message, status)
				if appErr.Code != 2000103 || appErr.Message != "Invalid request" || appErr.HTTPStatus != status {
					t.Fatalf("unexpected public validation error: %+v", appErr)
				}
				if appErr.Detail != sample.detail {
					t.Fatalf("Detail = %#v, want %#v", appErr.Detail, sample.detail)
				}
			}
		})
	}
}

func TestResolveWorkflowControlErrors(t *testing.T) {
	for _, sample := range []struct {
		message string
		status  int
		code    int
	}{
		{"workflow controls require an interactive single Chat run", http.StatusBadRequest, 2004901},
		{"native workflow controls require the LazyMind Chat executor", http.StatusConflict, 2004902},
		{"lease_token required", http.StatusBadRequest, 2004903},
		{"stream unavailable", http.StatusInternalServerError, 2004904},
		{"workflow state unavailable", http.StatusInternalServerError, 2004905},
		{"task cancellation rejected: HTTP 503", http.StatusInternalServerError, 2004906},
	} {
		t.Run(sample.message, func(t *testing.T) {
			appErr := ResolveAppError(sample.message, sample.status)
			if appErr.Code != sample.code || appErr.HTTPStatus != sample.status {
				t.Fatalf("unexpected workflow control error: %+v", appErr)
			}
		})
	}
}
