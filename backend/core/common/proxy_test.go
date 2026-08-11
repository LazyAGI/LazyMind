package common

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestProxy_ForwardsRequest verifies that Proxy forwards to the target and relays the response.
func TestProxy_ForwardsRequest(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Target", "reached")
		_, _ = w.Write([]byte(`target response`))
	}))
	defer target.Close()

	// Proxy with flushInterval=0 (buffered JSON mode, not streaming).
	handler := Proxy(target.URL, 0)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/proxy/me", nil)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("X-Target") != "reached" {
		t.Fatalf("X-Target header not forwarded")
	}
	if w.Body.String() != "target response" {
		t.Fatalf("body: got %q", string(w.Body.String()))
	}
}

// TestProxy_EmptyBodyReadFails responds 400 for an unreadable body (nil body is fine).
func TestProxy_ForwardsMethod(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Method))
	}))
	defer target.Close()

	handler := Proxy(target.URL, 0)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/proxy/me", nil)
	handler.ServeHTTP(w, req)

	if w.Body.String() != "DELETE" {
		t.Fatalf("method: got %q, want DELETE", w.Body.String())
	}
}

// TestProxyWithACL_NilExtractor behaves like Proxy and forwards without ACL checks.
func TestProxyWithACL_NilExtractor(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`passed`))
	}))
	defer target.Close()

	handler := ProxyWithACL(target.URL, 0, nil)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "passed" {
		t.Fatalf("body: got %q", w.Body.String())
	}
}

// TestProxy_FluhingNegativeOne uses streaming mode (flushInterval=-1).
func TestProxy_FlushingNegativeOne(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected Flusher")
		}
		_, _ = w.Write([]byte("data: hello\n\n"))
		flusher.Flush()
	}))
	defer target.Close()

	handler := Proxy(target.URL, -1)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/stream", nil)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
}

// TestProxyWithACLDynamicFlush_NilExtractor forwards without ACL.
func TestProxyWithACLDynamicFlush_NilExtractor(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`dynamic`))
	}))
	defer target.Close()

	flushFn := func(req *http.Request, body []byte) time.Duration {
		return 100 * time.Millisecond
	}
	handler := ProxyWithACLDynamicFlush(target.URL, nil, flushFn)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/dyn", nil)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "dynamic" {
		t.Fatalf("body: got %q", w.Body.String())
	}
}

// TestProxyWithACLDynamicFlush_NilFlushInterval uses zero flush.
func TestProxyWithACLDynamicFlush_NilFlushInterval(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`noflush`))
	}))
	defer target.Close()

	handler := ProxyWithACLDynamicFlush(target.URL, nil, nil)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/noflush", nil)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", w.Code, http.StatusOK)
	}
}
