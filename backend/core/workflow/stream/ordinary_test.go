package stream

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
)

func TestOrdinaryStreamReplacesPrivateReplayWithPublicSnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := workflowstore.New(db)
	if err := repo.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSession{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSession{ID: "s", CreateUserID: "owner"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendEvent(t.Context(), &workflowstore.Event{SessionID: "s", OwnerUserID: "owner", EventType: "workflow.snapshot", PayloadJSON: json.RawMessage(`{"prompt":"SECRET","graph":{"private":"SECRET"}}`)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := mux.SetURLVars(httptest.NewRequest("GET", "/events?view=ordinary", nil).WithContext(ctx), map[string]string{"session_id": "s"})
	req.Header.Set("X-User-Id", "owner")
	req.Header.Set("Last-Event-ID", "999")
	recorder := httptest.NewRecorder()
	Handler{Store: repo, Snapshot: func(_ *http.Request, _, _ string) (any, error) {
		cancel()
		return map[string]any{"schema_version": 1, "tasks": []any{}}, nil
	}}.ServeHTTP(recorder, req)
	body := recorder.Body.String()
	if !strings.Contains(body, "event: snapshot") || !strings.Contains(body, "event: resync_required") || strings.Contains(body, "SECRET") || strings.Contains(body, "event: workflow.snapshot") {
		t.Fatalf("unsafe ordinary replay: %s", body)
	}
}

func TestOrdinaryStreamAuthorizesResumeAndMasksProjectionErrors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := workflowstore.New(db)
	if err := repo.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSession{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSession{ID: "s", CreateUserID: "owner"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"intruder", "owner"} {
		req := mux.SetURLVars(httptest.NewRequest("GET", "/events?view=ordinary", nil), map[string]string{"session_id": "s"})
		req.Header.Set("X-User-Id", owner)
		req.Header.Set("Last-Event-ID", "1")
		recorder := httptest.NewRecorder()
		Handler{Store: repo, Snapshot: func(_ *http.Request, _, _ string) (any, error) { return nil, errors.New("SECRET SQL internal path") }}.ServeHTTP(recorder, req)
		requestID := recorder.Header().Get("X-Request-ID")
		if requestID == "" || !strings.Contains(recorder.Body.String(), requestID) {
			t.Fatal("ordinary error is missing its request ID")
		}
		if strings.Contains(recorder.Body.String(), "SECRET") {
			t.Fatal("raw exception leaked")
		}
		if owner == "intruder" && recorder.Code != http.StatusNotFound {
			t.Fatalf("resume authorization: %d", recorder.Code)
		}
		if owner == "owner" && !strings.Contains(recorder.Body.String(), "PUBLIC_PROJECTION_UNAVAILABLE") {
			t.Fatal("missing stable error")
		}
	}
}
