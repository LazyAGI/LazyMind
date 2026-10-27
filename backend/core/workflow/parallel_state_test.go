package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/subagent"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

func TestParallelApprovalIsNotHiddenByLaterSibling(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting", true: "paused"}[stopped], func(t *testing.T) {
			db, _ := humanCheckpoint(t, false)
			now := time.Now().UTC()
			if err := db.Create(&orm.WorkflowSessionStep{ID: "auto-sibling", SessionID: "batch-session", StepID: "branch_c", TaskID: "auto-task", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if stopped {
				if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("last_stopped_at", now.Add(time.Second)).Error; err != nil {
					t.Fatal(err)
				}
			}
			s, err := GetSession(t.Context(), db.DB, "batch-session")
			if err != nil {
				t.Fatal(err)
			}
			view, err := displaystate.Read(db.DB, *s)
			if err != nil || view.Status != "waiting" || view.ApprovalStepID != "branch_b" {
				t.Fatalf("unapproved human sibling hidden: %+v %v", view, err)
			}
			if _, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", NativeControlCommand{SessionID: s.ID, Kind: "resume", ExpectedStateVersion: s.StateVersion}); err == nil {
				t.Fatal("resume bypassed parallel approval")
			}
		})
	}
}

func TestParallelApprovalsAreAcceptedIndividually(t *testing.T) {
	db, hash := humanCheckpoint(t, false)
	var revision orm.WorkflowRevision
	if err := db.First(&revision, "id = ?", "batch-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes["branch_c"]
	node.Mode = "human"
	graph.Nodes["branch_c"] = node
	if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: "second-review", SessionID: "batch-session", StepID: "branch_c", TaskID: "second-review-task", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	s, err := GetSession(t.Context(), db.DB, "batch-session")
	if err != nil {
		t.Fatal(err)
	}
	cmd := NativeControlCommand{SessionID: s.ID, Kind: "approve", StepID: "branch_c", ExpectedStateVersion: s.StateVersion, HistoryID: "approval-turn"}
	version, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := FinishNativeControlTurn(t.Context(), db.DB, s.ID, version); err != nil {
		t.Fatal(err)
	}
	s, err = GetSession(t.Context(), db.DB, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := displaystate.Read(db.DB, *s)
	if err != nil || s.Status != "waiting" || view.ApprovalStepID != "branch_b" {
		t.Fatalf("first approval released another checkpoint: %+v %+v %v", s, view, err)
	}
	rec, _ := runBatchTransition(t, db, hash, "execute", []map[string]any{{"target_step_id": "branch_c", "task_id": "must-not-run"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("Chat bypassed remaining approval: %s", rec.Body.String())
	}
	cmd.StepID, cmd.ExpectedStateVersion = "branch_b", s.StateVersion
	if _, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", cmd); err != nil {
		t.Fatal(err)
	}
	s, err = GetSession(t.Context(), db.DB, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err = displaystate.Read(db.DB, *s)
	if err != nil || view.ApprovalStepID != "" {
		t.Fatalf("accepted approval reappeared: %+v %v", view, err)
	}
}

func TestPauseAfterOneParallelBranchFailed(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()
	if _, err := CreateSession(ctx, db.DB, CreateSessionInput{SessionID: "parallel", ConversationID: "chat", CreateUserID: "owner", WorkflowID: "test", TriggerHistoryID: "turn"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := subagent.CreateTask(ctx, db.DB, subagent.CreateTaskInput{TaskID: id, ConversationID: "chat", CreateUserID: "owner", AgentType: "workflow_step", TriggerHistoryID: "turn"}); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowSessionStep{ID: id, SessionID: "parallel", StepID: id, TaskID: id, Status: "running", Validity: "effective", LeaseToken: "lease-" + id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	OnSubAgentDone(ctx, db.DB, nil, "a", subagent.StatusFailed, "tool failed", func(string, map[string]any) {}, &WorkflowChatContext{SessionID: "parallel", StepID: "a", ConvID: "chat", WorkflowMode: "dynamic"})
	notified := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { notified++; w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)
	if err := StopWorkflowTurn(ctx, db.DB, nil, "owner", "chat", "turn"); err != nil {
		t.Fatal(err)
	}
	session, err := GetSession(ctx, db.DB, "parallel")
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != "pausing" || notified != 1 {
		t.Fatalf("running sibling was not paused: status=%s notifications=%d", session.Status, notified)
	}
	if err := ConfirmNativeStop(ctx, db.DB, "b", "lease-b"); err != nil {
		t.Fatal(err)
	}
	session, err = GetSession(ctx, db.DB, "parallel")
	if err != nil {
		t.Fatal(err)
	}
	view, err := displaystate.Read(db.DB, *session)
	if err != nil || view.Status != "paused" {
		t.Fatalf("pause not confirmed: %+v %v", view, err)
	}
	var failed orm.WorkflowSessionStep
	if err := db.First(&failed, "id = ?", "a").Error; err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" {
		t.Fatalf("pause erased sibling failure: %+v", failed)
	}
	// Retry's Chat request must release the pause explicitly; preserve the failed
	// checkpoint for the driver to retry instead of silently accepting plain text.
	if _, err := BeginNativeControlTurn(ctx, db.DB, "owner", "chat", NativeControlCommand{
		SessionID: session.ID, Kind: "resume", ExpectedStateVersion: session.StateVersion, HistoryID: "retry-turn",
	}); err != nil {
		t.Fatal(err)
	}
	session, err = GetSession(ctx, db.DB, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err = displaystate.Read(db.DB, *session)
	if err != nil || view.Status != "running" {
		t.Fatalf("retry could not release pause: %+v %v", view, err)
	}
	bound, err := displaystate.ChatHistoryID(*session)
	if err != nil || bound != "retry-turn" {
		t.Fatalf("retry did not rebind pause ownership: %s %v", bound, err)
	}
}

func TestChatStopRejectsUnrelatedWorkflowAndTracksResumeTurn(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	s := orm.WorkflowSession{ID: "background", ConversationID: "chat", CreateUserID: "owner", WorkflowID: "test", ControllerHost: "lazymind", Status: "active", TriggerHistoryID: "original-turn"}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	if err := StopWorkflowTurn(t.Context(), db.DB, nil, "owner", "chat", "unrelated-turn"); err != nil {
		t.Fatal(err)
	}
	actual, err := GetSession(t.Context(), db.DB, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Status != "active" {
		t.Fatalf("ordinary Chat stopped background workflow: %s", actual.Status)
	}
	if err := db.Model(&s).Updates(map[string]any{"status": "waiting", "last_stopped_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := BeginNativeControlTurn(t.Context(), db.DB, "owner", "chat", NativeControlCommand{SessionID: s.ID, Kind: "resume", ExpectedStateVersion: s.StateVersion, HistoryID: "resume-turn"}); err != nil {
		t.Fatal(err)
	}
	if err := StopWorkflowTurn(t.Context(), db.DB, nil, "owner", "chat", "original-turn"); err != nil {
		t.Fatal(err)
	}
	actual, err = GetSession(t.Context(), db.DB, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Status != "active" {
		t.Fatal("stale original turn cancelled resumed workflow")
	}
	if err := StopWorkflowTurn(t.Context(), db.DB, nil, "owner", "chat", "resume-turn"); err != nil {
		t.Fatal(err)
	}
	actual, err = GetSession(t.Context(), db.DB, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := displaystate.Read(db.DB, *actual)
	if err != nil || view.Status != "paused" || actual.TriggerHistoryID != "original-turn" {
		t.Fatalf("resume binding lost: %+v %+v %v", actual, view, err)
	}
}
