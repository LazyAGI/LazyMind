package workflow

import (
	"net/http"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

func TestPausedRerunAdmission(t *testing.T) {
	for _, scenario := range []string{"rewind", "retry", "ordinary", "automatic", "stale", "approval", "unknown", "foreign-turn", "pausing"} {
		t.Run(scenario, func(t *testing.T) {
			graph := graphengine.CompiledStateGraph{StartRoute: "all", Nodes: map[string]graphengine.CompiledNode{
				"draft": {ID: "draft", Mode: "auto"}, "next": {ID: "next", Mode: "auto"},
			}, ControlEdges: []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "draft"}, {ID: "next", From: "draft", To: "next"}, {ID: "end", From: "next", To: "__end__"}}}
			if scenario == "approval" {
				graph.Nodes["draft"] = graphengine.CompiledNode{ID: "draft", Mode: "human"}
			}
			db, session := newControllerTestSession(t, "native", graph)
			oldDB, oldLLM, oldState := store.DB(), store.LazyLLMDB(), store.State()
			store.Init(db.DB, db.DB, nil)
			t.Cleanup(func() { store.Init(oldDB, oldLLM, oldState) })
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().UTC()
			status := "succeeded"
			if scenario == "retry" {
				status = "interrupted"
			}
			must(db.Create(&orm.WorkflowSessionStep{ID: "old", TaskID: "old-task", SessionID: session.ID, StepID: "draft", Attempt: 1, Status: status, Validity: "effective", CreatedAt: now.Add(-time.Minute), ReviewRequired: scenario == "approval"}).Error)
			runStatus := "waiting"
			if scenario == "pausing" {
				runStatus = "pausing"
			}
			must(db.Model(&session).Updates(map[string]any{"status": runStatus, "last_stopped_at": now}).Error)
			must(db.Create(&orm.ChatHistory{ID: "rerun-turn", ConversationID: session.ConversationID}).Error)
			req := transitionCommandRequest{CommandID: "rerun", Operation: "advance", RetryOrigin: "user", ResumeForRerun: true, TargetStepID: "draft", ExpectedStateVersion: session.StateVersion, TriggerHistoryID: "rerun-turn"}
			want := ""
			switch scenario {
			case "ordinary":
				req.ResumeForRerun = false
				want = "WORKFLOW_PAUSED"
			case "automatic":
				req.RetryOrigin = "automatic"
				want = "INVALID_RERUN"
			case "stale":
				req.ExpectedStateVersion--
				want = "STATE_VERSION_CONFLICT"
			case "approval":
				want = "WORKFLOW_APPROVAL_REQUIRED"
			case "unknown":
				req.TargetStepID = "missing"
				want = "INVALID_RERUN"
			case "foreign-turn":
				req.TriggerHistoryID = "foreign"
				want = "INVALID_RERUN"
			case "pausing":
				want = "WORKFLOW_NOT_PAUSED"
			}
			result, code := transitionWorkflowSession(t.Context(), db.DB, session.ID, req)
			must(db.First(&session, "id = ?", session.ID).Error)
			var attempts []orm.WorkflowSessionStep
			must(db.Where("session_id = ?", session.ID).Order("attempt").Find(&attempts).Error)
			if want != "" {
				if code != http.StatusConflict || result.Error == nil || result.Error.Code != want {
					t.Fatalf("result=%+v error=%+v code=%d want=%s", result, result.Error, code, want)
				}
				if session.Status != runStatus || len(attempts) != 1 || attempts[0].Validity != "effective" {
					t.Fatalf("rejected rerun mutated session=%+v attempts=%+v", session, attempts)
				}
				return
			}
			if code != http.StatusOK || !result.Accepted || len(attempts) != 2 || attempts[0].Validity != "stale" || attempts[1].StepID != "draft" {
				t.Fatalf("result=%+v code=%d attempts=%+v", result, code, attempts)
			}
			view, err := displaystate.Read(db.DB, session)
			must(err)
			if view.Status != "running" || view.ChatHistoryID != "rerun-turn" {
				t.Fatalf("view=%+v", view)
			}
			// The one-shot resume does not change approval settings or later pause admission.
			must(db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", attempts[1].ID).Update("status", "interrupted").Error)
			must(db.Model(&session).Updates(map[string]any{"status": "waiting", "last_stopped_at": time.Now().UTC()}).Error)
			expectControlCode(t, guardRunAdvance(db.DB, session), "WORKFLOW_PAUSED")
		})
	}
}
