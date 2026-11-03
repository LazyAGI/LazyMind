package academic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUnpaywallCandidateRankingDedupAndLimit(t *testing.T) {
	var record unpaywallResponse
	err := json.Unmarshal([]byte(`{"best_oa_location":{"url_for_pdf":"https://repo.example/best.pdf#page=1","version":"acceptedVersion"},"oa_locations":[{"url_for_pdf":"http://127.0.0.1/private"},{"url_for_pdf":"https://repo.example/best.pdf"},{"url_for_pdf":"https://repo.example/preprint.pdf","version":"submittedVersion"},{"url_for_pdf":"https://publisher.example/final.pdf","version":"publishedVersion","host_type":"publisher"},{"url_for_pdf":"https://repo.example/accepted.pdf","version":"acceptedVersion"},{"url_for_pdf":"https://repo.example/extra.pdf"},{"url_for_landing_page":"https://repo.example/landing"}],"oa_locations_embargoed":[{"url_for_pdf":"https://repo.example/embargoed.pdf"}]}`), &record)
	if err != nil {
		t.Fatal(err)
	}
	got := unpaywallCandidates(record)
	want := []string{"https://repo.example/best.pdf", "https://publisher.example/final.pdf", "https://repo.example/accepted.pdf", "https://repo.example/preprint.pdf"}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i].URL != want[i] {
			t.Fatalf("%d: %+v", i, got)
		}
	}
}
func TestUnpaywallNoRecordVersusServiceFailure(t *testing.T) {
	for _, status := range []int{404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := pdfLookupTestClient(func(*http.Request) (int, string) { return status, "unavailable" })
			got := lookupOpenPDF(t.Context(), client, "10.1234/test")
			if got.Candidate != nil || (len(got.Warnings) > 0) != (status != 404) {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func TestUnpaywallSlowLocationsDoNotBlockOtherCopies(t *testing.T) {
	var running, peak, ended atomic.Int32
	client := &http.Client{Transport: importErrorTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"oa_locations":[{"url_for_pdf":"https://slow.example/1.pdf"},{"url_for_pdf":"https://slow.example/2.pdf"},{"url_for_pdf":"https://fast.example/3.pdf"}]}`
		if r.URL.Host != "api.unpaywall.org" {
			n := running.Add(1)
			defer running.Add(-1)
			for {
				old := peak.Load()
				if old >= n || peak.CompareAndSwap(old, n) {
					break
				}
			}
			if r.URL.Host == "slow.example" {
				defer ended.Add(1)
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			body = "%PDF-1.7 test"
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	start := time.Now()
	got := lookupOpenPDF(ctx, client, "10.1234/test")
	if got.Candidate == nil || got.Candidate.URL != "https://fast.example/3.pdf" || peak.Load() > 2 || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("%+v peak=%d", got, peak.Load())
	}
	deadline := time.Now().Add(time.Second)
	for ended.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if ended.Load() != 2 {
		t.Fatal("probe workers did not stop")
	}
}
func TestUnpaywallDoesNotAcceptHTMLAsPDF(t *testing.T) {
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.unpaywall.org" {
			return 200, `{"oa_locations":[{"url_for_pdf":"https://repo.example/p.pdf"}]}`
		}
		return 200, "<html>sign in</html>"
	})
	got := lookupOpenPDF(t.Context(), client, "10.1234/test")
	if got.Candidate != nil || len(got.Warnings) != 1 {
		t.Fatalf("%+v", got)
	}
}

// A usable PDF with a one-second first-byte latency must not be discarded
// while the enclosing three-second lookup still has time remaining.
func TestUnpaywallUsesRemainingBudgetForPDF(t *testing.T) {
	client := &http.Client{Transport: importErrorTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"best_oa_location":{"url_for_pdf":"https://repo.example/paper.pdf"}}`
		if r.URL.Host != "api.unpaywall.org" {
			select {
			case <-time.After(time.Second):
				body = "%PDF-1.7 test"
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	got := lookupOpenPDF(ctx, client, "10.1234/test")
	if got.Candidate == nil {
		t.Fatalf("valid PDF discarded before outer deadline: %+v", got)
	}
}
