package mcp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParsePositiveListServersInt parses positive integers with fallback.
func TestParsePositiveListServersInt(t *testing.T) {
	tests := []struct {
		raw      string
		fallback int
		want     int
	}{
		{"10", 1, 10},
		{"0", 1, 1},
		{"-5", 1, 1},
		{"abc", 20, 20},
		{"", 5, 5},
		{"  30  ", 1, 30},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := parsePositiveListServersInt(tt.raw, tt.fallback); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// TestReplyError maps known errors to HTTP status codes.
func TestReplyError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"bad_request", errBadRequest, http.StatusBadRequest},
		{"forbidden", errForbidden, http.StatusForbidden},
		{"not_found", errNotFound, http.StatusNotFound},
		{"internal", errors.New("generic"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			replyError(rec, tt.err, "fallback")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestBulkUpdateEnabledRequiresEnabledField(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/core/mcp_servers:enabled", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	BulkUpdateEnabled(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
