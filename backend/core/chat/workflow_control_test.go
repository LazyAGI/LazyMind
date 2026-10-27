package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/workflow"
	"lazymind/core/workflow/displaystate"
)

func TestChatPauseStillSignalsTurnWhenWorkflowCancellationFails(t *testing.T) {
	db := newSidechatTestDB(t)
	ctx := t.Context()
	sidechatTestConversation(t, db, "pause-chat", "owner", "pause")
	if _, err := workflow.CreateSession(ctx, db, workflow.CreateSessionInput{SessionID: "pause-workflow",
		ConversationID: "pause-chat", CreateUserID: "owner", WorkflowID: "test", TriggerHistoryID: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if _, err := subagent.CreateTask(ctx, db, subagent.CreateTaskInput{TaskID: "pause-attempt",
		ConversationID: "pause-chat", CreateUserID: "owner", AgentType: "workflow_step", TriggerHistoryID: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: "pause-attempt", TaskID: "pause-attempt", StepID: "step",
		SessionID: "pause-workflow", Status: "running", LeaseToken: "lease"}).Error; err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	err := StopConversationExecution(ctx, db, store.State(), "owner", "pause-chat", "foreground", "stopped by user")
	if err == nil {
		t.Fatal("workflow cancellation failure was swallowed")
	}
	if received, err := store.State().LPop(ctx, chatStopKey("pause-chat", "foreground")); err != nil || !received {
		t.Fatalf("workflow failure suppressed Chat pause: received=%v err=%v", received, err)
	}
	session, err := workflow.GetSession(ctx, db, "pause-workflow")
	if err != nil || session.Status != "pausing" {
		t.Fatalf("unconfirmed executor exposed as paused: %+v %v", session, err)
	}
}

func TestWorkflowRecoveryIsBoundToAdmittedChatTurn(t *testing.T) {
	for _, kind := range []string{"resume", "retry"} {
		t.Run(kind, func(t *testing.T) {
			for _, scenario := range []string{"stale", "other-owner", "ordinary-chat", "upstream-failure", "store-unavailable"} {
				t.Run(scenario, func(t *testing.T) {
					db := newSidechatTestDB(t)
					sidechatTestConversation(t, db, "control-chat", "owner", "control")
					now := time.Now().UTC()
					session := orm.WorkflowSession{ID: "workflow", ConversationID: "control-chat", CreateUserID: "owner",
						WorkflowID: "test", Status: "waiting", ControllerHost: "lazymind", LastStoppedAt: &now, StateVersion: 2, TriggerHistoryID: "original-turn"}
					if kind == "retry" {
						session.Status = "failed"
					}
					if err := db.Create(&session).Error; err != nil {
						t.Fatal(err)
					}
					calls := 0
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if _, leaked := body["_workflow_control"]; leaked {
							t.Error("user control capability leaked to model executor")
						}
						w.WriteHeader(http.StatusBadRequest)
					}))
					defer upstream.Close()
					command := &workflow.NativeControlCommand{SessionID: session.ID, Kind: kind, ExpectedStateVersion: 2}
					owner := "owner"
					if scenario == "stale" {
						command.ExpectedStateVersion--
					}
					if scenario == "other-owner" {
						owner = "other"
					}
					body := map[string]any{"query": "continue", "user_id": "owner"}
					if scenario != "ordinary-chat" {
						body["_workflow_control"], body["_workflow_control_owner"] = command, owner
					}
					recorder := httptest.NewRecorder()
					stateStore := store.State()
					if scenario == "store-unavailable" {
						stateStore = nil
					}
					handleStreamChat(recorder, httptest.NewRequest(http.MethodPost, "/conversations:chat", nil),
						db, stateStore, upstream.URL, body, session.ConversationID, "continue",
						chatPersistTarget{HistoryID: "control-history", Seq: 1}, false, json.RawMessage(`{}`))
					if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
						t.Fatal(err)
					}
					view, err := displaystate.Read(db, session)
					want := "paused"
					if kind == "retry" || scenario == "upstream-failure" {
						want = "failed"
					}
					if err != nil || view.Status != want {
						t.Fatalf("workflow after %s: %+v %v", scenario, view, err)
					}
					wantHistory := "original-turn"
					if scenario == "upstream-failure" {
						wantHistory = "control-history"
					}
					if view.ChatHistoryID != wantHistory {
						t.Fatalf("control ownership after %s: %s, want %s", scenario, view.ChatHistoryID, wantHistory)
					}
					if scenario == "stale" || scenario == "other-owner" {
						if calls != 0 || recorder.Code != http.StatusConflict || session.StateVersion != 2 {
							t.Fatalf("refused control was executed: calls=%d status=%d version=%d", calls, recorder.Code, session.StateVersion)
						}
					} else if scenario == "store-unavailable" {
						if calls != 0 || recorder.Code != http.StatusServiceUnavailable || session.StateVersion != 2 {
							t.Fatalf("unavailable Chat released workflow: calls=%d status=%d version=%d", calls, recorder.Code, session.StateVersion)
						}
					} else if calls != 1 {
						t.Fatalf("expected one Chat request, got %d: %s", calls, recorder.Body.String())
					}
				})
			}
		})
	}
}
