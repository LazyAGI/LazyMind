package chat

import (
	"net/http"
	"testing"
)

// TestWriterSyncStatus_BadRequest maps 400 and 422 to 400.
func TestWriterSyncStatus_BadRequest(t *testing.T) {
	if got := writerSyncStatus(http.StatusBadRequest); got != http.StatusBadRequest {
		t.Fatalf("status 400: got %d, want %d", got, http.StatusBadRequest)
	}
	if got := writerSyncStatus(http.StatusUnprocessableEntity); got != http.StatusBadRequest {
		t.Fatalf("status 422: got %d, want %d", got, http.StatusBadRequest)
	}
}

// TestWriterSyncStatus_AuthErrors passes through 401, 403, 409.
func TestWriterSyncStatus_AuthErrors(t *testing.T) {
	if got := writerSyncStatus(http.StatusUnauthorized); got != http.StatusUnauthorized {
		t.Fatalf("got %d, want %d", got, http.StatusUnauthorized)
	}
	if got := writerSyncStatus(http.StatusForbidden); got != http.StatusForbidden {
		t.Fatalf("got %d, want %d", got, http.StatusForbidden)
	}
	if got := writerSyncStatus(http.StatusConflict); got != http.StatusConflict {
		t.Fatalf("got %d, want %d", got, http.StatusConflict)
	}
}

// TestWriterSyncStatus_Default maps unrecognized statuses to 502.
func TestWriterSyncStatus_Default(t *testing.T) {
	if got := writerSyncStatus(http.StatusOK); got != http.StatusBadGateway {
		t.Fatalf("status 200: got %d, want %d", got, http.StatusBadGateway)
	}
	if got := writerSyncStatus(http.StatusInternalServerError); got != http.StatusBadGateway {
		t.Fatalf("status 500: got %d, want %d", got, http.StatusBadGateway)
	}
	if got := writerSyncStatus(http.StatusNotFound); got != http.StatusBadGateway {
		t.Fatalf("status 404: got %d, want %d", got, http.StatusBadGateway)
	}
	if got := writerSyncStatus(999); got != http.StatusBadGateway {
		t.Fatalf("unknown: got %d, want %d", got, http.StatusBadGateway)
	}
}
