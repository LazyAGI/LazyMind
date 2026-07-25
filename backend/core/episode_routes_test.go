package main

import (
	"testing"

	"github.com/gorilla/mux"
)

func TestEpisodeMemoryRoutesAreRegistered(t *testing.T) {
	router := mux.NewRouter()
	registerCoreRoutes(router)
	registered, err := collectRouteMethodsForTest(router)
	if err != nil {
		t.Fatalf("collect routes: %v", err)
	}
	for path, method := range map[string]string{
		"/memory/episodes":                           "GET",
		"/memory/episodes/{episode_id}":              "GET",
		"/memory/episodes/{episode_id}|delete":       "DELETE",
		"/internal/memory/episodes":                  "POST",
		"/internal/memory/episodes/{episode_id}":     "DELETE",
		"/internal/memory/episodes:searchCandidates": "POST",
		"/internal/memory/episodes:listRecent":       "POST",
		"/internal/memory/episodes|conversation":     "GET",
		"/internal/memory/episodes:recordHits":       "POST",
	} {
		routePath := path
		if routePath == "/memory/episodes/{episode_id}|delete" {
			routePath = "/memory/episodes/{episode_id}"
		}
		if routePath == "/internal/memory/episodes|conversation" {
			routePath = "/internal/memory/episodes"
		}
		if _, ok := registered[routePath][method]; !ok {
			t.Fatalf("missing route %s %s", method, routePath)
		}
	}
}

func collectRouteMethodsForTest(router *mux.Router) (map[string]map[string]struct{}, error) {
	registered := make(map[string]map[string]struct{})
	err := router.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		path, pathErr := route.GetPathTemplate()
		if pathErr != nil || path == "" {
			return nil
		}
		methods, methodsErr := route.GetMethods()
		if methodsErr != nil {
			return nil
		}
		if registered[path] == nil {
			registered[path] = make(map[string]struct{})
		}
		for _, method := range methods {
			registered[path][method] = struct{}{}
		}
		return nil
	})
	return registered, err
}
