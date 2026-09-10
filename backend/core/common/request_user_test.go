package common

import (
	"net/http"
	"testing"
)

// TestUserID verifies extraction of the X-User-Id header, including empty and whitespace cases.
func TestUserID(t *testing.T) {
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

// TestUserName verifies extraction of the X-User-Name header, including empty and whitespace cases.
func TestUserName(t *testing.T) {
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

func TestUserRole(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("X-User-Role", "  system-admin  ")
	if got := UserRole(req); got != "system-admin" {
		t.Fatalf("UserRole() = %q, want system-admin", got)
	}
}

func TestRoleIsAdmin(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{"admin", true},
		{"system-admin", true},
		{"system_admin", true},
		{"tenant.admin", true},
		{"user", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := RoleIsAdmin(tt.role); got != tt.want {
			t.Fatalf("RoleIsAdmin(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}
