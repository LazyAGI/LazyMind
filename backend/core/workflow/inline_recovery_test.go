package workflow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/subagent"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

func TestInlineChatRecoveryOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, ownerStatus, binding, sessionStatus, stepStatus, host string
		active, dismissed, approval, want                           bool
	}{
		{name: "failed owner", ownerStatus: "failed", want: true},
		{name: "live owner", ownerStatus: "generating"},
		{name: "completed owner", ownerStatus: "completed"},
		{name: "user cancelled", ownerStatus: "cancelled"},
		{name: "missing history"},
		{name: "new Chat running", ownerStatus: "failed", active: true},
		{name: "new owner completed", ownerStatus: "failed", binding: "replacement"},
		{name: "user stopped", ownerStatus: "failed", sessionStatus: "stopped"},
		{name: "removed workflow", ownerStatus: "failed", dismissed: true},
		{name: "completed workflow", ownerStatus: "failed", sessionStatus: "completed"},
		{name: "successor running", ownerStatus: "failed", stepStatus: "running"},
		{name: "external controller", ownerStatus: "failed", host: "external-agent"},
		{name: "approval required", ownerStatus: "failed", approval: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			s := orm.WorkflowSession{ID: "session", ConversationID: "conversation", TriggerHistoryID: "owner", ControllerHost: "lazymind", Status: "active", Dismissed: tc.dismissed}
			if tc.sessionStatus != "" {
				s.Status = tc.sessionStatus
			}
			if tc.binding != "" {
				s.ControlBindingJSON = `{"chat_history_id":"replacement"}`
			}
			if tc.host != "" {
				s.ControllerHost = tc.host
			}
			if tc.approval {
				seedEventLoopRevision(t, db, "approval-revision")
				s.WorkflowRevisionID = "approval-revision"
				graph := graphengine.CompiledStateGraph{Nodes: map[string]graphengine.CompiledNode{"analysis": {ID: "analysis", Mode: "human"}}}
				if err := db.Model(&orm.WorkflowRevision{}).Where("id = ?", s.WorkflowRevisionID).Update("compiled_graph", graph.JSON()).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&s).Error; err != nil {
				t.Fatal(err)
			}
			stepStatus := "succeeded"
			if tc.stepStatus != "" {
				stepStatus = tc.stepStatus
			}
			if err := db.Create(&orm.WorkflowSessionStep{ID: "step", SessionID: s.ID, StepID: "analysis", TaskID: "task", Status: stepStatus, Validity: "effective"}).Error; err != nil {
				t.Fatal(err)
			}
			if tc.ownerStatus != "" {
				if err := db.Create(&orm.ChatHistory{ID: "owner", ConversationID: s.ConversationID, RunStatus: tc.ownerStatus}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.binding != "" || tc.active {
				status := "completed"
				if tc.active {
					status = "generating"
				}
				if err := db.Create(&orm.ChatHistory{ID: "replacement", ConversationID: s.ConversationID, RunStatus: status}).Error; err != nil {
					t.Fatal(err)
				}
			}
			got, err := inlineChatNeedsRecovery(t.Context(), db.DB, s)
			if err != nil || got != tc.want {
				t.Fatalf("recover=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestRecoveredInlineStepStartsContinuationChat(t *testing.T) {
	for _, scenario := range []struct {
		mode       string
		childFirst bool
	}{{"dynamic", false}, {"auto", false}, {"dynamic", true}, {"auto", true}} {
		t.Run(fmt.Sprintf("%s/child-first-%v", scenario.mode, scenario.childFirst), func(t *testing.T) {
			mode := scenario.mode
			db := newTestDB(t)
			hash, schema := seedEventLoopRevision(t, db, "revision")
			_, err := CreateSession(t.Context(), db.DB, CreateSessionInput{SessionID: "session", ConversationID: "conversation", WorkflowID: "image-workflow", WorkflowRevisionID: "revision", GraphHash: hash, GraphSchemaVersion: schema, WorkflowMode: mode, CreateUserID: "user"})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "session").Update("trigger_history_id", "failed-chat").Error; err != nil {
				t.Fatal(err)
			}
			ownerStatus := "failed"
			if scenario.childFirst {
				ownerStatus = "generating"
			}
			if err := db.Create(&orm.ChatHistory{ID: "failed-chat", ConversationID: "conversation", RunStatus: ownerStatus}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := CreateSessionStep(t.Context(), db.DB, "session", "analyze_subject", "task", 1); err != nil {
				t.Fatal(err)
			}
			if _, err := subagent.CreateTask(t.Context(), db.DB, subagent.CreateTaskInput{TaskID: "task", ConversationID: "conversation", AgentType: "workflow_step", CreateUserID: "user", Params: json.RawMessage(`{"hand_off":false}`)}); err != nil {
				t.Fatal(err)
			}
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if r.URL.Path != "/conversations:chat" || r.Header.Get("X-User-Id") != "user" {
					t.Errorf("unexpected continuation request: %s", r.URL.Path)
				}
				wc := body["workflow_context"].(map[string]any)
				encoded, _ := json.Marshal(wc["recovery"])
				var recovery NativeChatRecovery
				if err := json.Unmarshal(encoded, &recovery); err != nil {
					t.Error(err)
				}
				if err := db.Create(&orm.ChatHistory{ID: "replacement", ConversationID: "conversation", RunStatus: "generating"}).Error; err != nil {
					t.Error(err)
				}
				version, err := AdmitNativeChatRecovery(r.Context(), db.DB, "user", "conversation", "replacement", recovery)
				if err != nil {
					t.Error(err)
				}
				defer func() {
					if err := FinishNativeControlTurn(t.Context(), db.DB, recovery.SessionID, version); err != nil {
						t.Error(err)
					}
				}()
				session, err := GetSession(r.Context(), db.DB, "session")
				if err != nil {
					t.Error(err)
					return
				}
				view, err := displaystate.Read(db.DB, *session)
				if err != nil || view.ChatHistoryID != "replacement" || view.Status != "running" {
					t.Errorf("replacement ownership: %+v error=%v", view, err)
				}
				requests <- body
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(200)
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CORE_SELF_URL", server.URL)
			handOff := false
			events := make(chan string, 10)
			OnSubAgentDone(t.Context(), db.DB, nil, "task", subagent.StatusSucceeded, "saved analysis",
				func(kind string, _ map[string]any) { events <- kind }, &WorkflowChatContext{SessionID: "session", ConvID: "conversation", WorkflowID: "image-workflow", StepID: "analyze_subject", WorkflowMode: mode, UserID: "user", HandOff: &handOff})
			if scenario.childFirst {
				if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "failed-chat").Update("run_status", "failed").Error; err != nil {
					t.Fatal(err)
				}
			}
			// Replay the other terminal notification: admission must remain unique.
			if err := RecoverNativeChatAfterFailure(t.Context(), db.DB, nil, "conversation", "failed-chat"); err != nil {
				t.Fatal(err)
			}
			select {
			case body := <-requests:
				wc := body["workflow_context"].(map[string]any)
				if body["conversation_id"] != "conversation" || body["stream"] != true || wc["session_id"] != "session" || wc["workflow_mode"] != mode || wc["synthetic_source"] != "auto_continue" {
					t.Fatalf("unexpected request: %#v", body)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("successful recovered child never started a replacement Chat")
			}
			// Wait for the existing no-progress cleanup too: a refused/no-op
			// replacement must expose failure rather than pretend to execute.
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				session, err := GetSession(t.Context(), db.DB, "session")
				if err != nil {
					t.Fatal(err)
				}
				if session.Status == SessionStatusFailed {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("continuation cleanup did not finish")
		})
	}
}

func TestNativeRecoveryAdmissionFences(t *testing.T) {
	for _, name := range []string{"accepted", "stopped", "new version", "new Chat", "missing replacement"} {
		t.Run(name, func(t *testing.T) {
			db := newTestDB(t)
			session := orm.WorkflowSession{ID: "session", ConversationID: "conversation", CreateUserID: "user", ControllerHost: "lazymind", Status: "active", StateVersion: 1, TriggerHistoryID: "owner", ControlBindingJSON: `{"recovered_attempt_id":"attempt"}`}
			if name == "stopped" {
				session.Status = "stopped"
			}
			if name == "new version" {
				session.StateVersion++
			}
			if err := db.Create(&session).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.ChatHistory{ID: "owner", ConversationID: "conversation", RunStatus: "failed"}).Error; err != nil {
				t.Fatal(err)
			}
			if name != "missing replacement" {
				if err := db.Create(&orm.ChatHistory{ID: "replacement", ConversationID: "conversation", RunStatus: "generating"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if name == "new Chat" {
				if err := db.Create(&orm.ChatHistory{ID: "user-chat", ConversationID: "conversation", RunStatus: "generating"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			recovery := NativeChatRecovery{SessionID: "session", AttemptID: "attempt", ExpectedStateVersion: 1}
			_, err := AdmitNativeChatRecovery(t.Context(), db.DB, "user", "conversation", "replacement", recovery)
			if (err == nil) != (name == "accepted") {
				t.Fatalf("admission error=%v", err)
			}
			if name == "accepted" {
				if _, err := AdmitNativeChatRecovery(t.Context(), db.DB, "user", "conversation", "replacement", recovery); err == nil {
					t.Fatal("duplicate recovery admitted")
				}
			}
		})
	}
}
