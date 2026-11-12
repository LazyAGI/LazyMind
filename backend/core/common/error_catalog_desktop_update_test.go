package common

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopUpdateErrorsUseExistingPublicFamilies(t *testing.T) {
	for _, sample := range []struct {
		source  string
		status  int
		code    int
		message string
	}{
		{"update_transaction_busy", http.StatusConflict, ErrCodeConflict, "Conflict"},
		{"component_install_failed", http.StatusServiceUnavailable, ErrCodeInternal, "Internal server error"},
		{"component_state_unknown", http.StatusServiceUnavailable, ErrCodeInternal, "Internal server error"},
		{"component_file_invalid", http.StatusInternalServerError, ErrCodeInternal, "Internal server error"},
		{"component_path_invalid", http.StatusInternalServerError, ErrCodeInternal, "Internal server error"},
		{"component_target_verification_failed", http.StatusInternalServerError, ErrCodeInternal, "Internal server error"},
		{"update_bootstrap_invalid", http.StatusInternalServerError, ErrCodeInternal, "Internal server error"},
	} {
		t.Run(sample.source, func(t *testing.T) {
			response := httptest.NewRecorder()
			ReplyErr(response, sample.source, sample.status)
			var body APIResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != sample.status || body.Code != sample.code || body.Message != sample.message || body.Data != nil {
				t.Fatalf("response status=%d body=%#v, want status=%d code=%d message=%q without internal detail", response.Code, body, sample.status, sample.code, sample.message)
			}
		})
	}
}
