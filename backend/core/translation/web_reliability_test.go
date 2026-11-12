package translation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWebRequestsShareCancelableSlot(t *testing.T) {
	spec := providerSpec{Name: "test-shared-web", Keyless: true}
	release, err := acquireWebProvider(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := acquireWebProvider(ctx, spec); !errors.Is(err, context.Canceled) {
		t.Fatalf("concurrent call was admitted: %v", err)
	}
	release()
	release, err = acquireWebProvider(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestWebRateLimitPreservesRetryAfter(t *testing.T) {
	_, err := webTranslationBody(&http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"90"}}, Body: io.NopCloser(strings.NewReader("limited"))})
	if RetryDelay(err) != 90*time.Second {
		t.Fatalf("lost server cooldown: %v", err)
	}
}
