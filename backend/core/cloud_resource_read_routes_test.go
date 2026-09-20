package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

func TestDesktopCloudResourceReadRoutes(t *testing.T) {
	t.Setenv("LAZYMIND_CLOUD_BASE_URL", "https://cloud.example")
	router := mux.NewRouter()
	router.UseEncodedPath()
	registerAllRoutes(router)
	for _, path := range []string{
		"/cloud/skills/fixture-id", "/cloud/skills/fixture-id/tree", "/cloud/skills/fixture-id/content?path=SKILL.md",
		"/cloud/workflows/fixture-id", "/cloud/workflows/fixture-id/tree", "/cloud/workflows/fixture-id/content?path=workflow.yaml",
		"/cloud/knowledge-market?cursor=next", "/cloud/knowledge-market/items/fixture-catalog",
	} {
		t.Run(path, func(t *testing.T) {
			var match mux.RouteMatch
			if !router.Match(httptest.NewRequest(http.MethodGet, path, nil), &match) {
				t.Fatalf("Desktop read route not mounted: %s", path)
			}
			if path != "/cloud/knowledge-market?cursor=next" && match.Vars["resource_id"] == "" && match.Vars["catalog_key"] == "" {
				t.Fatalf("read route does not identify its Cloud resource: %v", match.Vars)
			}
		})
	}
}
