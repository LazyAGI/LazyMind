package resourceupdate

import (
	"errors"
	"testing"
)

// TestReviewSkipReason maps known errors to reason strings.
func TestReviewSkipReason(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errReviewConflict, "review_conflict"},
		{errReviewNotFound, "review_not_found"},
		{errReviewInvalid, "review_invalid"},
		{errors.New("generic error"), "review_skipped"},
		{nil, "review_skipped"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := reviewSkipReason(tt.err); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
