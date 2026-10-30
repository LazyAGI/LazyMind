package academic

import (
	"net/http"
	"testing"
	"time"

	"lazymind/core/academic/arxivsource"
)

const testArxivHost = "arxiv.test"

// Install this after the test's mock transport. Provider parsing and HTTP error
// handling remain real; only the unrelated production pacing is excluded.
func installTestArxivClient(t *testing.T) {
	t.Helper()
	client := arxivsource.NewClientWithOptions(arxivsource.Options{
		HTTPClient:  &http.Client{Transport: http.DefaultTransport},
		APIBaseURL:  "https://" + testArxivHost + "/api/query",
		MinInterval: time.Nanosecond,
	})
	previous := newArxivClient
	newArxivClient = func() *arxivsource.Client { return client }
	t.Cleanup(func() { newArxivClient = previous })
}
