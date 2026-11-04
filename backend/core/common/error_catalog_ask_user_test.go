package common

import (
	"net/http"
	"testing"
)

func TestResolveAskUserValidationErrors(t *testing.T) {
	for _, sample := range []struct {
		message string
		detail  any
	}{
		{"answer length is outside the allowed range", nil},
		{"answer must be an integer in the allowed range", nil},
		{"unsupported answer format", nil},
		{"invalid answer validation range", nil},
		{"scope requires at least one selection", "scope requires at least one selection"},
		{"word_target requires a scalar answer", "word_target requires a scalar answer"},
		{"scope requires scalar selections", "scope requires scalar selections"},
		{"scope contains duplicate selections", "scope contains duplicate selections"},
		{"topic requires a non-empty answer", "topic requires a non-empty answer"},
		{"generate_background_images requires a yes/no answer", "generate_background_images requires a yes/no answer"},
		{"paper_language has an unsupported choice", "paper_language has an unsupported choice"},
	} {
		t.Run(sample.message, func(t *testing.T) {
			for _, status := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
				got := ResolveAppError(sample.message, status)
				if got.Code != 2000103 || got.Message != "Invalid request" || got.HTTPStatus != status {
					t.Fatalf("unexpected public validation error: %+v", got)
				}
				if got.Detail != sample.detail {
					t.Fatalf("Detail = %#v, want %#v", got.Detail, sample.detail)
				}
			}
		})
	}
}
