package state

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestIsMissing verifies detection of redis.Nil and sql.ErrNoRows as "missing" errors,
// while other errors and nil are not classified as missing.
func TestIsMissing(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"redis nil", redis.Nil, true},
		{"sql no rows", sql.ErrNoRows, true},
		{"other error", errors.New("some error"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsMissing(tt.err)
			if got != tt.want {
				t.Fatalf("IsMissing(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
