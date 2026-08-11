package common

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHTTPPost_Success verifies a successful POST with a JSON response body.
func TestHTTPPost_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, status, err := HTTPPost(context.Background(), srv.URL, "application/json", []byte(`{"q":"test"}`))
	if err != nil {
		t.Fatalf("HTTPPost: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status: got %d, want %d", status, http.StatusOK)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body: got %q", string(body))
	}
}

// TestHTTPPost_ErrorStatusCode returns the response body and status even on errors.
func TestHTTPPost_ErrorStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer srv.Close()

	body, status, err := HTTPPost(context.Background(), srv.URL, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", status, http.StatusBadRequest)
	}
	if string(body) != `{"error":"bad"}` {
		t.Fatalf("body: got %q", string(body))
	}
}

// TestHTTPPost_TODOContext verifies HTTPPost works with context.TODO as a placeholder context.
func TestHTTPPost_TODOContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	_, status, err := HTTPPost(context.TODO(), srv.URL, "", nil)
	if err != nil {
		t.Fatalf("HTTPPost TODO ctx: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status: got %d", status)
	}
}

// TestHTTPPost_InvalidURL returns an error for a malformed URL.
func TestHTTPPost_InvalidURL(t *testing.T) {
	_, _, err := HTTPPost(context.Background(), "://invalid", "", nil)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

// TestHTTPPost_EmptyContentType omits the Content-Type header.
func TestHTTPPost_EmptyContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "" {
			t.Fatalf("unexpected Content-Type: %q", r.Header.Get("Content-Type"))
		}
		_, _ = w.Write([]byte(`done`))
	}))
	defer srv.Close()

	_, _, err := HTTPPost(context.Background(), srv.URL, "", nil)
	if err != nil {
		t.Fatalf("HTTPPost: %v", err)
	}
}
