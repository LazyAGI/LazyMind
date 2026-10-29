package vocabulary

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestAddWordStorageErrorReturnsSafeInternalFailure(t *testing.T) {
	err := fmt.Errorf("save source: %w", &pgconn.PgError{Code: "22001", Message: "private database value exceeds varchar(64)", Detail: "secret SQL details"})
	recorder := httptest.NewRecorder()
	replyAddWordError(recorder, err)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("database failure reported as %d", recorder.Code)
	}
	for _, sensitive := range []string{"varchar", "secret", "private database", "22001"} {
		if strings.Contains(recorder.Body.String(), sensitive) {
			t.Fatalf("database detail leaked: %s", recorder.Body.String())
		}
	}
}
func TestAddWordValidationRemainsBadRequest(t *testing.T) {
	recorder := httptest.NewRecorder()
	replyAddWordError(recorder, errors.New("term is required"))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("validation returned %d", recorder.Code)
	}
}
