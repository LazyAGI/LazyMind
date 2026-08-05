package common

import (
	"net/http"
	"testing"
)

// TestCompactNonEmptyStrings_RemovesDuplicates verifies deduplication and whitespace trimming.
func TestCompactNonEmptyStrings_RemovesDuplicates(t *testing.T) {
	got := compactNonEmptyStrings([]string{" a ", "b", "  a  ", "", "c", "b"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("compactNonEmptyStrings: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("compactNonEmptyStrings[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCompactNonEmptyStrings_EmptyInput returns an empty slice.
func TestCompactNonEmptyStrings_EmptyInput(t *testing.T) {
	got := compactNonEmptyStrings([]string{})
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

// TestCompactNonEmptyStrings_AllWhitespace returns empty.
func TestCompactNonEmptyStrings_AllWhitespace(t *testing.T) {
	got := compactNonEmptyStrings([]string{"  ", "   "})
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

// TestFirstNonEmpty returns the first non-whitespace value.
func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "hello", "world"); got != "hello" {
		t.Fatalf("firstNonEmpty: got %q, want %q", got, "hello")
	}
}

// TestFirstNonEmpty_AllEmpty returns empty string.
func TestFirstNonEmpty_AllEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  "); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// TestAuthServiceRequestHeaders gathers headers from the incoming request.
func TestAuthServiceRequestHeaders(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer token123")
	req.Header.Set("X-User-Id", "user-1")
	req.Header.Set("X-User-Name", "Alice")

	headers := authServiceRequestHeaders(req)
	if headers["Authorization"] != "Bearer token123" {
		t.Fatalf("Authorization: got %q", headers["Authorization"])
	}
	if headers["X-User-Id"] != "user-1" {
		t.Fatalf("X-User-Id: got %q", headers["X-User-Id"])
	}
	if headers["X-User-Name"] != "Alice" {
		t.Fatalf("X-User-Name: got %q", headers["X-User-Name"])
	}
}

// TestAuthServiceRequestHeaders_NilRequest returns empty map.
func TestAuthServiceRequestHeaders_NilRequest(t *testing.T) {
	headers := authServiceRequestHeaders(nil)
	if len(headers) != 0 {
		t.Fatalf("expected empty map for nil request, got %v", headers)
	}
}

// TestAuthServiceRequestHeaders_NoAuthHeader omits the key.
func TestAuthServiceRequestHeaders_NoAuthHeader(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("X-User-Id", "user-2")

	headers := authServiceRequestHeaders(req)
	if _, exists := headers["Authorization"]; exists {
		t.Fatal("expected no Authorization header")
	}
	if headers["X-User-Id"] != "user-2" {
		t.Fatalf("X-User-Id: got %q", headers["X-User-Id"])
	}
}
