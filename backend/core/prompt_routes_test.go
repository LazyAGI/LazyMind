package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"lazymind/core/common/orm"
	corestore "lazymind/core/store"

	"github.com/gorilla/mux"
)

func TestPromptActionRoutesAcceptPost(t *testing.T) {
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "prompt-routes.db"))
	if err != nil {
		t.Fatalf("connect prompt route database: %v", err)
	}
	if err := db.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
		t.Fatalf("migrate prompt route database: %v", err)
	}
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })

	router := mux.NewRouter()
	router.UseEncodedPath()
	registerCoreRoutes(router)

	for _, action := range []string{"favorite", "unfavorite", "use"} {
		req := httptest.NewRequest(
			http.MethodPost,
			"/prompts/preset-general-qa:"+action,
			nil,
		)
		req.Header.Set("X-User-Id", "u-route-test")
		req.Header.Set("X-User-Name", "Route Tester")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST :%s matched wrong route: status=%d body=%s", action, rec.Code, rec.Body.String())
		}
	}
}
