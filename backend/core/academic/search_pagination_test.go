package academic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type searchTestResponse struct {
	Papers   []PaperRef      `json:"papers"`
	Sources  []searchOutcome `json:"sources"`
	CacheHit bool            `json:"cache_hit"`
}

func searchTestRecord(t *testing.T, id string) orm.AcademicExpansion {
	t.Helper()
	record := orm.AcademicExpansion{ID: id, UserID: "u", DatasetID: "papers", RequestOwner: "u", RequestKey: id, Status: "preparing", DiscoveryMode: "external", PreparationRevision: 1, ResultJSON: marshal(ReviewWorkflowInput{Papers: []WorkflowPaper{}}), DiscoveryJSON: marshal([]paperDiscovery{})}
	if err := store.DB().Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	return record
}

func callSearchTest(t *testing.T, id string, body map[string]any) searchTestResponse {
	t.Helper()
	body["expansion_id"] = id
	w := httptest.NewRecorder()
	SearchPapers(w, paperRequest(string(marshal(body))))
	var response searchTestResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return response
}

func TestSearchSourcePagesCachePartialResultsAndRespectCooldown(t *testing.T) {
	for _, limited := range []bool{false, true} {
		t.Run(fmt.Sprint("limited=", limited), func(t *testing.T) {
			db := paperFixture(t)
			base := orm.BaseModel{CreateUserID: "u"}
			for _, row := range []any{
				&orm.UserModelProvider{ID: "sci", Name: "Sciverse", Category: "datasource", BaseModel: base},
				&orm.UserModelProviderGroup{ID: "sci-key", UserModelProviderID: "sci", Name: "test", APIKey: "fixture", IsVerified: true, BaseModel: base},
				&orm.UserSelectedProvider{UserID: "u", Category: "datasource", UserModelProviderGroupID: "sci-key"},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			record := searchTestRecord(t, "paged")
			alexCalls, sciCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sciCalls++
				var request struct {
					Query  string `json:"query"`
					Cursor string `json:"cursor"`
					Page   int    `json:"page"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if limited {
					w.WriteHeader(429)
					fmt.Fprint(w, `{"detail":{"code":"rate_limited","retry_at":"2099-01-01T00:00:00Z"}}`)
					return
				}
				if request.Cursor == "sci-page-two" {
					if request.Query != "retrieval" || request.Page != 1 {
						t.Errorf("lost bound query or cursor: %+v", request)
					}
					fmt.Fprint(w, `{"items":[{"title":"Sciverse second","url":"https://doi.org/10.1000/sciverse-two","extra":{"doi":"10.1000/sciverse-two","doc_id":"two"}}],"total_count":2,"total_pages":2,"page":2}`)
				} else {
					fmt.Fprint(w, `{"items":[{"title":"Shared paper","url":"https://doi.org/10.1000/shared","extra":{"doi":"10.1000/shared","doc_id":"one"}}],"total_count":2,"next_cursor":"sci-page-two","page":1}`)
				}
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture")
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			previous := http.DefaultTransport
			http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.openalex.org" {
					return previous.RoundTrip(r)
				}
				alexCalls++
				id, doi := "W101", "shared"
				if r.URL.Query().Get("page") == "2" {
					id, doi = "W102", "alex-two"
				}
				if r.URL.Query().Get("cursor") != "" {
					t.Error("keyless OpenAlex should use basic pages")
				}
				body := fmt.Sprintf(`{"results":[{"id":"https://openalex.org/%s","title":"%s","doi":"https://doi.org/10.1000/%s"}],"meta":{"count":2}}`, id, id, doi)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			query := func() map[string]any {
				return map[string]any{"query": "retrieval", "sources": []string{"openalex", "sciverse"}, "max_results": 1}
			}
			first := callSearchTest(t, record.ID, query())
			if len(first.Papers) != 1 || len(first.Sources) != 2 || first.Sources[0].NextCursor == "" {
				t.Fatalf("first page: %+v", first)
			}
			second := callSearchTest(t, record.ID, map[string]any{"cursor": first.Sources[0].NextCursor})
			if len(second.Sources) != 1 || second.Sources[0].Source != "openalex" || second.Sources[0].HasMore || second.Papers[0].PaperID == first.Papers[0].PaperID || sciCalls != 1 {
				t.Fatalf("did not resume only OpenAlex: %+v", second)
			}
			if limited {
				if first.Sources[1].Status != "rate_limited" || first.Sources[1].RetryAt == nil {
					t.Fatalf("bridge lost HTTP status: %+v", first)
				}
				again := callSearchTest(t, record.ID, query())
				if !again.Sources[0].Reused || !again.Sources[1].Reused || alexCalls != 2 || sciCalls != 1 {
					t.Fatalf("partial cache or cooldown failed: %+v", again)
				}
				other := searchTestRecord(t, "another-task")
				callSearchTest(t, other.ID, query())
				if sciCalls != 1 {
					t.Fatal("shared credential cooldown did not cover another task")
				}
				var request paperSearchRequest
				if err := configureSearchSource(t.Context(), "u", "sciverse", &request); err != nil {
					t.Fatal(err)
				}
				if err := store.State().Del(t.Context(), "academic/search/cooldown/"+request.scope); err != nil {
					t.Fatal(err)
				}
				limited = false
				callSearchTest(t, record.ID, query())
				if alexCalls != 3 || sciCalls != 2 {
					t.Fatal("recovery re-fetched a successful source", alexCalls, sciCalls)
				}
			} else {
				if first.Sources[1].NewPaperCount != 0 || first.Sources[1].NextCursor == "" {
					t.Fatalf("cross-source deduplication lost: %+v", first)
				}
				more := callSearchTest(t, record.ID, map[string]any{"cursor": first.Sources[1].NextCursor})
				if len(more.Sources) != 1 || more.Sources[0].Source != "sciverse" || more.Sources[0].HasMore || more.Sources[0].NewPaperCount != 1 || alexCalls != 2 {
					t.Fatalf("Sciverse continuation: %+v", more)
				}
				cached := callSearchTest(t, record.ID, query())
				if !cached.CacheHit || alexCalls != 2 || sciCalls != 2 {
					t.Fatal("source pages were not cached")
				}
			}
			w := httptest.NewRecorder()
			SearchPapers(w, paperRequest(string(marshal(map[string]any{"expansion_id": record.ID, "cursor": first.Sources[0].NextCursor, "sources": []string{"arxiv"}}))))
			if w.Code != 400 {
				t.Fatal("cursor accepted a different source", w.Code)
			}
			other := searchTestRecord(t, "cursor-boundary")
			w = httptest.NewRecorder()
			SearchPapers(w, paperRequest(string(marshal(map[string]any{"expansion_id": other.ID, "cursor": first.Sources[0].NextCursor}))))
			if w.Code != 400 {
				t.Fatal("cursor crossed task boundary", w.Code)
			}
		})
	}
}

func TestOpenAlexDailyBudgetHasResetTime(t *testing.T) {
	headers := http.Header{"X-Ratelimit-Remaining": []string{"0"}, "X-Ratelimit-Reset": []string{"3600"}}
	failure := scholarlyHTTPFailure(429, headers, strings.NewReader(`{"error":"not_enough_credits"}`))
	if failure.Status != "quota_exhausted" || failure.RetryAt == nil || time.Until(*failure.RetryAt) < 59*time.Minute {
		t.Fatalf("lost daily budget semantics: %+v", failure)
	}
}
