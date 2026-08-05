package acl

import (
	"net/http"
	"testing"

	"github.com/gorilla/mux"
)

// TestCurrentUserID extracts the X-User-Id header value.
func TestCurrentUserID(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"present", "user-123", "user-123"},
		{"empty", "", ""},
		{"whitespace", "  user-456  ", "user-456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			if tt.header != "" || tt.name == "whitespace" {
				req.Header.Set("X-User-Id", tt.header)
			}
			got := CurrentUserID(req)
			if got != tt.want {
				t.Fatalf("CurrentUserID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// newRequestWithVars creates a request with gorilla/mux route variables set.
func newRequestWithVars(vars map[string]string) *http.Request {
	req, _ := http.NewRequest("GET", "/", nil)
	return mux.SetURLVars(req, vars)
}

// TestPathKbID extracts kb_id from route variables.
func TestPathKbID(t *testing.T) {
	req := newRequestWithVars(map[string]string{"kb_id": "kb-001"})
	if got := PathKbID(req); got != "kb-001" {
		t.Fatalf("PathKbID() = %q, want %q", got, "kb-001")
	}
}

// TestPathKbID_Empty returns empty string when not present.
func TestPathKbID_Empty(t *testing.T) {
	req := newRequestWithVars(map[string]string{})
	if got := PathKbID(req); got != "" {
		t.Fatalf("PathKbID() = %q, want empty", got)
	}
}

// TestPathACLID extracts acl_id from route variables as int64.
func TestPathACLID(t *testing.T) {
	req := newRequestWithVars(map[string]string{"acl_id": "42"})
	if got := PathACLID(req); got != 42 {
		t.Fatalf("PathACLID() = %d, want 42", got)
	}
}

// TestPathACLID_Empty returns 0 when not present.
func TestPathACLID_Empty(t *testing.T) {
	req := newRequestWithVars(map[string]string{})
	if got := PathACLID(req); got != 0 {
		t.Fatalf("PathACLID() = %d, want 0", got)
	}
}

// TestPathACLID_Invalid returns 0 for non-numeric values.
func TestPathACLID_Invalid(t *testing.T) {
	req := newRequestWithVars(map[string]string{"acl_id": "not-a-number"})
	if got := PathACLID(req); got != 0 {
		t.Fatalf("PathACLID() = %d, want 0", got)
	}
}

// TestPathGroupID extracts group_id from route variables with whitespace trimming.
func TestPathGroupID(t *testing.T) {
	req := newRequestWithVars(map[string]string{"group_id": " group-1 "})
	if got := PathGroupID(req); got != "group-1" {
		t.Fatalf("PathGroupID() = %q, want %q", got, "group-1")
	}
}

// TestPathGroupID_Empty returns empty when not present.
func TestPathGroupID_Empty(t *testing.T) {
	req := newRequestWithVars(map[string]string{})
	if got := PathGroupID(req); got != "" {
		t.Fatalf("PathGroupID() = %q, want empty", got)
	}
}

// TestPathUserID extracts user_id from route variables with whitespace trimming.
func TestPathUserID(t *testing.T) {
	req := newRequestWithVars(map[string]string{"user_id": " user-1 "})
	if got := PathUserID(req); got != "user-1" {
		t.Fatalf("PathUserID() = %q, want %q", got, "user-1")
	}
}

// TestPathUserID_Empty returns empty when not present.
func TestPathUserID_Empty(t *testing.T) {
	req := newRequestWithVars(map[string]string{})
	if got := PathUserID(req); got != "" {
		t.Fatalf("PathUserID() = %q, want empty", got)
	}
}
