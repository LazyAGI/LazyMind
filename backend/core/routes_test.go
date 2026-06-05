package main

import (
	"net/http"
	"net/http/httptest"
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
