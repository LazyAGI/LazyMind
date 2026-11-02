package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/tasktiming"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

func humanCheckpoint(t *testing.T, final bool) (*orm.DB, string) {
	t.Helper()
	db, hash := setupBatchTransitionSession(t)
	var revision orm.WorkflowRevision
	if err := db.First(&revision, "id = ?", "batch-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes["branch_b"]
	node.Mode = "human"
	graph.Nodes["branch_b"] = node
	if final {
		graph.Nodes = map[string]graphengine.CompiledNode{"branch_b": node}
		graph.ControlEdges = []graphengine.CompiledEdge{{ID: "start-b", From: "__start__", To: "branch_b"}, {ID: "b-end", From: "branch_b", To: "__end__"}}
	}
	if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: "review-attempt", SessionID: "batch-session", StepID: "branch_b", TaskID: "review-task", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: time.Now().UTC().Add(-time.Second)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("status", "waiting").Error; err != nil {
		t.Fatal(err)
	}
	return db, hash
}

func TestNativeApprovalRequiresVersionedUserDecision(t *testing.T) {
	db, hash := humanCheckpoint(t, false)
	rec, _ := runBatchTransition(t, db, hash, "execute", []map[string]any{{"target_step_id": "branch_c", "task_id": "ordinary-chat-attempt"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "WORKFLOW_APPROVAL_REQUIRED") {
		t.Fatalf("ordinary Chat bypassed approval: %s", rec.Body.String())
	}
	cmd := NativeControlCommand{SessionID: "batch-session", Kind: "approve", StepID: "branch_b", ExpectedStateVersion: 3}
	if _, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", cmd); err == nil {
		t.Fatal("stale approval accepted")
	}
	cmd.ExpectedStateVersion = 4
	if _, err := BeginNativeControlTurn(t.Context(), db.DB, "other", "batch-conversation", cmd); err == nil {
		t.Fatal("other owner accepted")
	}
	version, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", cmd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", cmd); err == nil {
		t.Fatal("duplicate decision accepted")
	}
	var review orm.WorkflowReviewCheckpoint
	if err := db.First(&review, "attempt_id = ?", "review-attempt").Error; err != nil || review.Status != "accepted" {
		t.Fatalf("approval not durable: %+v %v", review, err)
	}
	if err := FinishNativeControlTurn(t.Context(), db.DB, cmd.SessionID, version); err != nil {
		t.Fatal(err)
	}
	s, _ := GetSession(t.Context(), db.DB, cmd.SessionID)
	view, err := displaystate.Read(db.DB, *s)
	if err != nil || view.Status != "failed" {
		t.Fatalf("failed startup left phantom running: %+v %v", view, err)
	}
	if err := db.Model(s).Updates(map[string]any{"status": "waiting", "last_stopped_at": time.Now().UTC(), "state_version": version + 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err := FinishNativeControlTurn(t.Context(), db.DB, cmd.SessionID, version); err != nil {
		t.Fatal(err)
	}
	s, _ = GetSession(t.Context(), db.DB, cmd.SessionID)
	if s.Status != "waiting" {
		t.Fatal("late turn overwrote pause")
	}
}

func TestHumanApprovalSurvivesPauseAndGraphCompletion(t *testing.T) {
	for _, final := range []bool{false, true} {
		t.Run(map[bool]string{false: "pause", true: "final"}[final], func(t *testing.T) {
			db, _ := humanCheckpoint(t, final)
			if final {
				if err := freezeRouteDecision(t.Context(), db.DB, "batch-session", "branch_b", "review-task"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("last_stopped_at", time.Now().UTC()).Error; err != nil {
					t.Fatal(err)
				}
			}
			s, _ := GetSession(t.Context(), db.DB, "batch-session")
			if err := db.AutoMigrate(&orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{}); err != nil {
				t.Fatal(err)
			}
			cutoff := time.Now().UTC()
			active := *s
			active.Status = "active"
			if err := tasktiming.BindWorkflow(t.Context(), db.DB, active, cutoff.Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := tasktiming.SyncWorkflow(t.Context(), db.DB, s.ID, cutoff); err != nil {
				t.Fatal(err)
			}
			clock, err := tasktiming.Snapshot(t.Context(), db.DB, s.CreateUserID, s.ConversationID, s.ID, cutoff.Add(time.Hour))
			if err != nil || clock.Timing.FinishedAt == nil || *clock.Timing.ElapsedMS != 60000 {
				t.Fatalf("native approval clock did not freeze: %+v %v", clock, err)
			}
			view, err := displaystate.Read(db.DB, *s)
			if err != nil || view.Status != "waiting" {
				t.Fatalf("approval hidden: %+v %v", view, err)
			}
			cmd := NativeControlCommand{SessionID: s.ID, Kind: "resume", ExpectedStateVersion: s.StateVersion}
			if _, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", cmd); err == nil {
				t.Fatal("resume implicitly approved")
			}
			cmd.Kind = "approve"
			cmd.StepID = "branch_b"
			if _, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", cmd); err != nil {
				t.Fatal(err)
			}
			s, _ = GetSession(t.Context(), db.DB, s.ID)
			want := "active"
			if final {
				want = "completed"
			}
			if s.Status != want {
				t.Fatalf("after approval: got %s want %s", s.Status, want)
			}
			clock, err = tasktiming.Snapshot(t.Context(), db.DB, s.CreateUserID, s.ConversationID, s.ID, time.Now().UTC())
			if err != nil || (clock.Timing.FinishedAt != nil) != final || *clock.Timing.ElapsedMS < 60000 ||
				(final && *clock.Timing.ElapsedMS != 60000) {
				t.Fatalf("native approval lost accumulated timing: %+v %v", clock, err)
			}
		})
	}
}

func TestChatPauseScopesWorkflowAndProtectsLateDriverCallbacks(t *testing.T) {
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, id := range []string{"foreground", "background"} {
		if err := db.Create(&orm.WorkflowSession{ID: id, ConversationID: "chat", CreateUserID: "owner", WorkflowID: "workflow", TriggerHistoryID: id, Status: "active"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := StopWorkflowTurn(t.Context(), db.DB, nil, "owner", "chat", "foreground"); err != nil {
		t.Fatal(err)
	}
	if err := failIdleSession(t.Context(), db.DB, "foreground"); err != nil {
		t.Fatal(err)
	}
	if admitted, err := prepareContinuation(t.Context(), db.DB, "foreground"); err != nil || admitted {
		t.Fatal("late driver resumed pause", err)
	}
	for _, id := range []string{"foreground", "background"} {
		s, _ := GetSession(t.Context(), db.DB, id)
		view, err := displaystate.Read(db.DB, *s)
		want := "running"
		if id == "foreground" {
			want = "paused"
		}
		if err != nil || view.Status != want {
			t.Fatalf("%s %+v %v", id, view, err)
		}
	}
	s, _ := GetSession(t.Context(), db.DB, "foreground")
	version, err := BeginNativeControlTurn(t.Context(), db.DB, "owner", "chat", NativeControlCommand{SessionID: s.ID, Kind: "resume", ExpectedStateVersion: s.StateVersion})
	if err != nil {
		t.Fatal(err)
	}
	if err := FinishNativeControlTurn(t.Context(), db.DB, s.ID, version); err != nil {
		t.Fatal(err)
	}
	s, _ = GetSession(t.Context(), db.DB, s.ID)
	view, err := displaystate.Read(db.DB, *s)
	if err != nil || view.Status != "failed" {
		t.Fatalf("old pause masked failed resume: %+v %v", view, err)
	}
}

func TestPauseWaitsForEveryExecutorAndRejectsWrongReceipt(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{SessionID: "pause", ConversationID: "chat", WorkflowID: "test", CreateUserID: "owner"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := subagent.CreateTask(ctx, db.DB, subagent.CreateTaskInput{TaskID: id, ConversationID: "chat", AgentType: "workflow_step", CreateUserID: "owner"}); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowSessionStep{ID: id, TaskID: id, StepID: id, SessionID: "pause", Status: "running", LeaseToken: "lease-" + id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)
	session, err := GetSession(ctx, db.DB, "pause")
	if err != nil {
		t.Fatal(err)
	}
	if err := stopWorkflowSession(ctx, db.DB, nil, session, ""); err == nil {
		t.Fatal("cancellation transport failure was swallowed")
	}
	if requests != 2 {
		t.Fatalf("did not signal every executor: %d", requests)
	}
	assertStatus := func(want string) {
		t.Helper()
		s, _ := GetSession(ctx, db.DB, "pause")
		if s.Status != want {
			t.Fatalf("got %s want %s", s.Status, want)
		}
	}
	assertStatus("pausing")
	assertClock := func(frozen bool) int64 {
		t.Helper()
		// Reading far in the future must not advance a confirmed paused clock.
		run, err := tasktiming.Snapshot(ctx, db.DB, "owner", "chat", "pause", time.Now().UTC().Add(time.Hour))
		if err != nil || run == nil || (run.Timing.FinishedAt != nil) != frozen {
			t.Fatalf("clock frozen=%v: %+v %v", frozen, run, err)
		}
		return *run.Timing.ElapsedMS
	}
	assertClock(false)
	if active, err := GetActiveSession(ctx, db.DB, "chat"); err != nil || active == nil || active.ID != "pause" {
		t.Fatalf("pausing executor disappeared from active sessions: %+v %v", active, err)
	}
	s, _ := GetSession(ctx, db.DB, "pause")
	if _, err := BeginNativeControlTurn(ctx, db.DB, "owner", "chat", NativeControlCommand{SessionID: s.ID, Kind: "resume", ExpectedStateVersion: s.StateVersion}); err == nil {
		t.Fatal("resumed live executor")
	}
	if err := ConfirmNativeStop(ctx, db.DB, "a", "wrong"); err == nil {
		t.Fatal("accepted wrong receipt")
	}
	if err := ConfirmNativeStop(ctx, db.DB, "a", "lease-a"); err != nil {
		t.Fatal(err)
	}
	assertStatus("pausing")
	assertClock(false)
	if err := ConfirmNativeStop(ctx, db.DB, "b", "lease-b"); err != nil {
		t.Fatal(err)
	}
	assertStatus("waiting")
	frozenElapsed := assertClock(true)
	if err := ConfirmNativeStop(ctx, db.DB, "b", "lease-b"); err != nil {
		t.Fatal(err)
	}
	if assertClock(true) != frozenElapsed {
		t.Fatal("duplicate stop acknowledgement advanced the clock")
	}
	s, _ = GetSession(ctx, db.DB, "pause")
	view, err := displaystate.Read(db.DB, *s)
	if err != nil || view.Status != "paused" {
		t.Fatal(view, err)
	}
}
