package academic

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPDFDiscoveryKeepsCompletedProviderOnDeadline(t *testing.T) {
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.unpaywall.org" {
			return 200, `{"best_oa_location":{"url_for_pdf":"https://repo.example/paper.pdf"}}`
		}
		<-r.Context().Done()
		return 503, "unavailable"
	})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	candidates, result := discoverDOIPDFs(ctx, client, "10.1234/test", "")
	if len(candidates) != 1 || candidates[0].URL != "https://repo.example/paper.pdf" || result.Err == nil || !result.Retryable {
		t.Fatalf("completed provider lost: candidates=%+v result=%+v", candidates, result)
	}
}

func TestOpenDOIPDFCancellationStopsRetries(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: importErrorTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), paperPDFClientKey{}, client), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := lookupOpenDOIPDF(ctx, "10.1234/test")
	if result.Candidate != nil || result.Err == nil || calls.Load() != 2 || time.Since(started) > time.Second {
		t.Fatalf("cancellation ignored: calls=%d result=%+v", calls.Load(), result)
	}
}

func TestOpenDOIPDFProbesCompletedProviderAfterDiscoveryDeadline(t *testing.T) {
	var metadataCalls atomic.Int32
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		switch r.URL.Host {
		case "api.unpaywall.org":
			metadataCalls.Add(1)
			return 200, `{"best_oa_location":{"url_for_pdf":"https://repo.example/paper.pdf"}}`
		case "api.openalex.org":
			<-r.Context().Done()
			return 503, "unavailable"
		default:
			return 206, "%PDF-1.7 test"
		}
	})
	result := lookupOpenDOIPDF(context.WithValue(t.Context(), paperPDFClientKey{}, client), "10.1234/test")
	if result.Candidate == nil || result.Candidate.URL != "https://repo.example/paper.pdf" || metadataCalls.Load() != 1 {
		t.Fatalf("discovery consumed probe budget: calls=%d result=%+v", metadataCalls.Load(), result)
	}
}

func TestDOIPDFUsesExactOpenAlexLocationWhenUnpaywallUnavailable(t *testing.T) {
	var looked atomic.Bool
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		switch r.URL.Host {
		case "api.unpaywall.org":
			return 503, "unavailable"
		case "api.openalex.org":
			looked.Store(true)
			if r.URL.Query().Get("filter") != "doi:https://doi.org/10.1234/test" {
				t.Errorf("not exact DOI query: %s", r.URL)
			}
			return 200, `{"results":[{"doi":"https://doi.org/10.1234/test","best_oa_location":{"is_oa":true,"pdf_url":"https://repo.example/p.pdf","license":"cc-by","version":"acceptedVersion"}}]}`
		case "repo.example":
			return 206, "%PDF-1.7 test"
		default:
			return 404, ""
		}
	})
	got := lookupDOIPDFSources(t.Context(), "10.1234/test", nil, client)
	if !looked.Load() || got.Candidate == nil || got.Candidate.SourceProvider != "openalex" || got.Candidate.License != "cc-by" || got.Candidate.Version != "acceptedVersion" {
		t.Fatalf("OpenAlex PDF not found: %+v", got)
	}
	if !strings.Contains(strings.Join(got.Warnings, ";"), "503") {
		t.Fatalf("provider error lost: %+v", got)
	}
}

func TestDOIPDFRejectsOpenAlexWrongDOI(t *testing.T) {
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		if r.URL.Host == "api.openalex.org" {
			return 200, `{"results":[{"doi":"https://doi.org/10.1234/other","best_oa_location":{"is_oa":true,"pdf_url":"https://repo.example/wrong.pdf"}}]}`
		}
		if r.URL.Host == "repo.example" {
			t.Error("fetched another paper")
		}
		return 404, ""
	})
	got := lookupDOIPDFSources(t.Context(), "10.1234/test", nil, client)
	if got.Candidate != nil {
		t.Fatalf("wrong paper accepted: %+v", got)
	}
}

func TestIndexedPDFDeduplicatesCopiesAcrossProviders(t *testing.T) {
	var probes atomic.Int32
	client := pdfLookupTestClient(func(r *http.Request) (int, string) {
		switch r.URL.Host {
		case "api.unpaywall.org":
			return 200, `{"best_oa_location":{"url_for_pdf":"https://repo.example/p.pdf#page=1"}}`
		case "api.openalex.org":
			return 200, `{"results":[{"doi":"10.1234/test","locations":[{"is_oa":true,"pdf_url":"https://repo.example/p.pdf"}]}]}`
		default:
			probes.Add(1)
			return 200, "<html>not PDF</html>"
		}
	})
	got := lookupDOIPDFSources(t.Context(), "10.1234/test", nil, client)
	if probes.Load() != 1 || got.Candidate != nil {
		t.Fatalf("duplicate probes=%d result=%+v", probes.Load(), got)
	}
}

func TestOpenAlexLandingPageResolvesAndValidatesPDF(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(fmt.Sprint(wrong), func(t *testing.T) {
			var downloaded atomic.Bool
			client := pdfLookupTestClient(func(r *http.Request) (int, string) {
				switch r.URL.Host {
				case "api.unpaywall.org":
					return 404, ""
				case "api.openalex.org":
					return 200, `{"results":[{"doi":"10.1234/test","locations":[{"is_oa":true,"landing_page_url":"https://repo.example/paper"}]}]}`
				case "repo.example":
					if r.URL.Path == "/paper" {
						doi := "10.1234/test"
						if wrong {
							doi = "10.1234/other"
						}
						return 200, `<meta name="citation_doi" content="` + doi + `"><meta name="citation_pdf_url" content="/paper.pdf">`
					}
					downloaded.Store(true)
					return 206, "%PDF-1.7 test"
				default:
					return 404, ""
				}
			})
			got := lookupDOIPDFSources(t.Context(), "10.1234/test", nil, client)
			if wrong {
				if got.Candidate != nil || downloaded.Load() {
					t.Fatal("wrong DOI PDF downloaded")
				}
			} else if got.Candidate == nil || got.Candidate.URL != "https://repo.example/paper.pdf" || got.Candidate.SourceType != "open_access_pdf" {
				t.Fatalf("landing was not resolved: %+v", got)
			}
		})
	}
}

func TestPublicLandingRejectsPrivateAndScriptPDF(t *testing.T) {
	base, _ := url.Parse("https://publisher.example/paper")
	for _, address := range []string{"http://127.0.0.1/private", "https://169.254.169.254/metadata", "javascript:alert(1)"} {
		if _, err := publicLandingPDFURL([]byte(`<meta name="citation_pdf_url" content="`+address+`">`), base, "10.1234/test"); err == nil {
			t.Fatal(address)
		}
	}
}
