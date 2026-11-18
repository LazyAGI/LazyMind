package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestScanNativeRoutesReachCoreValidationWithoutRAG(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "false")
	db := orm.OpenTestDB(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.Document{}); err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	router := mux.NewRouter()
	registerAllRoutes(router)
	// These calls intentionally have no identity/data. The routing contract is
	// that Core validation runs; the doc package tests exercise authorized writes.
	for _, tc := range []struct{ method, path, template string }{
		{http.MethodPatch, "/datasets/native", "/datasets/{dataset}"},
		{http.MethodDelete, "/datasets/native", "/datasets/{dataset}"},
		{http.MethodPatch, "/datasets/native/documents/document", "/datasets/{dataset}/documents/{document}"},
		{http.MethodDelete, "/datasets/native/documents/document", "/datasets/{dataset}/documents/{document}"},
		{http.MethodPost, "/datasets/native:batchDelete", "/datasets/{dataset}:batchDelete"},
		{http.MethodPost, "/datasets/native/tasks/task:resume", "/datasets/{dataset}/tasks/{task}:resume"},
		{http.MethodPost, "/datasets/native/members:search", "/datasets/{dataset}/members:search"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, nil)
			var match mux.RouteMatch
			if !router.Match(request, &match) {
				t.Fatal("native route is not registered")
			}
			if got, err := match.Route.GetPathTemplate(); err != nil || got != tc.template {
				t.Fatalf("wrong native route: template=%q err=%v", got, err)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, request)
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("native operation did not reach Core validation: status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	blocked := httptest.NewRecorder()
	router.ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/dataset/algos", nil))
	if blocked.Code != http.StatusServiceUnavailable {
		t.Fatalf("algorithm capability lost its RAG gate: %d", blocked.Code)
	}
}
