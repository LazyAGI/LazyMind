package common

import "testing"

// TestJoinURL exercises URL joining with various base/path combinations,
// including leading/trailing slashes, whitespace trimming, and edge cases.
func TestJoinURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{"both simple", "http://host", "/api", "http://host/api"},
		{"base trailing slash", "http://host/", "/api", "http://host/api"},
		{"path leading slash", "http://host", "api", "http://host/api"},
		{"both with slashes", "http://host/", "/api/", "http://host/api/"},
		{"empty path", "http://host", "", "http://host"},
		{"empty base", "", "/api", "/api"},
		{"empty base empty path", "", "", "/"},
		{"empty base no slash path", "", "api", "/api"},
		{"base with spaces", " http://host ", "/api", "http://host/api"},
		{"path with spaces", "http://host", " /api ", "http://host/api"},
		{"multi slashes base", "http://host///", "/api", "http://host/api"},
		{"multi slashes path", "http://host", "///api", "http://host/api"},
		{"base with port", "http://host:8080", "/api/v1", "http://host:8080/api/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := JoinURL(tt.base, tt.path)
			if got != tt.want {
				t.Fatalf("JoinURL(%q, %q) = %q, want %q", tt.base, tt.path, got, tt.want)
			}
		})
	}
}
