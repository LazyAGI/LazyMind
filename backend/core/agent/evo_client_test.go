package agent

import (
	"net/http"
	"testing"

	"lazymind/core/common"
)

// TestRawProxyResponse builds upstreamProxyResponse with JSON body detection.
func TestRawProxyResponse(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	resp := rawProxyResponse(200, []byte(`{"ok":true}`), header)
	if resp.StatusCode != 200 {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}
	if resp.Body == nil {
		t.Fatal("expected Body to be parsed for JSON response")
	}
	if resp.ContentType != "application/json" {
		t.Fatalf("ContentType: got %q, want application/json", resp.ContentType)
	}

	// Non-JSON content type with JSON-like body is still parsed.
	resp2 := rawProxyResponse(200, []byte(`{"a":1}`), http.Header{})
	if resp2.Body == nil {
		t.Fatal("JSON-like body should be parsed even without Content-Type")
	}

	// Plain text body is left as raw bytes only.
	resp3 := rawProxyResponse(200, []byte(`hello`), http.Header{})
	if resp3.Body != nil {
		t.Fatalf("plain text body: got %v, want nil", resp3.Body)
	}
}

// TestProxyStatusCode returns OK for nil or zero-status proxy.
func TestProxyStatusCode(t *testing.T) {
	if got := proxyStatusCode(nil); got != http.StatusOK {
		t.Fatalf("nil: got %d, want %d", got, http.StatusOK)
	}
	if got := proxyStatusCode(&upstreamProxyResponse{StatusCode: 0}); got != http.StatusOK {
		t.Fatalf("zero: got %d, want %d", got, http.StatusOK)
	}
	if got := proxyStatusCode(&upstreamProxyResponse{StatusCode: 201}); got != 201 {
		t.Fatalf("got %d, want 201", got)
	}
}

// TestEvoProxyStatusCode extracts HTTP status from error.
func TestEvoProxyStatusCode(t *testing.T) {
	if got := evoProxyStatusCode(nil); got != http.StatusOK {
		t.Fatalf("nil error: got %d, want %d", got, http.StatusOK)
	}
	// Non-HTTPError defaults to 502.
	httpErr := &common.HTTPError{StatusCode: http.StatusBadGateway, Message: "boom"}
	if got := evoProxyStatusCode(httpErr); got != http.StatusBadGateway {
		t.Fatalf("got %d, want %d", got, http.StatusBadGateway)
	}
}

// TestEvoCreateModelAppError detects model-config app errors from 422 responses.
func TestEvoCreateModelAppError(t *testing.T) {
	// Non-422 error returns false.
	httpErr := &common.HTTPError{StatusCode: http.StatusBadRequest, Message: "bad"}
	_, ok := evoCreateModelAppError(httpErr)
	if ok {
		t.Fatal("non-422 error should not be detected")
	}
	// Nil error returns false.
	_, ok2 := evoCreateModelAppError(nil)
	if ok2 {
		t.Fatal("nil error should not be detected")
	}
}
