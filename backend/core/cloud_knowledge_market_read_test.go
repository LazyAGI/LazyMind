package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/cloudsession"
)

type desktopKnowledgeTransport func(*http.Request) (*http.Response, error)

func (f desktopKnowledgeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func desktopKnowledgeRouter(t *testing.T, signedIn bool, transport desktopKnowledgeTransport) *mux.Router {
	t.Helper()
	t.Setenv("LAZYMIND_CLOUD_BASE_URL", "https://cloud.example")
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	session := cloudsession.NewService(cloudsession.ServiceDeps{Store: cloudsession.NewMemorySecureTokenStore(), Now: func() time.Time { return now }})
	if signedIn {
		if err := session.Establish(context.Background(), cloudsession.TokenPair{AccessToken: "desktop-cloud-access-fixture", RefreshToken: "desktop-cloud-refresh-fixture", AccessExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	previousSession, previousTransport := cloudsession.DefaultService(), http.DefaultTransport
	cloudsession.SetDefaultService(session)
	http.DefaultTransport = transport
	t.Cleanup(func() { cloudsession.SetDefaultService(previousSession); http.DefaultTransport = previousTransport })
	router := mux.NewRouter()
	router.UseEncodedPath()
	registerAllRoutes(router)
	return router
}

func TestDesktopDynamicKnowledgeQueriesCurrentCloudContract(t *testing.T) {
	for _, detail := range []bool{false, true} {
		t.Run(map[bool]string{false: "list", true: "detail"}[detail], func(t *testing.T) {
			calls := 0
			router := desktopKnowledgeRouter(t, true, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer desktop-cloud-access-fixture" {
					t.Error("incorrect Cloud method or token boundary")
				}
				item := map[string]any{"catalog_key": "same-key", "version": 7, "category": "industry", "name": "Cloud catalog", "description": "Cloud description", "icon": "", "domain": "cloud-domain", "tags": []string{}, "online_access_url": "", "data_source": "fixture", "published_at": "2026-09-07T00:00:00Z", "updated_at": "2026-09-07T00:00:00Z"}
				var value any
				if detail {
					if r.URL.Path != "/v1/knowledge-market/items/same-key" {
						t.Errorf("detail requested wrong endpoint: %s", r.URL.Path)
					}
					item["package_url"] = "https://packages.example.test/fixture.zip"
					item["package_revision"] = "fixture"
					item["source_adapter"] = "generic_markdown"
					item["adapter_options"] = map[string]any{}
					item["sample_questions"] = []string{"fixture question"}
					value = item
				} else {
					if r.URL.Path != "/v1/knowledge-market/items" {
						t.Errorf("list requested obsolete or incorrect endpoint: %s", r.URL.Path)
					}
					query := r.URL.Query()
					if query.Get("cursor") != "next" || query.Get("page_size") != "20" || query.Get("category") != "industry" || query.Get("domain") != "cloud-domain" || query.Get("q") != "law" {
						t.Errorf("Cloud query lost fields: %v", query)
					}
					value = map[string]any{"items": []any{item}, "catalog_revision": 9, "next_cursor": "next-2"}
				}
				body, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Etag": {`"catalog-fixture"`}}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})
			path := "/cloud/knowledge-market?cursor=next&page_size=20&category=industry&domain=cloud-domain&q=law"
			if detail {
				path = "/cloud/knowledge-market/items/same-key"
			}
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("X-User-Id", "local-owner")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("Core dynamic knowledge query status=%d body=%s", w.Code, w.Body.String())
			}
			var response struct {
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("expected one Cloud query, got %d", calls)
			}
			if detail {
				if response.Data["catalog_key"] != "same-key" || response.Data["version"] != float64(7) {
					t.Fatalf("Cloud identity/version changed: %v", response.Data)
				}
			} else if response.Data["next_cursor"] != "next-2" || response.Data["catalog_revision"] != float64(9) {
				t.Fatalf("Cloud pagination metadata missing: %v", response.Data)
			}
		})
	}
}

func TestDesktopDynamicKnowledgeDoesNotFallbackToLocalOnCloudFailure(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			router := desktopKnowledgeRouter(t, true, func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"3"}}, Body: io.NopCloser(strings.NewReader(`{"code":3000004,"message":"upstream-private-secret-canary","request_id":"fixture"}`))}, nil
			})
			r := httptest.NewRequest("GET", "/cloud/knowledge-market/items/same-key", nil)
			r.Header.Set("X-User-Id", "local-owner")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if calls != 1 {
				t.Fatalf("Cloud detail route did not query its source: calls=%d status=%d", calls, w.Code)
			}
			if w.Code == 200 || strings.Contains(w.Body.String(), "secret-canary") {
				t.Fatalf("Cloud failure fell back or leaked upstream details: %s", w.Body.String())
			}
		})
	}
}

func TestDesktopDynamicKnowledgeSignedOutDoesNotCallCloud(t *testing.T) {
	calls := 0
	router := desktopKnowledgeRouter(t, false, func(r *http.Request) (*http.Response, error) { calls++; return nil, context.Canceled })
	r := httptest.NewRequest("GET", "/cloud/knowledge-market", nil)
	r.Header.Set("X-User-Id", "local-owner")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("Cloud signed-out route did not return the required JSON error: status=%d body=%q", w.Code, w.Body.String())
	}
	if calls != 0 || body["code"] != float64(2002920) {
		t.Fatalf("missing distinct Cloud signed-out result: calls=%d status=%d body=%s", calls, w.Code, w.Body.String())
	}
}
