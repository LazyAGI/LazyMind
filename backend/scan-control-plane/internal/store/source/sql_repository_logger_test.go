package source

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
			return "SELECT * FROM parse_tasks LIMIT 1", 0
		}, err)
	}

	trace(gorm.ErrRecordNotFound)
	if output.Len() != 0 {
		t.Fatalf("record-not-found log was not suppressed: %q", output.String())
	}

	trace(errors.New("database unavailable"))
	if got := output.String(); !strings.Contains(got, "database unavailable") || !strings.Contains(got, "SELECT * FROM parse_tasks") {
		t.Fatalf("real database error was not logged: %q", got)
	}
}

func TestGORMLoggerDoesNotExpandQueryParameters(t *testing.T) {
	logger := newGORMLogger(&bytes.Buffer{})
	filter, ok := logger.(interface {
		ParamsFilter(context.Context, string, ...interface{}) (string, []interface{})
	})
	if !ok {
		t.Fatal("GORM logger does not expose parameter filtering")
	}

	query, params := filter.ParamsFilter(context.Background(), "UPDATE parse_tasks SET last_error = ?", "large error payload")
	if query != "UPDATE parse_tasks SET last_error = ?" {
		t.Fatalf("query template changed unexpectedly: %q", query)
	}
	if len(params) != 0 {
		t.Fatalf("query parameters should be omitted from logs, got %v", params)
	}
}
