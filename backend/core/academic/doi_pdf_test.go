package academic

import (
	"context"
	"encoding/json"
	"io"
	"lazymind/core/common/orm"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func pdfLookupTestClient(fn func(*http.Request) (int, string)) *http.Client {
	return &http.Client{Transport: importErrorTransport(func(r *http.Request) (*http.Response, error) {
		status, body := fn(r)
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
}
func TestDOIPDFLookupPrefersOpenAccessAndChecksPDF(t *testing.T) {
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.unpaywall.org" {
			return 200, `{"best_oa_location":{"url_for_pdf":"https://publisher.example/paper.pdf","license":"cc-by"}}`
		}
		if r.URL.Host != "publisher.example" || r.Header.Get("Range") != "bytes=0-1023" {
			t.Fatalf("unexpected fetch: %s %v", r.URL, r.Header)
		}
		return 206, "%PDF-1.7 test"
	})
	result := lookupDOIPDFWithClient(t.Context(), "10.1234/test", "https://mirror.example", client)
	if result.Candidate == nil || result.Candidate.SourceProvider != "unpaywall" || result.Candidate.License != "cc-by" || len(result.Warnings) != 0 {
		t.Fatalf("%+v", result)
	}
}
func TestDOIPDFLookupFallsBackAndPreservesProviderFailure(t *testing.T) {
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		switch r.URL.Host {
		case "api.unpaywall.org":
			return 503, "unavailable"
		case "mirror.example":
			if r.URL.Path != "/10.1234/test" {
				t.Fatal(r.URL)
			}
			return 200, `<iframe src="//files.example/paper.pdf#page=2"></iframe>`
		case "files.example":
			return 206, "%PDF-1.7 test"
		}
		t.Fatal(r.URL)
		return 404, ""
	})
	result := lookupDOIPDFWithClient(t.Context(), "10.1234/test", "https://mirror.example", client)
	if result.Candidate == nil || result.Candidate.SourceProvider != "scihub" || result.Candidate.URL != "https://files.example/paper.pdf" || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "503") {
		t.Fatalf("%+v", result)
	}
}
func TestDOIPDFLookupRejectsNonPDFAndPrivateTargets(t *testing.T) {
	for _, target := range []string{"https://files.example/paper.pdf", "http://127.0.0.1/internal", "https://169.254.169.254/latest/meta-data"} {
		t.Run(target, func(t *testing.T) {
			client := pdfLookupTestClient(func(r *http.Request) (int, string) {
				switch r.URL.Host {
				case "api.unpaywall.org":
					return 200, `{"best_oa_location":null}`
				case "mirror.example":
					return 200, `<embed src="` + target + `">`
				case "files.example":
					return 200, "<html>captcha</html>"
				}
				t.Fatalf("private target fetched: %s", r.URL)
				return 404, ""
			})
			result := lookupDOIPDFWithClient(t.Context(), "10.1234/test", "https://mirror.example", client)
			if result.Candidate != nil || len(result.Warnings) == 0 {
				t.Fatalf("%+v", result)
			}
		})
	}
}
func TestDOIPDFLookupHonorsSharedDeadline(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: importErrorTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	result := lookupDOIPDFWithClient(ctx, "10.1234/test", "https://mirror.example", client)
	if time.Since(start) > 300*time.Millisecond || calls != 1 || result.Candidate != nil || len(result.Warnings) == 0 {
		t.Fatalf("deadline escaped: calls=%d %+v", calls, result)
	}
}
func TestSciHubPDFURLResolvesRelativeAndRejectsScripts(t *testing.T) {
	base, _ := url.Parse("https://mirror.example/10.1234/test")
	got, err := sciHubPDFURL([]byte(`<object data="/files/paper.pdf"></object>`), base)
	if err != nil || got != "https://mirror.example/files/paper.pdf" {
		t.Fatal(got, err)
	}
	if _, err := sciHubPDFURL([]byte(`<iframe src="javascript:alert(1)"></iframe>`), base); err == nil {
		t.Fatal("script URL accepted")
	}
}

func TestSciHubDirectPDFDoesNotReadEntireDocumentDuringLookup(t *testing.T) {
	client := pdfLookupTestClient(func(r *http.Request) (int, string) { return 200, "%PDF-1.7\n" + strings.Repeat("x", 3<<20) })
	candidate, err := lookupSciHubPDF(t.Context(), client, "https://mirror.example", "10.1234/test")
	if err != nil || candidate.URL != "https://mirror.example/10.1234/test" {
		t.Fatal(candidate, err)
	}
}

func TestDOIPreviewSeparatesMetadataAndPDFAndIssuesReceipts(t *testing.T) {
	paperFixture(t)
	old := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "scan-control-plane:18080" {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header), Request: r}, nil
		}
		if r.URL.Host != "api.crossref.org" {
			t.Fatalf("metadata phase fetched PDF provider: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"message":{"DOI":"10.1234/test","title":["Confirmed title"],"author":[{"given":"Test","family":"Author"}]}}`)), Header: make(http.Header), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	type previewResponse struct {
		Items []PreviewItem `json:"items"`
		Token string        `json:"preview_token"`
	}
	w := httptest.NewRecorder()
	PreviewImport(w, paperRequest(`{"identifier":"10.1234/test","target_dataset_id":"papers","lookup_fulltext":false}`))
	var metadata previewResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &metadata) != nil || len(metadata.Items) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	if metadata.Token == "" || len(metadata.Items[0].Candidates) != 0 || metadata.Items[0].Paper.Title != "Confirmed title" {
		t.Fatalf("%+v", metadata)
	}
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.unpaywall.org" {
			return 503, "unavailable"
		}
		if r.URL.Host == "sci-hub.ru" {
			return 200, `<iframe src="https://files.example/paper.pdf"></iframe>`
		}
		return 206, "%PDF-1.7 test"
	})
	t.Setenv("ACADEMIC_SCIHUB_BASE_URL", "https://sci-hub.ru")
	req := paperRequest(`{"work_ids":["` + metadata.Items[0].WorkID + `"],"target_dataset_id":"papers","lookup_fulltext":true}`)
	req = req.WithContext(context.WithValue(req.Context(), paperPDFClientKey{}, client))
	w = httptest.NewRecorder()
	PreviewImport(w, req)
	var pdf previewResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &pdf) != nil || len(pdf.Items) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	if pdf.Token == "" || pdf.Token == metadata.Token || len(pdf.Items[0].Candidates) != 1 || pdf.Items[0].Candidates[0].SourceProvider != "scihub" || len(pdf.Items[0].Warnings) != 1 {
		t.Fatalf("%+v", pdf)
	}
}

func TestDOIPDFLookupEnforcesItsOwnProviderAndTotalDeadlines(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: importErrorTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > paperPDFProviderTimeout {
			t.Fatal("missing provider deadline")
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	start := time.Now()
	result := lookupDOIPDFWithClient(t.Context(), "10.1234/test", "https://mirror.example", client)
	if elapsed := time.Since(start); elapsed > paperPDFLookupTimeout+time.Second || calls != 2 || result.Candidate != nil || len(result.Warnings) < 2 {
		t.Fatalf("unbounded lookup: %v calls=%d %+v", elapsed, calls, result)
	}
}

func TestDOIPreviewUnavailablePDFReturnsEmptyArrayAndWarnings(t *testing.T) {
	db := paperFixture(t)
	if err := db.Model(&orm.AcademicWork{}).Where("id=?", "a").Update("doi_normalized", "10.1234/test").Error; err != nil {
		t.Fatal(err)
	}
	client := pdfLookupTestClient(func(r *http.Request) (int, string) { return 503, "unavailable" })
	req := paperRequest(`{"work_ids":["a"],"target_dataset_id":"papers","lookup_fulltext":true}`)
	req = req.WithContext(context.WithValue(req.Context(), paperPDFClientKey{}, client))
	w := httptest.NewRecorder()
	PreviewImport(w, req)
	var result struct {
		Items []PreviewItem `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	item := result.Items[0]
	if item.Candidates == nil || len(item.Candidates) != 0 || len(item.Warnings) != 1+len(defaultSciHubSources) || len(item.ManualDownloadURLs) != 1+len(defaultSciHubSources) {
		t.Fatalf("must retain warnings and serialize [], not null: %+v", item)
	}
}

func TestSciHubChallengeIsReportedWithoutAttemptingToSolveIt(t *testing.T) {
	base, _ := url.Parse("https://sci-hub.ru/10.1234/test")
	_, err := sciHubPDFURL([]byte(`<altcha-widget challengeurl="/challenge"></altcha-widget>`), base)
	if err == nil || !strings.Contains(err.Error(), "人机验证") {
		t.Fatal(err)
	}
}

func TestDOIPDFMultipleSourcesRaceAndCancel(t *testing.T) {
	var active, peak atomic.Int32
	canceled := make(chan struct{}, 5)
	client := &http.Client{Transport: importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.unpaywall.org" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"best_oa_location":null}`)), Request: r}, nil
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		if r.URL.Host == "fast.example" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("%PDF-1.7")), Request: r}, nil
		}
		<-r.Context().Done()
		canceled <- struct{}{}
		return nil, r.Context().Err()
	})}
	start := time.Now()
	result := lookupDOIPDFSources(t.Context(), "10.1234/test", []string{"https://slow.example", "https://fast.example", "https://other.example", "https://last.example"}, client)
	if result.Candidate == nil || !strings.Contains(result.Candidate.URL, "fast.example") {
		t.Fatalf("%+v", result)
	}
	if time.Since(start) > time.Second || peak.Load() > 3 {
		t.Fatal("lookup was serial or exceeded concurrency limit")
	}
}

func TestSciHubSourceConfigAndManualLinks(t *testing.T) {
	t.Setenv("ACADEMIC_SCIHUB_BASE_URL", "")
	t.Setenv("ACADEMIC_SCIHUB_BASE_URLS", "")
	if len(sciHubSources()) < 10 {
		t.Fatal("expected ten default endpoints")
	}
	hosts := map[string]bool{}
	for _, source := range sciHubSources() {
		u, _ := url.Parse(source)
		hosts[u.Hostname()] = true
	}
	if len(hosts) < 10 {
		t.Fatal("expected ten distinct domains")
	}
	t.Setenv("ACADEMIC_SCIHUB_BASE_URLS", "https://first.example/,https://first.example;https://second.example,http://127.0.0.1")
	if len(sciHubSources()) != 3 {
		t.Fatal(sciHubSources())
	}
	links := sciHubManualURLs("10.1234/test")
	if len(links) != 3 || links[1] != "https://first.example/10.1234/test" {
		t.Fatal(links)
	}
}
