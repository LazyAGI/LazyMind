package sourceurl

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubAPIGetAcceptsTreeResponsesLargerThanOneMiB(t *testing.T) {
	payload := strings.Repeat("a", 2<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tree":"`+payload+`"}`)
	}))
	defer server.Close()
	var result struct{ Tree string }
	status, err := GitHubAPIGet(context.Background(), server.Client(), server.URL, &result)
	if err != nil || status != http.StatusOK || result.Tree != payload {
		t.Fatalf("tree response length=%d, status=%d, err=%v", len(result.Tree), status, err)
	}
}

func TestGitHubAPIGetRejectsOversizedAndTrailingJSON(t *testing.T) {
	for _, body := range []string{`{"tree":"` + strings.Repeat("a", 8<<20) + `"}`, `{"sha":"one"} {"sha":"two"}`} {
		t.Run(strconv.Itoa(len(body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			var result any
			if _, err := GitHubAPIGet(context.Background(), server.Client(), server.URL, &result); err == nil {
				t.Fatal("invalid or oversized response was accepted")
			}
		})
	}
}

func TestGitHubAPIGetCoalescesAndCachesSuccessfulRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"sha":"commit-one"}`)
	}))
	defer server.Close()
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var result struct{ SHA string }
			status, err := GitHubAPIGet(context.Background(), server.Client(), server.URL, &result)
			if err != nil || status != http.StatusOK || result.SHA != "commit-one" {
				t.Errorf("status=%d result=%+v err=%v", status, result, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("successful metadata requested %d times, want once", calls.Load())
	}
}

func TestGitHubAPIGetDoesNotCacheFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"sha":"fixed"}`)
	}))
	defer server.Close()
	var result struct{ SHA string }
	_, _ = GitHubAPIGet(context.Background(), server.Client(), server.URL, &result)
	status, err := GitHubAPIGet(context.Background(), server.Client(), server.URL, &result)
	if err != nil || status != http.StatusOK || result.SHA != "fixed" {
		t.Fatalf("failed response was reused: status=%d result=%+v err=%v", status, result, err)
	}
}

func TestGitHubAPIGetEvictsOldMetadata(t *testing.T) {
	var firstCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/0" {
			firstCalls.Add(1)
		}
		_, _ = io.WriteString(w, `{"sha":"ok"}`)
	}))
	defer server.Close()
	for i := range 70 {
		var result any
		if _, err := GitHubAPIGet(context.Background(), server.Client(), server.URL+"/"+strconv.Itoa(i), &result); err != nil {
			t.Fatal(err)
		}
	}
	var result any
	_, _ = GitHubAPIGet(context.Background(), server.Client(), server.URL+"/0", &result)
	if firstCalls.Load() != 2 {
		t.Fatalf("old metadata was not evicted: first endpoint requested %d times", firstCalls.Load())
	}
}

type githubTestRoundTripper func(*http.Request) (*http.Response, error)

func (f githubTestRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubAPIGetScopesTokensAndStripsRedirectCredentials(t *testing.T) {
	t.Setenv("LAZYMIND_GITHUB_TOKEN", "preferred-test-token")
	t.Setenv("GITHUB_TOKEN", "fallback-test-token")
	for _, host := range []string{"api.github.com", "sub.api.github.com", "example.test"} {
		t.Run(host, func(t *testing.T) {
			var requests []*http.Request
			client := &http.Client{Transport: githubTestRoundTripper(func(r *http.Request) (*http.Response, error) {
				requests = append(requests, r.Clone(context.Background()))
				if r.URL.Path == "/redirect" {
					return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://sub.api.github.com/final"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
			})}
			var result any
			if _, err := GitHubAPIGet(context.Background(), client, "https://"+host+"/redirect?test="+url.QueryEscape(t.TempDir()), &result); err != nil {
				t.Fatal(err)
			}
			want := ""
			if host == "api.github.com" {
				want = "Bearer preferred-test-token"
			}
			if requests[0].Header.Get("Authorization") != want {
				t.Fatalf("initial Authorization=%q, want %q", requests[0].Header.Get("Authorization"), want)
			}
			if len(requests) != 2 || requests[1].Header.Get("Authorization") != "" {
				t.Fatal("credentials followed a redirect outside api.github.com")
			}
		})
	}
}

func TestGitHubAPIGetRetriesTransientFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"sha":"recovered"}`)
	}))
	defer server.Close()
	var result struct{ SHA string }
	status, err := GitHubAPIGet(context.Background(), server.Client(), server.URL, &result)
	if err != nil || status != http.StatusOK || result.SHA != "recovered" || calls.Load() != 3 {
		t.Fatalf("status=%d result=%+v calls=%d err=%v", status, result, calls.Load(), err)
	}
}

func TestGitHubAPIGetReportsLongRateLimitWithoutRetrying(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	_, err := GitHubAPIGet(context.Background(), server.Client(), server.URL, nil)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "rate limit") || !strings.Contains(err.Error(), "120") || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestGitHubAPIGetRetryWaitHonorsCancellation(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := GitHubAPIGet(ctx, server.Client(), server.URL, nil); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry wait ignored cancellation")
	}
	if calls.Load() != 1 {
		t.Fatalf("retried after cancellation: %d calls", calls.Load())
	}
}

func TestGitHubAPIGetUsesFallbackTokenOnlyOverHTTPS(t *testing.T) {
	t.Setenv("LAZYMIND_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "fallback-test-token")
	for _, scheme := range []string{"https", "http"} {
		t.Run(scheme, func(t *testing.T) {
			var authorization string
			client := &http.Client{Transport: githubTestRoundTripper(func(r *http.Request) (*http.Response, error) {
				authorization = r.Header.Get("Authorization")
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
			})}
			_, err := GitHubAPIGet(context.Background(), client, scheme+"://api.github.com/fallback?test="+url.QueryEscape(t.TempDir()), nil)
			want := ""
			if scheme == "https" {
				want = "Bearer fallback-test-token"
			}
			if err != nil || authorization != want {
				t.Fatalf("Authorization=%q want=%q err=%v", authorization, want, err)
			}
		})
	}
}

func TestGitHubAPIGetBoundsTransientRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	status, err := GitHubAPIGet(context.Background(), server.Client(), server.URL, nil)
	if err != nil || status != http.StatusBadGateway || calls.Load() != 3 {
		t.Fatalf("status=%d calls=%d err=%v", status, calls.Load(), err)
	}
}

func TestGitHubAPIGetCanceledWaiterDoesNotCancelOtherWaiters(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		_, _ = io.WriteString(w, `{"sha":"kept"}`)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	canceled := make(chan error, 1)
	go func() { _, err := GitHubAPIGet(ctx, server.Client(), server.URL, nil); canceled <- err }()
	<-started
	completed := make(chan error, 1)
	go func() {
		var result struct{ SHA string }
		_, err := GitHubAPIGet(context.Background(), server.Client(), server.URL, &result)
		if err == nil && result.SHA != "kept" {
			err = errors.New("other waiter lost its result")
		}
		completed <- err
	}()
	// Observe the public operation's shared flight before canceling the first
	// caller, so cancellation cannot race the second caller's registration.
	deadline := time.Now().Add(time.Second)
	for {
		githubAPIMetadata.Lock()
		waiters := 0
		for key, flight := range githubAPIMetadata.flights {
			if key.endpoint == server.URL {
				waiters = flight.waiters
			}
		}
		githubAPIMetadata.Unlock()
		if waiters == 2 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("second caller did not join shared request")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		close(release)
		t.Fatalf("canceled waiter error=%v", err)
	}
	close(release)
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("coalesced request count=%d", calls.Load())
	}
}
