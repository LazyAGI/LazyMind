package doc

import (
	"net/http/httptest"
	"testing"
)

func TestScanSourceAccessHeadersIncludeInternalServiceToken(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-token")
	req := httptest.NewRequest("POST", "/datasets/dataset-1/uploads", nil)
	req.Header.Set("X-User-Id", "user-1")

	headers := scanSourceAccessHeaders(req)

	if headers["X-User-ID"] != "user-1" {
		t.Fatalf("expected user id to be forwarded, got %+v", headers)
	}
	if headers["X-LazyMind-Internal-Token"] != "internal-token" {
		t.Fatalf("expected internal service token to be forwarded")
	}
}
