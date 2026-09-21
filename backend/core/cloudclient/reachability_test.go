package cloudclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type reachabilityRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip reachabilityRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type cloudReachabilityChecker interface {
	CheckReachability(context.Context) error
}

func TestReachabilityCheckUsesOnlyTheConfiguredCredentialFreeHealthEndpoint(t *testing.T) {
	var observed *http.Request
	httpClient := &http.Client{Transport: reachabilityRoundTripper(func(request *http.Request) (*http.Response, error) {
		observed = request.Clone(request.Context())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
			Request:    request,
		}, nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	checker, ok := any(client).(cloudReachabilityChecker)
	if !ok {
		t.Fatal("Cloud client does not expose the reviewed reachability check")
	}
	if err := checker.CheckReachability(context.Background()); err != nil {
		t.Fatalf("check reachability: %v", err)
	}
	if observed == nil || observed.Method != http.MethodGet || observed.URL.String() != "https://cloud.example/healthz" {
		t.Fatalf("unexpected health request: %#v", observed)
	}
	if observed.Header.Get("Authorization") != "" || observed.Header.Get("Cookie") != "" || observed.Header.Get("X-User-Id") != "" {
		t.Fatalf("reachability probe carried credentials: %#v", observed.Header)
	}
}

func TestReachabilityCheckRejectsAnUnhealthyResponse(t *testing.T) {
	httpClient := &http.Client{Transport: reachabilityRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"status":"unavailable"}`)),
			Request:    request,
		}, nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	checker, ok := any(client).(cloudReachabilityChecker)
	if !ok {
		t.Fatal("Cloud client does not expose the reviewed reachability check")
	}
	if err := checker.CheckReachability(context.Background()); err == nil {
		t.Fatal("unhealthy Cloud endpoint was reported as reachable")
	}
}
