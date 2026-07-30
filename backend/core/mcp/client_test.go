package mcp

import (
	"net/http"
	"testing"
)

// TestJoinMCPURL resolves relative endpoints against base URLs.
func TestJoinMCPURL(t *testing.T) {
	// Endpoint is already absolute → returned as-is
	got, err := joinMCPURL("http://base.com", "https://other.com/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "https://other.com/path" {
		t.Fatalf("got %q, want https://other.com/path", got)
	}

	// Relative path joined with base
	got2, err := joinMCPURL("http://base.com/api", "/tools/list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got2 != "http://base.com/tools/list" {
		t.Fatalf("got %q, want http://base.com/tools/list", got2)
	}

	// Relative path without leading slash
	got3, err := joinMCPURL("http://base.com/api/", "tools/list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got3 != "http://base.com/api/tools/list" {
		t.Fatalf("got %q, want http://base.com/api/tools/list", got3)
	}

	// Invalid base URL
	_, err = joinMCPURL("://invalid", "/path")
	if err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

// TestCloneHeaders creates a shallow copy of a headers map.
func TestCloneHeaders(t *testing.T) {
	original := map[string]any{
		"Authorization": "Bearer token",
		"Content-Type":  "application/json",
	}
	cloned := cloneHeaders(original)
	if len(cloned) != 2 {
		t.Fatalf("got %d entries, want 2", len(cloned))
	}
	if cloned["Authorization"] != "Bearer token" {
		t.Fatalf("Authorization = %q", cloned["Authorization"])
	}

	// Nil input creates empty map
	if got := cloneHeaders(nil); len(got) != 0 {
		t.Fatalf("nil got %d entries, want 0", len(got))
	}
}

// TestUnwrapSSEData extracts data from SSE format, returns trimmed for non-SSE.
func TestUnwrapSSEData(t *testing.T) {
	// SSE format with data: prefix
	sseData := []byte("data: {\"json\": true}\n\ndata: [DONE]\n\n")
	got := unwrapSSEData(sseData)
	if string(got) != `{"json": true}` {
		t.Fatalf("got %s", string(got))
	}

	// Non-SSE data returned trimmed
	plain := []byte("  plain text  ")
	if string(unwrapSSEData(plain)) != "plain text" {
		t.Fatal("non-SSE data should be trimmed")
	}

	// Empty
	if len(unwrapSSEData([]byte(""))) != 0 {
		t.Fatal("empty should remain empty")
	}
}

// TestApplyHeaders sets headers from map[string]any values.
func TestApplyHeaders(t *testing.T) {
	h := http.Header{}

	// String value
	applyHeaders(h, map[string]any{"X-Custom": "value1"})
	if h.Get("X-Custom") != "value1" {
		t.Fatalf("X-Custom = %q", h.Get("X-Custom"))
	}

	// Empty key skipped
	applyHeaders(h, map[string]any{"": "ignored"})

	// []string value
	h2 := http.Header{}
	applyHeaders(h2, map[string]any{"X-Multi": []string{"a", "b"}})
	if len(h2["X-Multi"]) != 2 {
		t.Fatalf("multi got %d values, want 2", len(h2["X-Multi"]))
	}

	// []any with string values
	h3 := http.Header{}
	applyHeaders(h3, map[string]any{"X-Any": []any{"x", "y"}})
	if len(h3["X-Any"]) != 2 {
		t.Fatalf("any got %d values, want 2", len(h3["X-Any"]))
	}
}

// TestApplyHeadersSkipsEmptyValues skips empty and whitespace-only values.
func TestApplyHeadersSkipsEmptyValues(t *testing.T) {
	h := http.Header{}
	applyHeaders(h, map[string]any{
		"X-Empty": "",
		"X-Space": "  ",
		"X-Valid": "hello",
	})
	if h.Get("X-Empty") != "" {
		t.Fatal("empty string should be skipped")
	}
	if h.Get("X-Space") != "" {
		t.Fatal("whitespace string should be skipped")
	}
	if h.Get("X-Valid") != "hello" {
		t.Fatalf("X-Valid = %q", h.Get("X-Valid"))
	}
}
