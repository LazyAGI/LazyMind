package stream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	workflowstore "lazymind/core/workflow/store"
)

func TestStreamSendsSnapshotAndReplaysAfterLastEventID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:stream?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := workflowstore.New(db)
	if err := repo.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"attempt.running", "artifact.saved"} {
		if err := repo.AppendEvent(context.Background(), &workflowstore.Event{SessionID: "s1", OwnerUserID: "u1", EventType: typ, PayloadJSON: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/workflow-sessions/s1/events", nil).WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"session_id": "s1"})
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("Last-Event-ID", "1")
	recorder := httptest.NewRecorder()
	Handler{Store: repo, Snapshot: func(_ *http.Request, _, _ string) (any, error) { return map[string]any{"state_version": 2}, nil }, Heartbeat: 5 * time.Millisecond}.ServeHTTP(recorder, req)
	body := recorder.Body.String()
	if strings.Contains(body, "event: snapshot") {
		t.Fatalf("resume must not resend snapshot: %s", body)
	}
	if !strings.Contains(body, "id: 2") || !strings.Contains(body, "event: artifact.saved") {
		t.Fatalf("missing replay: %s", body)
	}
	if recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type: %s", recorder.Header().Get("Content-Type"))
	}
}

func TestInitialStreamSnapshotStartsAtLatestCursorWithoutHistoricalReplay(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:stream-initial?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := workflowstore.New(db)
	if err := repo.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"workflow.snapshot", "workflow.patch"} {
		if err := repo.AppendEvent(context.Background(), &workflowstore.Event{SessionID: "s1", OwnerUserID: "u1", EventType: typ, PayloadJSON: json.RawMessage(`{"status":"active"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/workflow-sessions/s1/events", nil).WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"session_id": "s1"})
	req.Header.Set("X-User-Id", "u1")
	recorder := httptest.NewRecorder()
	Handler{Store: repo, Snapshot: func(_ *http.Request, _, _ string) (any, error) {
		return map[string]any{"state_version": 3, "status": "failed"}, nil
	}, Heartbeat: 5 * time.Millisecond}.ServeHTTP(recorder, req)
	body := recorder.Body.String()
	if !strings.Contains(body, "id: 2\nevent: snapshot") || !strings.Contains(body, `"status":"failed"`) {
		t.Fatalf("current snapshot missing latest cursor: %s", body)
	}
	if strings.Contains(body, "event: workflow.snapshot") || strings.Contains(body, "event: workflow.patch") {
		t.Fatalf("initial stream replayed historical status over current snapshot: %s", body)
	}
}
