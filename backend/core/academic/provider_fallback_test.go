package academic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

const fallbackCitation = "A. Author. 2024. A sufficiently long paper title."
const fallbackOpenAlexMetadata = `{"results":[{"id":"https://openalex.org/W123","title":"A sufficiently long paper title","publication_year":2024,"doi":"https://doi.org/10.1234/example","ids":{},"best_oa_location":null}]}`
const fallbackArxivMatch = `<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>https://arxiv.org/abs/2404.06654v1</id><title>A sufficiently long paper title</title><published>2024-04-10T00:00:00Z</published><author><name>A. Author</name></author></entry></feed>`

func TestReferenceResolutionFallsBackAfterOptionalProviderFailure(t *testing.T) {
	for _, provider := range []string{"OpenAlex", "Unpaywall"} {
		t.Run(provider, func(t *testing.T) {
			db := importErrorDB(t)
			installFallbackTransport(t, func(host string) (int, string) {
				switch host {
				case "api.openalex.org":
					if provider == "Unpaywall" {
						return http.StatusOK, fallbackOpenAlexMetadata
					}
				case "export.arxiv.org":
					return http.StatusOK, fallbackArxivMatch
				}
				return http.StatusServiceUnavailable, "unavailable"
			})
			ref := orm.AcademicReference{ID: "ref-fallback", RawText: fallbackCitation, AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
			if err := db.Create(&ref).Error; err != nil {
				t.Fatal(err)
			}

			work, _, status, err := resolveReferenceWithExternal(context.Background(), db, &ref, true)
			if err != nil || work.ArxivIDBase != "2404.06654" || status != "exact" {
				t.Fatalf("arXiv fallback was blocked by %s: work=%+v status=%q error=%v", provider, work, status, err)
			}
			if provider == "Unpaywall" && work.DOINormalized != "10.1234/example" {
				t.Fatalf("fallback discarded the matched OpenAlex DOI: %+v", work)
			}
			var saved orm.AcademicWork
			if err := db.Take(&saved, "id = ?", work.ID).Error; err != nil {
				t.Fatal(err)
			}
			var provenance struct {
				ProviderErrors []string `json:"provider_errors"`
			}
			if err := json.Unmarshal(saved.ProvenanceJSON, &provenance); err != nil {
				t.Fatal(err)
			}
			if len(provenance.ProviderErrors) != 1 || !strings.Contains(provenance.ProviderErrors[0], provider) || !strings.Contains(provenance.ProviderErrors[0], "503") {
				t.Fatalf("fallback lost provider diagnostics: %s", saved.ProvenanceJSON)
			}
		})
	}
}

func TestReferenceResolutionAggregatesProviderFailuresWithoutSaving(t *testing.T) {
	for _, provider := range []string{"OpenAlex", "Unpaywall"} {
		t.Run(provider, func(t *testing.T) {
			db := importErrorDB(t)
			installFallbackTransport(t, func(host string) (int, string) {
				if host == "api.openalex.org" && provider == "Unpaywall" {
					return http.StatusOK, fallbackOpenAlexMetadata
				}
				if host == "export.arxiv.org" {
					return http.StatusTooManyRequests, "rate limited"
				}
				return http.StatusServiceUnavailable, "unavailable"
			})
			ref := orm.AcademicReference{ID: "ref-failure", RawText: fallbackCitation, ResolutionStatus: "unresolved", AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
			if err := db.Create(&ref).Error; err != nil {
				t.Fatal(err)
			}

			_, _, _, err := resolveReferenceWithExternal(context.Background(), db, &ref, true)
			if err == nil || !strings.Contains(err.Error(), provider) || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "arXiv") || !strings.Contains(err.Error(), "429") {
				t.Fatalf("expected all attempted provider failures, got %v", err)
			}
			var count int64
			if err := db.Model(&orm.AcademicWork{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Take(&ref, "id = ?", ref.ID).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 || ref.ResolvedWorkID != "" || ref.ResolutionStatus != "unresolved" {
				t.Fatalf("provider failures fabricated a resolution: works=%d reference=%+v", count, ref)
			}
		})
	}
}

func TestReferenceResolutionFallbackKeepsDiagnosticsOnExistingWork(t *testing.T) {
	db := importErrorDB(t)
	installFallbackTransport(t, func(host string) (int, string) {
		if host == "export.arxiv.org" {
			return http.StatusOK, fallbackArxivMatch
		}
		return http.StatusServiceUnavailable, "unavailable"
	})
	existing := orm.AcademicWork{ID: "existing-work", CanonicalTitle: "A sufficiently long paper title", NormalizedTitle: "a sufficiently long paper title", ArxivIDBase: "2404.06654", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{"pdf_url":"https://publisher.example/paper.pdf"}`)}
	ref := orm.AcademicReference{ID: "ref-existing", RawText: fallbackCitation, AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
	for _, row := range []any{&existing, &ref} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	work, _, _, err := resolveReferenceWithExternal(context.Background(), db, &ref, true)
	if err != nil || work.ID != existing.ID {
		t.Fatalf("existing work was not reused: work=%+v error=%v", work, err)
	}
	if err := db.Take(&existing, "id = ?", existing.ID).Error; err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		PDFURL         string   `json:"pdf_url"`
		ProviderErrors []string `json:"provider_errors"`
	}
	if err := json.Unmarshal(existing.ProvenanceJSON, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance.PDFURL != "https://publisher.example/paper.pdf" || len(provenance.ProviderErrors) != 1 || !strings.Contains(provenance.ProviderErrors[0], "OpenAlex") {
		t.Fatalf("existing work lost provenance or fallback diagnostics: %s", existing.ProvenanceJSON)
	}
}

func TestReferenceResolutionKeepsOpenAlexMetadataWhenUnpaywallHasNoRecord(t *testing.T) {
	db := importErrorDB(t)
	installFallbackTransport(t, func(host string) (int, string) {
		switch host {
		case "api.openalex.org":
			return http.StatusOK, fallbackOpenAlexMetadata
		case "api.unpaywall.org":
			return http.StatusNotFound, `{"message":"DOI not found"}`
		case "export.arxiv.org":
			return http.StatusOK, `<feed xmlns="http://www.w3.org/2005/Atom"></feed>`
		default:
			t.Fatalf("unexpected provider %q", host)
			return 0, ""
		}
	})
	ref := orm.AcademicReference{ID: "ref-metadata", RawText: fallbackCitation, AuthorsJSON: json.RawMessage(`[]`), BBoxJSON: json.RawMessage(`{}`), SegmentIDsJSON: json.RawMessage(`[]`)}
	if err := db.Create(&ref).Error; err != nil {
		t.Fatal(err)
	}

	work, _, _, err := resolveReferenceWithExternal(context.Background(), db, &ref, true)
	if err != nil || work.CanonicalTitle != "A sufficiently long paper title" || work.DOINormalized != "10.1234/example" {
		t.Fatalf("Unpaywall no-record discarded valid metadata: work=%+v error=%v", work, err)
	}
	if len(candidatesForWork(work)) != 0 || strings.Contains(string(work.ProvenanceJSON), "provider_errors") {
		t.Fatalf("no-record fabricated a fulltext candidate or provider failure: %+v", work)
	}
}

func TestResolveDOIOpenAccessTreatsNotFoundAsNoRecord(t *testing.T) {
	installFallbackTransport(t, func(string) (int, string) {
		return http.StatusNotFound, `{"message":"DOI not found"}`
	})
	pdfURL, license, version, err := resolveDOIOpenAccess(context.Background(), "10.1234/example")
	if err != nil || pdfURL != "" || license != "" || version != "" {
		t.Fatalf("Unpaywall no-record must be an empty result: %q, %q, %q, %v", pdfURL, license, version, err)
	}
}

func installFallbackTransport(t *testing.T, response func(string) (int, string)) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		status, body := response(r.URL.Hostname())
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
}
