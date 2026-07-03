package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func TestAgentThreadEventsRouteWinsOverGenericThreadRoute(t *testing.T) {
	r := mux.NewRouter()
	r.UseEncodedPath()
	registerAllRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr-306c5b7b:events", nil)
	var match mux.RouteMatch
	if !r.Match(req, &match) {
		t.Fatalf("expected events route to match")
	}

	gotTemplate, err := match.Route.GetPathTemplate()
	if err != nil {
		t.Fatalf("get matched route template: %v", err)
	}
	if want := "/agent/threads/{thread_id}:events"; gotTemplate != want {
		t.Fatalf("expected template %q, got %q", want, gotTemplate)
	}
	if gotID := match.Vars["thread_id"]; gotID != "thr-306c5b7b" {
		t.Fatalf("expected thread_id %q, got %q", "thr-306c5b7b", gotID)
	}
}

func TestAgentThreadStepEventsRouteWinsOverGenericThreadRoute(t *testing.T) {
	r := mux.NewRouter()
	r.UseEncodedPath()
	registerAllRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr-306c5b7b/events/collect_material", nil)
	var match mux.RouteMatch
	if !r.Match(req, &match) {
		t.Fatalf("expected step events route to match")
	}

	gotTemplate, err := match.Route.GetPathTemplate()
	if err != nil {
		t.Fatalf("get matched route template: %v", err)
	}
	if want := "/agent/threads/{thread_id}/events/{step_id}"; gotTemplate != want {
		t.Fatalf("expected template %q, got %q", want, gotTemplate)
	}
	if gotID := match.Vars["thread_id"]; gotID != "thr-306c5b7b" {
		t.Fatalf("expected thread_id %q, got %q", "thr-306c5b7b", gotID)
	}
	if gotStepID := match.Vars["step_id"]; gotStepID != "collect_material" {
		t.Fatalf("expected step_id %q, got %q", "collect_material", gotStepID)
	}
}

func TestAgentThreadStepsRouteWinsOverGenericThreadRoute(t *testing.T) {
	r := mux.NewRouter()
	r.UseEncodedPath()
	registerAllRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr-306c5b7b/steps", nil)
	var match mux.RouteMatch
	if !r.Match(req, &match) {
		t.Fatalf("expected thread steps route to match")
	}

	gotTemplate, err := match.Route.GetPathTemplate()
	if err != nil {
		t.Fatalf("get matched route template: %v", err)
	}
	if want := "/agent/threads/{thread_id}/steps"; gotTemplate != want {
		t.Fatalf("expected template %q, got %q", want, gotTemplate)
	}
	if gotID := match.Vars["thread_id"]; gotID != "thr-306c5b7b" {
		t.Fatalf("expected thread_id %q, got %q", "thr-306c5b7b", gotID)
	}
}

func TestAgentThreadStepRecordsRouteWinsOverGenericThreadRoute(t *testing.T) {
	r := mux.NewRouter()
	r.UseEncodedPath()
	registerAllRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr-306c5b7b/steps/collect_material/records", nil)
	var match mux.RouteMatch
	if !r.Match(req, &match) {
		t.Fatalf("expected thread step records route to match")
	}

	gotTemplate, err := match.Route.GetPathTemplate()
	if err != nil {
		t.Fatalf("get matched route template: %v", err)
	}
	if want := "/agent/threads/{thread_id}/steps/{step_id}/records"; gotTemplate != want {
		t.Fatalf("expected template %q, got %q", want, gotTemplate)
	}
	if gotID := match.Vars["thread_id"]; gotID != "thr-306c5b7b" {
		t.Fatalf("expected thread_id %q, got %q", "thr-306c5b7b", gotID)
	}
	if gotStepID := match.Vars["step_id"]; gotStepID != "collect_material" {
		t.Fatalf("expected step_id %q, got %q", "collect_material", gotStepID)
	}
}

func TestAgentThreadResultDownloadRouteRegistered(t *testing.T) {
	r := mux.NewRouter()
	r.UseEncodedPath()
	registerAllRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/agent/threads/thr-1/results/eval-reports:download", nil)
	var match mux.RouteMatch
	if !r.Match(req, &match) {
		t.Fatalf("expected thread result download route to match")
	}

	gotTemplate, err := match.Route.GetPathTemplate()
	if err != nil {
		t.Fatalf("get matched route template: %v", err)
	}
	if want := "/agent/threads/{thread_id}/results/{kind}:download"; gotTemplate != want {
		t.Fatalf("expected template %q, got %q", want, gotTemplate)
	}
	if got := match.Vars["thread_id"]; got != "thr-1" {
		t.Fatalf("expected thread_id %q, got %q", "thr-1", got)
	}
	if got := match.Vars["kind"]; got != "eval-reports" {
		t.Fatalf("expected kind %q, got %q", "eval-reports", got)
	}
}

func TestLegacyAgentResultDetailRoutesAreNotRegistered(t *testing.T) {
	r := mux.NewRouter()
	r.UseEncodedPath()
	registerAllRoutes(r)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/agent/threads/thr-1/results/eval-reports/v0001/bad-cases"},
		{http.MethodGet, "/agent/threads/thr-1/results/abtests/abtest.comparison/case-details"},
		{http.MethodGet, "/agent/threads/thr-1/results/traces/trace-1"},
		{http.MethodGet, "/agent/threads/thr-1/results/traces-compare"},
		{http.MethodGet, "/agent/reports/report-1:content"},
		{http.MethodGet, "/agent/diffs/apply-1/file.diff"},
		{http.MethodPost, "/agent/files:content"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		var match mux.RouteMatch
		if r.Match(req, &match) {
			template, _ := match.Route.GetPathTemplate()
			t.Fatalf("expected legacy route %s %q not to match, got %q", tc.method, tc.path, template)
		}
	}
}

func TestSkillDraftPreviewRouteWinsOverGenericSkillRoute(t *testing.T) {
	r := mux.NewRouter()
	registerAllRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/skills/skill-306c5b7b:draft-preview", nil)
	var match mux.RouteMatch
	if !r.Match(req, &match) {
		t.Fatalf("expected draft-preview route to match")
	}

	gotTemplate, err := match.Route.GetPathTemplate()
	if err != nil {
		t.Fatalf("get matched route template: %v", err)
	}
	if want := "/skills/{skill_id}:draft-preview"; gotTemplate != want {
		t.Fatalf("expected template %q, got %q", want, gotTemplate)
	}
	if gotID := match.Vars["skill_id"]; gotID != "skill-306c5b7b" {
		t.Fatalf("expected skill_id %q, got %q", "skill-306c5b7b", gotID)
	}
}

func TestReviewResultActionRoutesRegistered(t *testing.T) {
	r := mux.NewRouter()
	registerAllRoutes(r)

	cases := []struct {
		method string
		path   string
		want   string
		id     string
	}{
		{http.MethodPost, "/skill-review-results/review-1:accept", "/skill-review-results/{review_result_id}:accept", "review-1"},
		{http.MethodPost, "/skill-review-results/review-1:reject", "/skill-review-results/{review_result_id}:reject", "review-1"},
		{http.MethodPost, "/memory-review-results/review-2:accept", "/memory-review-results/{review_result_id}:accept", "review-2"},
		{http.MethodGet, "/evolution/tasks/task-1", "/evolution/tasks/{task_id}", "task-1"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		var match mux.RouteMatch
		if !r.Match(req, &match) {
			t.Fatalf("expected route to match %s %s", tc.method, tc.path)
		}
		gotTemplate, err := match.Route.GetPathTemplate()
		if err != nil {
			t.Fatalf("get matched route template: %v", err)
		}
		if gotTemplate != tc.want {
			t.Fatalf("expected template %q, got %q", tc.want, gotTemplate)
		}
		if strings.Contains(tc.want, "task_id") {
			if got := match.Vars["task_id"]; got != tc.id {
				t.Fatalf("expected task_id %q, got %q", tc.id, got)
			}
			continue
		}
		if got := match.Vars["review_result_id"]; got != tc.id {
			t.Fatalf("expected review_result_id %q, got %q", tc.id, got)
		}
	}
}

func TestListDocumentsByDatasetsRouteRegistered(t *testing.T) {
	r := mux.NewRouter()
	registerAllRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/documents:listByDatasets", nil)
	var match mux.RouteMatch
	if !r.Match(req, &match) {
		t.Fatalf("expected listByDatasets route to match")
	}

	gotTemplate, err := match.Route.GetPathTemplate()
	if err != nil {
		t.Fatalf("get matched route template: %v", err)
	}
	if want := "/documents:listByDatasets"; gotTemplate != want {
		t.Fatalf("expected template %q, got %q", want, gotTemplate)
	}
}

func TestToolDisableRouteRegistered(t *testing.T) {
	r := mux.NewRouter()
	registerAllRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/tools/bing:disable", nil)
	var match mux.RouteMatch
	if !r.Match(req, &match) {
		t.Fatalf("expected tool disable route to match")
	}

	gotTemplate, err := match.Route.GetPathTemplate()
	if err != nil {
		t.Fatalf("get matched route template: %v", err)
	}
	if want := "/tools/{tool_name}:disable"; gotTemplate != want {
		t.Fatalf("expected template %q, got %q", want, gotTemplate)
	}
	if gotName := match.Vars["tool_name"]; gotName != "bing" {
		t.Fatalf("expected tool_name %q, got %q", "bing", gotName)
	}
}
