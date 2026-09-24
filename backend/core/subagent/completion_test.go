package subagent

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/state"
	"lazymind/core/store"
)

func TestCompletionFailureSurvivesStatusCacheAndDatabaseFallback(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	ss, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	store.Init(db.DB, nil, ss)
	seedSubagentTask(t, db, "incomplete", "conversation", "owner", StatusRunning)
	ev := TaskEvent{
		Type: "error", TaskID: "incomplete", Status: StatusFailed,
		CurrentPhase: "missing_required_artifacts", Message: "Required document was not delivered.",
	}
	if err := routeEventWithWorkflowHooks(t.Context(), db.DB, ss, ev, false, false); err != nil {
		t.Fatalf("failure event not accepted: %v", err)
	}
	for _, cached := range []bool{true, false} {
		if !cached {
			store.Init(db.DB, nil, nil)
		}
		req := httptest.NewRequest(http.MethodGet, "/internal/subagent/tasks/incomplete", nil)
		req = mux.SetURLVars(req, map[string]string{"task_id": ev.TaskID})
		rec := httptest.NewRecorder()
		InternalGetTaskStatus(rec, req)
		data := getData(rec.Body.Bytes())
		if data["status"] != StatusFailed || data["current_phase"] != ev.CurrentPhase {
			t.Fatalf("cached=%v: lost structured completion failure: %s", cached, rec.Body.String())
		}
	}
}

func TestCompletionFailureCannotOverwriteUserStop(t *testing.T) {
	for _, stopped := range []string{StatusInterrupted, StatusCanceled} {
		t.Run(stopped, func(t *testing.T) {
			db := newTestDB(t)
			seedSubagentTask(t, db, "stopped", "conversation", "owner", stopped)
			err := routeEventWithWorkflowHooks(t.Context(), db.DB, nil, TaskEvent{
				Type: "error", TaskID: "stopped", Status: StatusFailed,
				CurrentPhase: "objective_incomplete", Message: "Late model verdict",
			}, false, false)
			if err != nil {
				t.Fatalf("route late completion: %v", err)
			}
			task, err := GetTask(t.Context(), db.DB, "stopped")
			if err != nil || task.Status != stopped || task.CurrentPhase != "working" {
				t.Fatalf("late completion changed stopped task: %+v %v", task, err)
			}
		})
	}
}
