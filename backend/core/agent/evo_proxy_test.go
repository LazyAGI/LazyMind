package agent

import (
	"net/url"
	"testing"
)

// TestSkipProxyResponseHeader filters hop-by-hop headers.
func TestSkipProxyResponseHeader(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"Connection", true},
		{"keep-alive", true},
		{"Transfer-Encoding", true},
		{"Content-Length", true},
		{"upgrade", true},
		{"Content-Type", false},
		{"Authorization", false},
		{"X-Request-Id", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := skipProxyResponseHeader(tt.key); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCloneURLValues deep-copies url.Values.
func TestCloneURLValues(t *testing.T) {
	// Empty input returns nil.
	if got := cloneURLValues(nil); got != nil {
		t.Fatalf("nil: got %v, want nil", got)
	}
	if got := cloneURLValues(url.Values{}); got != nil {
		t.Fatalf("empty: got %v, want nil", got)
	}
	// Cloned values are deep copies.
	orig := url.Values{"a": {"1", "2"}, "b": {"3"}}
	cloned := cloneURLValues(orig)
	orig["a"][0] = "changed"
	if cloned.Get("a") != "1" {
		t.Fatalf("clone mutated: got %q", cloned.Get("a"))
	}
}

// TestThreadProxyPath builds the thread proxy path.
func TestThreadProxyPath(t *testing.T) {
	got := threadProxyPath("thread-1", "/steps")
	if got != "/threads/thread-1/steps" {
		t.Fatalf("got %q, want /threads/thread-1/steps", got)
	}
	// With special chars in threadID.
	got2 := threadProxyPath("thread/with spaces", "")
	if got2 != "/threads/thread%2Fwith%20spaces" {
		t.Fatalf("got %q", got2)
	}
}
