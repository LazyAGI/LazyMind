package stream

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	workflowstore "lazymind/core/workflow/store"
)

type reviewRecorder struct {
	*httptest.ResponseRecorder
	onHeartbeat func()
}

func (w *reviewRecorder) Flush() {
	w.ResponseRecorder.Flush()
	if w.onHeartbeat != nil && strings.Contains(w.Body.String(), ": heartbeat") {
		callback := w.onHeartbeat
		w.onHeartbeat = nil
		callback()
	}
}

func TestCrossProcessReplayPreservesPersistedOrder(t *testing.T) {
	for _, withLocalEvent := range []bool{false, true} {
		name := "remote_only"
		if withLocalEvent {
			name = "remote_then_local"
		}
		t.Run(name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			local, remote := workflowstore.New(db), workflowstore.New(db)
			if err := local.AutoMigrate(); err != nil {
				t.Fatal(err)
			}
			appendEvent := func(repo *workflowstore.Repository, entity string) {
				t.Helper()
				if err := repo.AppendEvent(context.Background(), &workflowstore.Event{
					SessionID: "s1", OwnerUserID: "u1", EventType: "workflow.patch",
					EntityID: entity, PayloadJSON: json.RawMessage(`{}`),
				}); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
			defer cancel()
			req := mux.SetURLVars(httptest.NewRequest("GET", "/workflow-sessions/s1/events", nil).WithContext(ctx), map[string]string{"session_id": "s1"})
			req.Header.Set("X-User-Id", "u1")
			w := &reviewRecorder{ResponseRecorder: httptest.NewRecorder()}
			w.onHeartbeat = func() {
				appendEvent(remote, "remote-event")
				if withLocalEvent {
					appendEvent(local, "local-event")
				}
			}
			Handler{Store: local, Heartbeat: 5 * time.Millisecond, PollInterval: 50 * time.Millisecond}.ServeHTTP(w, req)
			if !strings.Contains(w.Body.String(), "remote-event") {
				t.Fatalf("persisted cross-process event was skipped: %s", w.Body.String())
			}
		})
	}
}
