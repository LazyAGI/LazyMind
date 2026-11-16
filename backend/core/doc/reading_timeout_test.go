package doc

import (
	"context"
	"fmt"
	"lazymind/core/algo"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadingTimeoutResponse(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, fmt.Errorf("generation: %w", &algo.StructuredTaskError{Code: "request_timeout"})} {
		w := httptest.NewRecorder()
		replyReadingServiceError(w, err)
		if w.Code != 504 || !strings.Contains(w.Body.String(), `"error_code":"request_timeout"`) || !strings.Contains(w.Body.String(), "2 minutes") {
			t.Fatalf("timeout response = %d %s", w.Code, w.Body)
		}
	}
	w := httptest.NewRecorder()
	replyReadingServiceError(w, &algo.StructuredTaskError{Code: "authentication_failed"})
	if w.Code != 502 || strings.Contains(w.Body.String(), `"error_code":"request_timeout"`) {
		t.Fatalf("non-timeout misclassified: %d %s", w.Code, w.Body)
	}
}
