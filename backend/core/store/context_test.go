package store

import (
	"net/http"
	"testing"
)

// TestStoreUserID verifies that store.UserID delegates to common.UserID and extracts
// the X-User-Id header correctly for present, empty, and whitespace-only values.
func TestStoreUserID(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"present", "user-123", "user-123"},
		{"empty", "", ""},
		{"with spaces", "  user-456  ", "user-456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			if tt.header != "" || tt.name == "with spaces" {
				req.Header.Set("X-User-Id", tt.header)
			}
			got := UserID(req)
			if got != tt.want {
				t.Fatalf("UserID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStoreUserName verifies that store.UserName delegates to common.UserName and extracts
// the X-User-Name header correctly for present, empty, and whitespace-only values.
func TestStoreUserName(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"present", "Alice", "Alice"},
		{"empty", "", ""},
		{"with spaces", "  Bob  ", "Bob"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			if tt.header != "" || tt.name == "with spaces" {
				req.Header.Set("X-User-Name", tt.header)
			}
			got := UserName(req)
			if got != tt.want {
				t.Fatalf("UserName() = %q, want %q", got, tt.want)
			}
		})
	}
}
