package academic

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"lazymind/core/academic/arxivsource"
)

// Keep the real HTTP/XML client while giving mock requests their own fast gate.
// The transport must be an explicit fake; production clients keep the shared gate.
func mockArxivClient(t *testing.T, transport http.RoundTripper) {
	t.Helper()
	previous := newArxivClient
	client := arxivsource.NewClientWithOptions(arxivsource.Options{
		APIBaseURL:  "http://arxiv.test/api/query",
		MinInterval: time.Nanosecond,
		HTTPClient: &http.Client{Transport: sourceTransport(func(r *http.Request) (*http.Response, error) {
			// Existing provider fakes route by the logical official origin.
			request := r.Clone(r.Context())
			request.URL.Scheme = "https"
			request.URL.Host = "export.arxiv.org"
			request.Host = request.URL.Host
			return transport.RoundTrip(request)
		})},
	})
	newArxivClient = func() *arxivsource.Client { return client }
	t.Cleanup(func() { newArxivClient = previous })
}

func TestMockArxivClientKeepsParsingWithoutProductionWaits(t *testing.T) {
	requests := 0
	mockArxivClient(t, sourceTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Host != "export.arxiv.org" || r.URL.Path != "/api/query" || r.URL.Query().Get("id_list") != "1902.00751v1" {
			t.Fatalf("unexpected lookup: %s", r.URL)
		}
		body := houlsbyAtom("1902.00751", "v1")
		if requests == 3 {
			body = "<invalid XML"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		metadata, err := newArxivClient().Lookup(ctx, "1902.00751v1")
		if err != nil || metadata.Title != houlsbyTitle || metadata.Version != "v1" {
			t.Fatalf("mock lookup did not preserve parsed metadata: %+v %v", metadata, err)
		}
	}
	if _, err := newArxivClient().Lookup(ctx, "1902.00751v1"); err == nil {
		t.Fatal("mock client bypassed malformed XML validation")
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3 without production pacing", requests)
	}
}

func TestMockArxivClientRestoresItsPreviousFactory(t *testing.T) {
	previous := newArxivClient
	t.Cleanup(func() { newArxivClient = previous })
	restoredCalls := 0
	newArxivClient = func() *arxivsource.Client {
		restoredCalls++
		return nil
	}
	t.Run("mock scope", func(t *testing.T) {
		mockArxivClient(t, sourceTransport(func(*http.Request) (*http.Response, error) {
			t.Fatal("no HTTP request expected")
			return nil, nil
		}))
		if newArxivClient() == nil {
			t.Fatal("mock client was not installed")
		}
	})
	if newArxivClient() != nil || restoredCalls != 1 {
		t.Fatal("test client factory escaped its cleanup scope")
	}
}
