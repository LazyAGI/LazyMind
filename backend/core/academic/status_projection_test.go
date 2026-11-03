package academic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/workflow"
	"lazymind/core/workflow/graphengine"
)

func TestExpansionStatusFollowsWorkflowLifecycle(t *testing.T) {
	for _, scenario := range []string{"failure", "timeout", "pause while executing", "pause before dispatch"} {
		t.Run(scenario, func(t *testing.T) {
			db := paperFixture(t)
			conversation, err := newAcademicConversation(db.DB, "u", "papers", "research", "expansion")
			if err != nil {
				t.Fatal(err)
			}
			record := orm.AcademicExpansion{ID: "record", UserID: "u", RequestOwner: "u", RequestKey: "launch", DatasetID: "papers",
				ConversationID: conversation, Goal: "expansion", Status: "running", Version: 1,
				ResultJSON: marshal(ReviewWorkflowInput{UserInstruction: "research", Papers: []WorkflowPaper{}}),
				ReportJSON: marshal(map[string]string{"narrative": "saved report"})}
			if err := db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			session := orm.WorkflowSession{ID: "session", ConversationID: conversation, WorkflowID: "paper_expansion",
				CreateUserID: "u", Status: "active", ControllerHost: "lazymind"}
			if err := db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			if scenario != "pause before dispatch" {
				if _, err := subagent.CreateTask(t.Context(), db.DB, subagent.CreateTaskInput{
					TaskID: "task", ConversationID: conversation, AgentType: "workflow_step", CreateUserID: "u",
				}); err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&orm.WorkflowSessionStep{ID: "attempt", SessionID: session.ID, StepID: "analyze_relations",
					TaskID: "task", Status: "running", Validity: "effective", Attempt: 1, LeaseToken: "lease"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			runner := asyncjob.Start(ctx, db.DB, asyncjob.Options{JobTypes: []string{"academic.project"}, Concurrency: 1, PollInterval: 10 * time.Millisecond})
			t.Cleanup(func() { cancel(); <-runner.Done() })
			var processed int64
			assertProjected := func(want string) {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for {
					var actual orm.AcademicExpansion
					if err := db.Where("id=?", record.ID).Take(&actual).Error; err != nil {
						t.Fatal(err)
					}
					var jobs []orm.AsyncJob
					if err := db.Where("job_type=? AND resource_id=?", "academic.project", record.ID).Find(&jobs).Error; err != nil {
						t.Fatal(err)
					}
					var succeeded int64
					for _, job := range jobs {
						if job.Status == "failed" {
							t.Fatalf("projection failed: %s", job.ErrorMessage)
						}
						if job.Status == "succeeded" {
							succeeded++
						}
					}
					if actual.Status == want && succeeded > processed && succeeded == int64(len(jobs)) {
						processed = succeeded
						if actual.Version != record.Version || string(actual.ResultJSON) != string(record.ResultJSON) || string(actual.ReportJSON) != string(record.ReportJSON) {
							t.Fatal("status synchronization changed saved research results")
						}
						r := paperRequest("")
						r.URL.RawQuery = url.Values{"conversation_id": {conversation}, "view": {"summary"}}.Encode()
						w := httptest.NewRecorder()
						AcademicChatContext(w, r)
						var summary academicChatSummary
						if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil || w.Code != http.StatusOK || summary.Status != want {
							t.Fatalf("chat summary status=%s, want=%s: %d %s %v", summary.Status, want, w.Code, w.Body.String(), err)
						}
						return
					}
					if time.Now().After(deadline) {
						t.Fatalf("status=%s, want=%s; completed projections=%d, previously=%d, total=%d", actual.Status, want, succeeded, processed, len(jobs))
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if scenario == "failure" || scenario == "timeout" {
				message := "model request failed"
				if scenario == "timeout" {
					message = "WORKFLOW_STEP_TIMEOUT: analyze_relations exceeded its declared timeout"
				}
				workflow.OnSubAgentDone(t.Context(), db.DB, store.State(), "task", subagent.StatusFailed, message,
					func(string, map[string]any) {}, &workflow.WorkflowChatContext{
						SessionID: session.ID, StepID: "analyze_relations", WorkflowID: "paper_expansion", ConvID: conversation, WorkflowMode: "dynamic",
					})
				assertProjected("failed")
				return
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)
			r := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/workflow-sessions/session:pause", nil), map[string]string{"session_id": session.ID})
			r.Header.Set("X-User-Id", "u")
			r.Header.Set("Origin", "http://localhost")
			w := httptest.NewRecorder()
			workflow.PauseNativeWorkflowHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("pause failed: %d %s", w.Code, w.Body.String())
			}
			if scenario == "pause while executing" {
				// Drain the pause-request projection before acknowledging shutdown,
				// so it cannot accidentally hide a missing final-stop notification.
				assertProjected("running")
				if err := workflow.ConfirmNativeStop(t.Context(), db.DB, "attempt", "lease"); err != nil {
					t.Fatal(err)
				}
			}
			assertProjected("stopped")
			current, err := workflow.GetSession(t.Context(), db.DB, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := workflow.BeginNativeControlTurn(t.Context(), db.DB, "u", conversation, workflow.NativeControlCommand{
				SessionID: session.ID, Kind: "resume", ExpectedStateVersion: current.StateVersion, HistoryID: "resume-turn",
			}); err != nil {
				t.Fatal(err)
			}
			assertProjected("running")
		})
	}
}

func TestExpansionProjectionUsesCurrentWorkflowState(t *testing.T) {
	for _, sample := range []struct {
		name, session, step, mode, want string
		question, stopped               bool
	}{
		{name: "resumed execution", session: "active", step: "running", stopped: true, want: "running"},
		{name: "current question", session: "active", question: true, want: "awaiting_input"},
		{name: "pause retains old question", session: "waiting", question: true, stopped: true, want: "stopped"},
		{name: "completion supersedes stop and question", session: "completed", step: "succeeded", question: true, stopped: true, want: "completed"},
		{name: "failure supersedes stop and question", session: "failed", step: "failed", question: true, stopped: true, want: "failed"},
		{name: "approval after resume", session: "waiting", step: "succeeded", mode: "human", stopped: true, want: "awaiting_input"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := paperFixture(t)
			if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}); err != nil {
				t.Fatal(err)
			}
			conversation, err := newAcademicConversation(db.DB, "u", "papers", "research", "expansion")
			if err != nil {
				t.Fatal(err)
			}
			record := orm.AcademicExpansion{ID: "record", UserID: "u", RequestOwner: "u", RequestKey: "launch", DatasetID: "papers",
				ConversationID: conversation, Goal: "expansion", Status: "running", Version: 1,
				ResultJSON: marshal(ReviewWorkflowInput{UserInstruction: "research", Papers: []WorkflowPaper{}}),
				ReportJSON: marshal(map[string]string{"narrative": "saved report"})}
			if err = db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			result, report := string(record.ResultJSON), string(record.ReportJSON)
			stoppedAt := time.Now().UTC().Add(-time.Minute)
			session := orm.WorkflowSession{ID: "session", ConversationID: conversation, WorkflowID: "paper_expansion", CreateUserID: "u",
				Status: sample.session, ControllerHost: "lazymind", WorkflowRevisionID: "revision"}
			if sample.stopped {
				session.LastStoppedAt = &stoppedAt
			}
			graph := graphengine.CompiledStateGraph{Nodes: map[string]graphengine.CompiledNode{"analyze_relations": {ID: "analyze_relations", Mode: sample.mode}}}
			if err = db.Create(&orm.WorkflowRevision{ID: "revision", WorkflowResourceID: "resource", RevisionNo: 1, CompiledGraph: graph.JSON()}).Error; err != nil {
				t.Fatal(err)
			}
			if err = db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			if sample.step != "" {
				if err = db.Create(&orm.WorkflowSessionStep{ID: "attempt", SessionID: session.ID, StepID: "analyze_relations", TaskID: "task",
					Status: sample.step, Attempt: 1, Validity: "effective", CreatedAt: stoppedAt.Add(time.Second)}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if sample.question {
				if err = db.Create(&orm.ChatHistory{ID: "question", ConversationID: conversation, Seq: 1,
					Ext: marshal(map[string]any{"ask_pending": []string{"scope?"}})}).Error; err != nil {
					t.Fatal(err)
				}
			}
			job := asyncjob.Job{ResourceID: record.ID, CreateUserID: "u", PayloadJSON: marshal(expansionProjectionPayload{SessionID: session.ID})}
			if _, err = projectAcademicResult(t.Context(), job, nil); err != nil {
				t.Fatal(err)
			}
			if err = db.Where("id=?", record.ID).Take(&record).Error; err != nil {
				t.Fatal(err)
			}
			if record.Status != sample.want {
				t.Fatalf("status=%q want %q", record.Status, sample.want)
			}
			if record.Version != 1 || string(record.ResultJSON) != result || string(record.ReportJSON) != report {
				t.Fatal("status projection changed saved results")
			}
		})
	}
}
