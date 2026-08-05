package common

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestServer starts an httptest.Server that echoes back a fixed JSON response.
func newTestServer(t *testing.T, statusCode int, body any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
}

// TestApiPost_Success verifies a successful POST with JSON response.
func TestApiPost_Success(t *testing.T) {
	srv := newTestServer(t, 200, map[string]any{"result": "ok"})
	defer srv.Close()

	var resp map[string]any
	err := ApiPost(context.Background(), srv.URL, map[string]string{"req": "data"}, map[string]string{"X-Custom": "v"}, &resp, time.Second)
	if err != nil {
		t.Fatalf("ApiPost: %v", err)
	}
	if resp["result"] != "ok" {
		t.Fatalf("result: got %v, want ok", resp["result"])
	}
}

// TestApiGet_Success verifies a successful GET.
func TestApiGet_Success(t *testing.T) {
	srv := newTestServer(t, 200, map[string]any{"found": true})
	defer srv.Close()

	var resp map[string]any
	err := ApiGet(context.Background(), srv.URL, nil, &resp, time.Second)
	if err != nil {
		t.Fatalf("ApiGet: %v", err)
	}
	if resp["found"] != true {
		t.Fatalf("found: got %v, want true", resp["found"])
	}
}

// TestApiDelete_Success verifies a successful DELETE.
func TestApiDelete_Success(t *testing.T) {
	srv := newTestServer(t, 200, nil)
	defer srv.Close()

	err := ApiDelete(context.Background(), srv.URL, nil, nil, time.Second)
	if err != nil {
		t.Fatalf("ApiDelete: %v", err)
	}
}

// TestApiGet_ErrorStatusCode returns an HTTPError for non-2xx responses.
func TestApiGet_ErrorStatusCode(t *testing.T) {
	srv := newTestServer(t, 500, map[string]any{"message": "boom"})
	defer srv.Close()

	var resp map[string]any
	err := ApiGet(context.Background(), srv.URL, nil, &resp, time.Second)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	httpErr, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("expected *HTTPError, got %T", err)
	}
	if httpErr.StatusCode != 500 {
		t.Fatalf("StatusCode: got %d, want 500", httpErr.StatusCode)
	}
}

// TestApiPost_Timeout verifies that a slow server triggers a timeout.
func TestApiPost_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	err := ApiPost(context.Background(), srv.URL, nil, nil, nil, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// TestSummarizeExternalErrorMessage_JSON extracts the message field from JSON.
func TestSummarizeExternalErrorMessage_JSON(t *testing.T) {
	body := []byte(`{"message": "something went wrong"}`)
	got := summarizeExternalErrorMessage(body)
	if got != "something went wrong" {
		t.Fatalf("summarizeExternalErrorMessage: got %q, want %q", got, "something went wrong")
	}
}

// TestSummarizeExternalErrorMessage_EmptyBody returns a placeholder.
func TestSummarizeExternalErrorMessage_EmptyBody(t *testing.T) {
	got := summarizeExternalErrorMessage([]byte(""))
	if got == "" {
		t.Fatal("expected non-empty placeholder for empty body")
	}
}

// TestSummarizeExternalErrorMessage_NonJSON returns trimmed plain text.
func TestSummarizeExternalErrorMessage_NonJSON(t *testing.T) {
	body := []byte("  raw error message  ")
	got := summarizeExternalErrorMessage(body)
	if got != "raw error message" {
		t.Fatalf("summarizeExternalErrorMessage: got %q, want %q", got, "raw error message")
	}
}

// TestSummarizeExternalErrorMessage_Truncation trims long messages.
func TestSummarizeExternalErrorMessage_Truncation(t *testing.T) {
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	got := summarizeExternalErrorMessage(long)
	if len(got) > 250 {
		t.Fatalf("expected truncated message, got len=%d", len(got))
	}
}

// TestExtractExternalErrorMessage_PreferredKeys searches preferred keys first.
func TestExtractExternalErrorMessage_PreferredKeys(t *testing.T) {
	data := map[string]any{"msg": "msg value", "message": "preferred value"}
	got := extractExternalErrorMessage(data)
	if got != "preferred value" {
		t.Fatalf("extractExternalErrorMessage: got %q, want %q", got, "preferred value")
	}
}

// TestExtractExternalErrorMessage_Nested extracts from nested objects.
func TestExtractExternalErrorMessage_Nested(t *testing.T) {
	data := map[string]any{"data": map[string]any{"error": "nested error"}}
	got := extractExternalErrorMessage(data)
	if got != "nested error" {
		t.Fatalf("extractExternalErrorMessage nested: got %q, want %q", got, "nested error")
	}
}

// TestExtractExternalErrorMessage_Array extracts from array elements.
func TestExtractExternalErrorMessage_Array(t *testing.T) {
	data := []any{map[string]any{"reason": "array reason"}}
	got := extractExternalErrorMessage(data)
	if got != "array reason" {
		t.Fatalf("extractExternalErrorMessage array: got %q, want %q", got, "array reason")
	}
}

// TestExtractExternalErrorMessage_NonContainers returns empty for unsupported types.
func TestExtractExternalErrorMessage_NonContainers(t *testing.T) {
	got := extractExternalErrorMessage(42)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

// TestHTTPError_ErrorNil returns empty string for nil receiver.
func TestHTTPError_ErrorNil(t *testing.T) {
	var e *HTTPError
	if e.Error() != "" {
		t.Fatalf("expected empty string for nil HTTPError, got %q", e.Error())
	}
}
