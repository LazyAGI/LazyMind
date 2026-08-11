package chat

import (
	"os"
	"testing"
)

// TestAuthServiceInternalHeaders_NoEnv returns empty map when env var not set.
func TestAuthServiceInternalHeaders_NoEnv(t *testing.T) {
	// Ensure env is not set.
	os.Unsetenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")
	headers := authServiceInternalHeaders()
	if len(headers) != 0 {
		t.Fatalf("headers: got %v, want empty", headers)
	}
}

// TestAuthServiceInternalHeaders_WithEnv returns the header when env var is set.
func TestAuthServiceInternalHeaders_WithEnv(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "secret-token-123")
	headers := authServiceInternalHeaders()
	if got := headers["X-LazyMind-Internal-Token"]; got != "secret-token-123" {
		t.Fatalf("header: got %q, want secret-token-123", got)
	}
}

// TestAuthServiceInternalHeaders_WhitespaceToken is treated as empty.
func TestAuthServiceInternalHeaders_WhitespaceToken(t *testing.T) {
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "   ")
	headers := authServiceInternalHeaders()
	if len(headers) != 0 {
		t.Fatalf("headers: got %v, want empty for whitespace token", headers)
	}
}
