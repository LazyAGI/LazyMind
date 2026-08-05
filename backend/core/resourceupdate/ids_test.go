package resourceupdate

import (
	"strings"
	"testing"
)

// TestNewSkillReviewRequestID generates an ID with the review prefix.
func TestNewSkillReviewRequestID(t *testing.T) {
	id := newSkillReviewRequestID()
	if !strings.HasPrefix(id, skillReviewRequestIDPrefix) {
		t.Fatalf("got %q, want prefix %q", id, skillReviewRequestIDPrefix)
	}
	if id == skillReviewRequestIDPrefix {
		t.Fatal("expected non-empty suffix after prefix")
	}
	// Consecutive calls produce different IDs.
	id2 := newSkillReviewRequestID()
	if id == id2 {
		t.Fatal("expected different IDs")
	}
}

// TestNormalizeSkillReviewRequestID removes whitespace and ensures prefix.
func TestNormalizeSkillReviewRequestID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  abc123  ", "review_abc123"},
		{"review_abc123", "review_abc123"},
		{"review_", "review_"},
		{"", ""},
		{"  ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeSkillReviewRequestID(tt.input); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
