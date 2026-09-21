package common

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTaskOwnerAndRequestUseSameInternalRoleLookup(t *testing.T) {
	for _, test := range []struct {
		body string
		want bool
	}{
		{`{"role":"admin","disabled":false}`, true},
		{`{"data":{"role":"admin","disabled":true}}`, false},
		{`{"data":{"role":"user","disabled":false}}`, false},
	} {
		t.Run(test.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/authservice/user/owner/role/internal" || r.Header.Get("X-LazyMind-Internal-Token") != "test-token" {
					t.Error("incorrect role lookup identity or authentication")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-token")
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-User-Id", "owner")
			if UserIsAdmin(t.Context(), "owner") != test.want || RequestUserIsAdmin(req) != test.want {
				t.Fatal("task and request authorization disagree")
			}
		})
	}
}

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
