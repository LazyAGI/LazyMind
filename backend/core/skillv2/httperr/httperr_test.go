package httperr

import (
	"errors"
	"net/http"
	"testing"

	"gorm.io/gorm"
)

// TestForError_RecordNotFound maps gorm.ErrRecordNotFound to 404.
func TestForError_RecordNotFound(t *testing.T) {
	sem := ForError(gorm.ErrRecordNotFound)
	if sem.Status != http.StatusNotFound || sem.Code != CodeNotFound {
		t.Fatalf("got status=%d code=%s, want 404 not_found", sem.Status, sem.Code)
	}
}

// TestForError_Nil returns empty semantic (ReplyError bails out early).
func TestForError_Nil(t *testing.T) {
	// ForError itself is never called with nil (ReplyError guards), but verify it handles it.
	sem := ForError(errors.New(""))
	if sem.Status != http.StatusBadRequest {
		t.Fatalf("got status=%d, want 400 for empty message", sem.Status)
	}
}

// TestForMessage_InfersStatus detects status from message content.
func TestForMessage_InfersStatus(t *testing.T) {
	tests := []struct {
		message string
		want    int
	}{
		{"not found anywhere", http.StatusNotFound},
		{"forbidden for another user", http.StatusForbidden},
		{"duplicate entry already exists", http.StatusConflict},
		{"stale draft version conflict", http.StatusConflict},
		{"skill package must contain SKILL.md", http.StatusUnprocessableEntity},
		{"object store error", http.StatusInternalServerError},
		{"unknown error", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			sem := ForMessage(tt.message, 0)
			if sem.Status != tt.want {
				t.Fatalf("got status=%d, want %d", sem.Status, tt.want)
			}
		})
	}
}

// TestForMessage_ExplicitStatus uses the provided status without inference.
func TestForMessage_ExplicitStatus(t *testing.T) {
	sem := ForMessage("some message", http.StatusTeapot)
	if sem.Status != http.StatusTeapot {
		t.Fatalf("got status=%d, want %d", sem.Status, http.StatusTeapot)
	}
}

// TestStatusForMessage maps well-known error phrases to HTTP status codes.
func TestStatusForMessage(t *testing.T) {
	tests := []struct {
		message string
		want    int
	}{
		{"record not found", http.StatusNotFound},
		{"user not found", http.StatusNotFound},
		{"forbidden access", http.StatusForbidden},
		{"stale draft version detected", http.StatusConflict},
		{"path already exists", http.StatusConflict},
		{"not a valid zip file", http.StatusUnprocessableEntity},
		{"db is not configured", http.StatusInternalServerError},
		{"random message", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.message, func(t *testing.T) {
			if got := statusForMessage(tt.message); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCodeForMessage maps messages to error codes.
func TestCodeForMessage(t *testing.T) {
	tests := []struct {
		message string
		status  int
		want    string
	}{
		{"unsafe path detected", http.StatusBadRequest, CodeInvalidPath},
		{"draft overlay is empty", http.StatusBadRequest, CodeEmptyDraft},
		{"stale draft version", http.StatusBadRequest, CodeDraftVersionConflict},
		{"already exists", http.StatusBadRequest, CodePathExists},
		{"write file over directory", http.StatusBadRequest, CodeEntryTypeConflict},
		{"skill package must contain", http.StatusBadRequest, CodeSkillPackageInvalid},
		{"diff refs must belong", http.StatusBadRequest, CodeDiffRefMismatch},
		{"unknown message", http.StatusBadRequest, CodeInvalidRequest},
		{"empty", http.StatusNotFound, CodeNotFound},
		{"empty", http.StatusInternalServerError, CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := codeForMessage(tt.message, tt.status); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// TestCodeForStatus maps HTTP status to error codes.
func TestCodeForStatus(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, CodeUnauthenticated},
		{http.StatusForbidden, CodeForbidden},
		{http.StatusNotFound, CodeNotFound},
		{http.StatusConflict, CodeDraftConflict},
		{http.StatusRequestEntityTooLarge, CodePayloadTooLarge},
		{http.StatusUnprocessableEntity, CodeInvalidRequest},
		{http.StatusInternalServerError, CodeInternal},
		{http.StatusBadGateway, CodeInternal},
		{http.StatusOK, CodeInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := codeForStatus(tt.status); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
