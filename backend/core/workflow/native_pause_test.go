package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

func TestNativeSessionPauseIsScopedAndWaitsForExecutor(t *testing.T) {
	for _, target := range []string{"foreground", "background"} {
		t.Run(target, func(t *testing.T) {
			db := newTestDB(t)
			store.Init(db.DB, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			for _, id := range []string{"foreground", "background"} {
				if err := db.Create(&orm.WorkflowSession{
					ID: id, ConversationID: "chat", WorkflowID: "test", CreateUserID: "owner", Status: "active", ControllerHost: "lazymind", TriggerHistoryID: id + "-turn",
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := subagent.CreateTask(t.Context(), db.DB, subagent.CreateTaskInput{
				TaskID: "target-task", ConversationID: "chat", AgentType: "workflow_step", CreateUserID: "owner",
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowSessionStep{ID: "attempt", TaskID: "target-task", StepID: "write", SessionID: target, Status: "running", LeaseToken: "lease"}).Error; err != nil {
				t.Fatal(err)
			}
			var cancelled []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/workflow/task-cancel" {
					t.Errorf("workflow pause must not cancel Chat: %s", r.URL.Path)
				}
				var payload struct {
					TaskID string `json:"task_id"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				cancelled = append(cancelled, payload.TaskID)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", srv.URL)
			pause := func(owner, origin, invocation string) int {
				r := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/workflow-sessions/"+target+":pause", nil), map[string]string{"session_id": target})
				r.Header.Set("X-User-Id", owner)
				r.Header.Set("Origin", origin)
				r.Header.Set("X-LazyMind-Invocation-Id", invocation)
				w := httptest.NewRecorder()
				PauseNativeWorkflowHTTP(w, r)
				return w.Code
			}
			for _, denied := range [][3]string{{"other", "http://localhost", ""}, {"owner", "", ""}, {"owner", "http://localhost", "agent"}} {
				if status := pause(denied[0], denied[1], denied[2]); status < 400 {
					t.Fatalf("unauthorized pause: %d", status)
				}
			}
			if len(cancelled) != 0 {
				t.Fatal("denied request cancelled a task")
			}
			if status := pause("owner", "http://localhost", ""); status != http.StatusOK {
				t.Fatalf("pause status %d", status)
			}
			if len(cancelled) != 1 || cancelled[0] != "target-task" {
				t.Fatalf("wrong cancellation: %v", cancelled)
			}
			paused, _ := GetSession(t.Context(), db.DB, target)
			if paused.Status != "pausing" {
				t.Fatalf("premature pause: %s", paused.Status)
			}
			otherID := "foreground"
			if target == otherID {
				otherID = "background"
			}
			other, _ := GetSession(t.Context(), db.DB, otherID)
			if other.Status != "active" {
				t.Fatal("card pause affected another workflow")
			}
			if err := ConfirmNativeStop(t.Context(), db.DB, "attempt", "lease"); err != nil {
				t.Fatal(err)
			}
			paused, _ = GetSession(t.Context(), db.DB, target)
			view, err := displaystate.Read(db.DB, *paused)
			if err != nil || view.Status != "paused" {
				t.Fatalf("confirmed pause: %+v %v", view, err)
			}
		})
	}
}

func TestNativeResumeRebindsPauseOwnershipToNewChatTurn(t *testing.T) {
	db := newTestDB(t)
	session, err := CreateSession(t.Context(), db.DB, CreateSessionInput{
		SessionID: "before-dispatch", ConversationID: "chat", WorkflowID: "test", CreateUserID: "owner", TriggerHistoryID: "first-turn",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stopWorkflowSession(t.Context(), db.DB, nil, session, ""); err != nil {
		t.Fatal(err)
	}
	session, _ = GetSession(t.Context(), db.DB, session.ID)
	if _, err := BeginNativeControlTurn(t.Context(), db.DB, "owner", "chat", NativeControlCommand{
		SessionID: session.ID, Kind: "resume", ExpectedStateVersion: session.StateVersion, HistoryID: "resume-turn",
	}); err != nil {
		t.Fatal(err)
	}
	session, _ = GetSession(t.Context(), db.DB, session.ID)
	view, err := displaystate.Read(db.DB, *session)
	if err != nil || view.Status != "running" {
		t.Fatalf("wrong turn ownership: %+v %v", view, err)
	}
	bound, err := displaystate.ChatHistoryID(*session)
	if err != nil || bound != "resume-turn" {
		t.Fatalf("wrong pause ownership: %s %v", bound, err)
	}
}

func TestNativeRetryRebindsPauseOwnershipToNewChatTurn(t *testing.T) {
	db := newTestDB(t)
	session := orm.WorkflowSession{ID: "retry-workflow", ConversationID: "chat", WorkflowID: "test",
		CreateUserID: "owner", ControllerHost: "lazymind", Status: "failed", StateVersion: 4,
		TriggerHistoryID: "first-turn", ControlBindingJSON: `{"chat_history_id":"previous-turn"}`}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	version, err := BeginNativeControlTurn(t.Context(), db.DB, "owner", "chat", NativeControlCommand{
		SessionID: session.ID, Kind: "retry", ExpectedStateVersion: 4, HistoryID: "retry-turn",
	})
	if err != nil {
		t.Fatal(err)
	}
	if version != 5 {
		t.Fatalf("retry version: %d", version)
	}
	// The immutable launch history is retained; only control ownership moves.
	current, err := GetSession(t.Context(), db.DB, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := displaystate.Read(db.DB, *current)
	if err != nil || view.Status != "running" || view.ChatHistoryID != "retry-turn" || current.TriggerHistoryID != "first-turn" {
		t.Fatalf("retry binding: session=%+v view=%+v err=%v", current, view, err)
	}
	if err := StopWorkflowTurn(t.Context(), db.DB, nil, "owner", "chat", "previous-turn"); err != nil {
		t.Fatal(err)
	}
	current, _ = GetSession(t.Context(), db.DB, session.ID)
	if current.Status != "active" {
		t.Fatal("old Chat turn stopped the retried workflow")
	}
	if err := StopWorkflowTurn(t.Context(), db.DB, nil, "owner", "chat", "retry-turn"); err != nil {
		t.Fatal(err)
	}
	current, _ = GetSession(t.Context(), db.DB, session.ID)
	view, err = displaystate.Read(db.DB, *current)
	if err != nil || view.Status != "paused" {
		t.Fatalf("retry turn could not pause workflow: %+v %v", view, err)
	}
}

func TestNativeRetryRejectsInvalidControlWithoutRebinding(t *testing.T) {
	for _, scenario := range []string{"active", "completed", "stopped", "stale", "other-owner", "other-chat", "preference"} {
		t.Run(scenario, func(t *testing.T) {
			db := newTestDB(t)
			session := orm.WorkflowSession{ID: "retry", ConversationID: "chat", WorkflowID: "test",
				CreateUserID: "owner", ControllerHost: "lazymind", Status: "failed", StateVersion: 4, TriggerHistoryID: "first-turn",
				ControlBindingJSON: `{"chat_history_id":"previous-turn"}`}
			command := NativeControlCommand{SessionID: session.ID, Kind: "retry", ExpectedStateVersion: 4, HistoryID: "retry-turn"}
			owner, conversation := "owner", "chat"
			switch scenario {
			case "active", "completed", "stopped":
				session.Status = scenario
			case "stale":
				command.ExpectedStateVersion--
			case "other-owner":
				owner = "other"
			case "other-chat":
				conversation = "other"
			case "preference":
				command.PreferenceScope = "following"
			}
			if err := db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := BeginNativeControlTurn(t.Context(), db.DB, owner, conversation, command); err == nil {
				t.Fatal("invalid retry accepted")
			}
			current, err := GetSession(t.Context(), db.DB, session.ID)
			if err != nil || current.Status != session.Status || current.StateVersion != 4 || current.ControlBindingJSON != session.ControlBindingJSON {
				t.Fatalf("rejected retry mutated session: %+v %v", current, err)
			}
		})
	}
}

func TestNativeRetryDoesNotApprovePendingCheckpoint(t *testing.T) {
	db, _ := humanCheckpoint(t, false)
	session, err := GetSession(t.Context(), db.DB, "batch-session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", NativeControlCommand{
		SessionID: session.ID, Kind: "retry", ExpectedStateVersion: session.StateVersion, HistoryID: "retry-turn",
	}); err == nil {
		t.Fatal("retry accepted at approval checkpoint")
	}
	current, err := GetSession(t.Context(), db.DB, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := displaystate.Read(db.DB, *current)
	if err != nil || view.Status != "waiting" || view.ApprovalStepID != "branch_b" || current.StateVersion != session.StateVersion || current.ControlBindingJSON != session.ControlBindingJSON {
		t.Fatalf("retry changed approval checkpoint: %+v %v", view, err)
	}
}

func nativeRewindFixture(t *testing.T, status string) (*orm.DB, *orm.WorkflowSession) {
	t.Helper()
	db, _ := humanCheckpoint(t, true)
	var revision orm.WorkflowRevision
	if err := db.First(&revision, "id = ?", "batch-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes["branch_b"]
	node.Mode = "auto"
	graph.Nodes["branch_b"] = node
	graph.Nodes["followup"] = graphengine.CompiledNode{ID: "followup"}
	graph.Runtime.CompletedContinueSteps = []string{"followup"}
	if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(map[string]any{
		"status": status, "trigger_history_id": "original-turn", "control_binding_json": `{"chat_history_id":"previous-turn"}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	session, err := GetSession(t.Context(), db.DB, "batch-session")
	if err != nil {
		t.Fatal(err)
	}
	return db, session
}

func TestNativeRewindBindsNewTurnWithoutChangingRecoverySemantics(t *testing.T) {
	for _, scenario := range []struct{ status, step string }{{"completed", "branch_b"}, {"failed", "branch_b"}, {"completed", "followup"}} {
		t.Run(scenario.status+"/"+scenario.step, func(t *testing.T) {
			db, session := nativeRewindFixture(t, scenario.status)
			version, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", NativeControlCommand{
				SessionID: session.ID, Kind: "rewind", StepID: scenario.step, ExpectedStateVersion: session.StateVersion, HistoryID: "rewind-turn",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := FinishNativeControlTurn(t.Context(), db.DB, session.ID, version); err != nil {
				t.Fatal(err)
			}
			current, err := GetSession(t.Context(), db.DB, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			view, err := displaystate.Read(db.DB, *current)
			if err != nil || current.Status != scenario.status || current.TriggerHistoryID != "original-turn" || view.ChatHistoryID != "rewind-turn" {
				t.Fatalf("binding changed original recovery semantics: %+v %+v %v", current, view, err)
			}
			// Once the existing driver hands off, only the newly bound Chat owns stop.
			if err := db.Model(current).Update("status", "active").Error; err != nil {
				t.Fatal(err)
			}
			if err := StopWorkflowTurn(t.Context(), db.DB, nil, "batch-user", "batch-conversation", "previous-turn"); err != nil {
				t.Fatal(err)
			}
			current, _ = GetSession(t.Context(), db.DB, session.ID)
			if current.Status != "active" {
				t.Fatal("old turn stopped the new execution")
			}
			if err := StopWorkflowTurn(t.Context(), db.DB, nil, "batch-user", "batch-conversation", "rewind-turn"); err != nil {
				t.Fatal(err)
			}
			current, _ = GetSession(t.Context(), db.DB, session.ID)
			view, err = displaystate.Read(db.DB, *current)
			if err != nil || view.Status != "paused" {
				t.Fatalf("new turn cannot pause: %+v %v", view, err)
			}
		})
	}
}

func TestNativeRewindRejectsInvalidControlWithoutMutation(t *testing.T) {
	for _, scenario := range []string{"running", "paused", "approval", "stale", "other-owner", "other-chat", "preference", "unknown-step", "missing-step", "failed-followup", "external"} {
		t.Run(scenario, func(t *testing.T) {
			db, session := nativeRewindFixture(t, "completed")
			command := NativeControlCommand{SessionID: session.ID, Kind: "rewind", StepID: "branch_b", ExpectedStateVersion: session.StateVersion, HistoryID: "rewind-turn"}
			owner, conversation := "batch-user", "batch-conversation"
			switch scenario {
			case "running":
				session.Status = "active"
			case "paused":
				session.Status = "stopped"
			case "approval":
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
				if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
					t.Fatal(err)
				}
			case "stale":
				command.ExpectedStateVersion--
			case "other-owner":
				owner = "other"
			case "other-chat":
				conversation = "other"
			case "preference":
				command.PreferenceScope = "following"
			case "unknown-step":
				command.StepID = "unknown"
			case "missing-step":
				command.StepID = ""
			case "failed-followup":
				session.Status = "failed"
				command.StepID = "followup"
			case "external":
				session.ControllerHost = "external-agent"
				session.ControlProtocol = "workflow.control.v1"
			}
			if err := db.Save(session).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := BeginNativeControlTurn(t.Context(), db.DB, owner, conversation, command); err == nil {
				t.Fatal("invalid rewind accepted")
			}
			current, err := GetSession(t.Context(), db.DB, session.ID)
			if err != nil || current.Status != session.Status || current.StateVersion != session.StateVersion || current.ControlBindingJSON != session.ControlBindingJSON {
				t.Fatalf("invalid control mutated session: %+v %v", current, err)
			}
		})
	}
}

func TestNativeRewindPreservesExistingTransitionAdmission(t *testing.T) {
	for _, scenario := range []struct{ status, step, operation string }{
		{"completed", "branch_b", "rewind"}, {"failed", "branch_b", "rewind"}, {"completed", "followup", "execute"},
	} {
		t.Run(scenario.status+"/"+scenario.step, func(t *testing.T) {
			db, session := nativeRewindFixture(t, scenario.status)
			oldDB, oldState := store.DB(), store.State()
			store.Init(db.DB, db.DB, nil)
			t.Cleanup(func() { store.Init(oldDB, oldDB, oldState) })
			version, err := BeginNativeControlTurn(t.Context(), db.DB, "batch-user", "batch-conversation", NativeControlCommand{
				SessionID: session.ID, Kind: "rewind", StepID: scenario.step, ExpectedStateVersion: session.StateVersion, HistoryID: "rewind-turn",
			})
			if err != nil {
				t.Fatal(err)
			}
			response, status := transitionWorkflowSession(t.Context(), db.DB, session.ID, transitionCommandRequest{
				CommandID: "rewind-after-binding", Operation: scenario.operation, TargetStepID: scenario.step,
				TaskID: "new-attempt", ExpectedStateVersion: version, GraphHash: session.GraphHash, WorkflowMode: "dynamic",
			})
			if status != http.StatusOK || !response.Accepted {
				t.Fatalf("original transition rejected after binding: %+v (%d)", response, status)
			}
			current, err := GetSession(t.Context(), db.DB, session.ID)
			if err != nil {
				t.Fatal(err)
			}
			view, err := displaystate.Read(db.DB, *current)
			if err != nil || view.Status != "running" || view.ChatHistoryID != "rewind-turn" {
				t.Fatalf("handoff lost Chat ownership: %+v %v", view, err)
			}
		})
	}
}
