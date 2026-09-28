package doc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitForDocumentParsedJoinsUntilParsed(t *testing.T) {
	statuses := []string{"parsing", "parsing", "parsed"}
	calls := 0
	err := waitForDocumentParsed(context.Background(), time.Second, time.Millisecond, func() (EnsureDocumentParsedResult, error) {
		status := statuses[calls]
		calls++
		return EnsureDocumentParsedResult{Status: status, TaskID: "shared-task"}, nil
	})
	if err != nil {
		t.Fatalf("waitForDocumentParsed: %v", err)
	}
	if calls != 3 {
		t.Fatalf("ensure calls = %d, want 3", calls)
	}
}

func TestWaitForDocumentParsedReturnsParseFailure(t *testing.T) {
	want := &DocumentServiceError{Code: DocumentServiceUnavailable, Message: "reader crashed"}
	err := waitForDocumentParsed(context.Background(), time.Second, time.Millisecond, func() (EnsureDocumentParsedResult, error) {
		return EnsureDocumentParsedResult{Status: "failed"}, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestWaitForDocumentParsedTimesOut(t *testing.T) {
	err := waitForDocumentParsed(context.Background(), 5*time.Millisecond, time.Millisecond, func() (EnsureDocumentParsedResult, error) {
		return EnsureDocumentParsedResult{Status: "parsing", TaskID: "shared-task"}, nil
	})
	var serviceErr *DocumentServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Code != DocumentServiceUnavailable {
		t.Fatalf("error = %v, want unavailable DocumentServiceError", err)
	}
}

func TestFailedParseStatusIsRetryable(t *testing.T) {
	for _, status := range []string{"FAILED", "ERROR"} {
		if !isRetryableParseObservation(status, "") {
			t.Fatalf("upload status %q should be retryable", status)
		}
	}
	for _, taskStatus := range []string{"FAILED", "ERROR"} {
		if !isRetryableParseObservation("WAITING", taskStatus) {
			t.Fatalf("task status %q should be retryable", taskStatus)
		}
	}
	if isRetryableParseObservation("SUCCESS", "FAILED") {
		t.Fatal("successful document artifact must win over a stale failed Core task")
	}
}
