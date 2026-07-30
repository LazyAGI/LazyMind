package evalset

import (
	"errors"
	"testing"

	"lazymind/core/asyncjob"
)

// TestImportErrorCodeForError maps known error patterns to error codes.
func TestImportErrorCodeForError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, asyncjob.ErrorCodeHandlerFailed},
		{"eval_set_not_found", errors.New("eval_set_not_found: record not found"), importErrorEvalSetNotFound},
		{"insert_failed", errors.New("insert_failed: constraint violation"), importErrorInsertFailed},
		{"temp_file", errors.New("temp file missing"), importErrorTempFileMissing},
		{"no_such_file", errors.New("no such file"), importErrorTempFileMissing},
		{"invalid_payload", errors.New("payload error"), importErrorInvalidPayload},
		{"mode_keyword", errors.New("mode mismatch"), importErrorInvalidPayload},
		{"valid_rows", errors.New("valid_rows mismatch"), importErrorInvalidPayload},
		{"default", errors.New("unknown error"), asyncjob.ErrorCodeHandlerFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := importErrorCodeForError(tt.err); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
