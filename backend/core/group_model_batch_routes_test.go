package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

func TestBatchModelImportRouteUsesBatchAction(t *testing.T) {
	router := mux.NewRouter()
	registerAllRoutes(router)
	request := httptest.NewRequest(http.MethodPost, "/model_providers/supplier/groups/connection/models:batch", nil)
	var match mux.RouteMatch
	if !router.Match(request, &match) {
		t.Fatal("batch model import route is not mounted")
	}
	want := "/model_providers/{model_provider_id}/groups/{group_id}/models:batch"
	if path, err := match.Route.GetPathTemplate(); err != nil || path != want {
		t.Fatalf("batch import matched another action: %s %v", path, err)
	}
}
