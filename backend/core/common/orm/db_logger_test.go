package orm

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestGORMLoggerSuppressesRecordNotFoundOnly(t *testing.T) {
	var output bytes.Buffer
	logger := newGORMLogger(&output)
	trace := func(err error) {
		logger.Trace(context.Background(), time.Now(), func() (string, int64) {
			return "SELECT * FROM jobs LIMIT 1", 0
		}, err)
	}

	trace(gorm.ErrRecordNotFound)
	if output.Len() != 0 {
		t.Fatalf("record-not-found log was not suppressed: %q", output.String())
	}

	trace(errors.New("database unavailable"))
	if got := output.String(); !strings.Contains(got, "database unavailable") || !strings.Contains(got, "SELECT * FROM jobs") {
		t.Fatalf("real database error was not logged: %q", got)
	}
}
