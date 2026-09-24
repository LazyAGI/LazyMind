package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

func TestCloudSkillUploadRouteUsesLocalSkillIdentity(t *testing.T) {
	t.Setenv("LAZYMIND_CLOUD_BASE_URL", "https://cloud.example")
	router := mux.NewRouter()
	router.UseEncodedPath()
	registerAllRoutes(router)

	request := httptest.NewRequest(http.MethodPost, "/cloud/skills/local-skill-1:upload", nil)
	var match mux.RouteMatch
	if !router.Match(request, &match) {
		t.Fatal("POST /cloud/skills/{skill_id}:upload is not mounted")
	}
	if got := match.Vars["skill_id"]; got != "local-skill-1" {
		t.Fatalf("skill_id = %q, want local-skill-1", got)
	}
	want := "/cloud/skills/{skill_id}:upload"
	if got, err := match.Route.GetPathTemplate(); err != nil || got != want {
		t.Fatalf("route template = %q, err=%v; want %q", got, err, want)
	}
}
