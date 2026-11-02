package arxivsource

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const validEntry = `<entry><id>http://arxiv.org/abs/2404.06654v3</id><title>RULER: What's the Real Context Size
 of Your Long-Context Language Models?</title><published>2024-04-09T18:00:00Z</published><updated>2024-08-29T18:00:00Z</updated><summary>We study context
 lengths.</summary><author><name>Cheng-Ping Hsieh</name></author><author><name>Simeng Sun</name></author><arxiv:doi xmlns:arxiv="http://arxiv.org/schemas/atom">10.1234/ruler</arxiv:doi><link title="pdf" href="https://evil.test/unsafe.pdf"/></entry>`

func feed(entries string) string {
	return `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">` + entries + `</feed>`
}

func requireCode(t *testing.T, err error, code string) *LookupError {
	t.Helper()
	var lookupErr *LookupError
	if !errors.As(err, &lookupErr) || lookupErr.Code != code {
		t.Fatalf("error = %v; want LookupError code %s", err, code)
	}
	return lookupErr
}

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClientWithOptions(Options{HTTPClient: srv.Client(), APIBaseURL: srv.URL + "/api/query", MinInterval: time.Nanosecond, Timeout: time.Second})
}

func TestLookupLocksActualVersionAndBuildsOfficialURLs(t *testing.T) {
	for _, input := range []string{"2404.06654", "arXiv:2404.06654v3", "https://arxiv.org/pdf/2404.06654v3.pdf?x=1", "https://doi.org/10.48550/arXiv.2404.06654v3"} {
		t.Run(input, func(t *testing.T) {
			var requestID string
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				requestID = r.URL.Query().Get("id_list")
				if r.URL.Path != "/api/query" || r.URL.Query().Get("search_query") != "" {
					t.Errorf("unexpected query %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/atom+xml")
				fmt.Fprint(w, feed(validEntry))
			})
			got, err := client.Lookup(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			wantID := "2404.06654v3"
			if input == "2404.06654" {
				wantID = "2404.06654"
			}
			if requestID != wantID {
				t.Fatalf("request id_list=%q; want %q", requestID, wantID)
			}
			if got.Title != "RULER: What's the Real Context Size of Your Long-Context Language Models?" || got.Year != 2024 || got.Abstract != "We study context lengths." {
				t.Fatalf("unexpected metadata %#v", got)
			}
			if len(got.Authors) != 2 || got.Authors[0] != "Cheng-Ping Hsieh" || got.DOI != "10.1234/ruler" || got.Venue != "arXiv" {
				t.Fatalf("unexpected authors/DOI/venue %#v", got)
			}
			if got.BaseID != "2404.06654" || got.Version != "v3" || got.AbsURL != "https://arxiv.org/abs/2404.06654v3" || got.PDFURL != "https://arxiv.org/pdf/2404.06654v3" {
				t.Fatalf("unsafe or unversioned result %#v", got)
			}
		})
	}
}

func TestLookupPropagatesProviderFailures(t *testing.T) {
	tests := []struct {
		name, body, contentType, input, code string
		status                               int
	}{
		{"rate limited", "rate limit", "text/plain", "2404.06654", "http_error", 429},
		{"provider unavailable", "upstream failed", "text/plain", "2404.06654", "http_error", 503},
		{"SPA homepage", "<!doctype html><html><title>LazyMind</title></html>", "text/html", "2404.06654", "invalid_response", 200},
		{"HTML mislabeled as XML", "<html><body>not found</body></html>", "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"no result", feed(""), "application/atom+xml", "2404.06654", "not_found", 200},
		{"wrong namespace", strings.ReplaceAll(feed(validEntry), "http://www.w3.org/2005/Atom", "https://evil.test"), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"broken XML", "<feed xmlns=\"http://www.w3.org/2005/Atom\"><entry>", "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"wrong ID", strings.ReplaceAll(feed(validEntry), "2404.06654v3", "2404.06655v3"), "application/atom+xml", "2404.06654", "identifier_mismatch", 200},
		{"wrong version", feed(validEntry), "application/atom+xml", "2404.06654v2", "identifier_mismatch", 200},
		{"missing version", strings.ReplaceAll(feed(validEntry), "2404.06654v3", "2404.06654"), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"missing title", feed(strings.ReplaceAll(validEntry, "RULER: What's the Real Context Size\n of Your Long-Context Language Models?", "")), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"placeholder title", feed(strings.ReplaceAll(validEntry, "RULER: What's the Real Context Size\n of Your Long-Context Language Models?", "arXiv:2404.06654v3")), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"missing authors", feed(strings.ReplaceAll(strings.ReplaceAll(validEntry, "<author><name>Cheng-Ping Hsieh</name></author>", ""), "<author><name>Simeng Sun</name></author>", "")), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"incomplete author", feed(strings.ReplaceAll(validEntry, "<name>Simeng Sun</name>", "<name> </name>")), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"missing abstract", feed(strings.ReplaceAll(validEntry, "We study context\n lengths.", "")), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"invalid published", strings.ReplaceAll(feed(validEntry), "2024-04-09T18:00:00Z", "yesterday"), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"multiple entries", feed(validEntry + validEntry), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"trailing document", feed(validEntry) + `<html>error</html>`, "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"foreign entry namespace", feed(strings.Replace(validEntry, "<entry>", `<entry xmlns="https://evil.test">`, 1)), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"Atom provider error", feed(`<entry><id>http://arxiv.org/api/errors#incorrect_id_format_for_2404.06654</id><title>Error</title><summary>incorrect id format</summary></entry>`), "application/atom+xml", "2404.06654", "invalid_response", 200},
		{"oversized response", feed(strings.Repeat(" ", 2*1024*1024) + validEntry), "application/atom+xml", "2404.06654", "invalid_response", 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			got, err := client.Lookup(context.Background(), tt.input)
			if err == nil {
				t.Fatalf("must not succeed: %#v", got)
			}
			lookupErr := requireCode(t, err, tt.code)
			if tt.code == "http_error" && lookupErr.StatusCode != tt.status {
				t.Fatalf("HTTP status lost: %#v", lookupErr)
			}
			if tt.name == "SPA homepage" && !strings.Contains(err.Error(), "HTML") {
				t.Fatalf("HTML failure not identified: %v", err)
			}
		})
	}
}

func TestLookupCanonicalizesLegacyResponseAndKeepsMissingDOIEmpty(t *testing.T) {
	entry := strings.ReplaceAll(validEntry, "2404.06654v3", "math.CA/0611800v2")
	entry = strings.ReplaceAll(entry, `<arxiv:doi xmlns:arxiv="http://arxiv.org/schemas/atom">10.1234/ruler</arxiv:doi>`, "")
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id_list") != "math/0611800v2" {
			t.Errorf("non-canonical API query %s", r.URL)
		}
		fmt.Fprint(w, feed(entry))
	})
	got, err := client.Lookup(context.Background(), "arXiv:math.CA/0611800v2")
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseID != "math/0611800" || got.Version != "v2" || got.DOI != "" || got.PDFURL != "https://arxiv.org/pdf/math/0611800v2" {
		t.Fatalf("incorrect legacy metadata %#v", got)
	}
}

func TestLookupSerializesConcurrentConnections(t *testing.T) {
	var active, peak atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		count := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); count > old; old = peak.Load() {
			if peak.CompareAndSwap(old, count) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		fmt.Fprint(w, feed(validEntry))
	})
	var group sync.WaitGroup
	for i := 0; i < 3; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := client.Lookup(context.Background(), "2404.06654"); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if peak.Load() != 1 {
		t.Fatalf("%d concurrent provider connections; want 1", peak.Load())
	}
}

func TestLookupDoesNotRequestArbitraryInputOrRedirects(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "https://example.org/metadata", http.StatusFound)
	})
	_, err := client.Lookup(context.Background(), "https://evil.test/pdf/2404.06654")
	requireCode(t, err, "invalid_input")
	if calls.Load() != 0 {
		t.Fatal("invalid input caused a request")
	}
	_, err = client.Lookup(context.Background(), "2404.06654")
	requireCode(t, err, "http_error")
	if calls.Load() != 1 {
		t.Fatalf("calls=%d; expected one official request", calls.Load())
	}
}

func TestLookupHonorsContextTimeoutAndPacing(t *testing.T) {
	t.Run("network deadline", func(t *testing.T) {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		_, err := client.Lookup(ctx, "2404.06654")
		requireCode(t, err, "network_error")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline cause lost: %v", err)
		}
	})
	t.Run("pace all calls and cancel while queued", func(t *testing.T) {
		var mu sync.Mutex
		var arrivals []time.Time
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			arrivals = append(arrivals, time.Now())
			mu.Unlock()
			fmt.Fprint(w, feed(validEntry))
		}))
		defer srv.Close()
		client := NewClientWithOptions(Options{APIBaseURL: srv.URL, HTTPClient: srv.Client(), MinInterval: 80 * time.Millisecond, Timeout: time.Second})
		if _, err := client.Lookup(context.Background(), "2404.06654"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		_, err := client.Lookup(ctx, "2404.06654")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued cancellation lost: %v", err)
		}
		if _, err := client.Lookup(context.Background(), "2404.06654"); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(arrivals) != 2 || arrivals[1].Sub(arrivals[0]) < 70*time.Millisecond {
			t.Fatalf("provider pacing failed: %v", arrivals)
		}
	})
}
