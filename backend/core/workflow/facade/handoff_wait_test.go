package facade

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	workflowstore "lazymind/core/workflow/store"
)

func TestHandoffWaitsForExecutionAndStopAcknowledgement(t *testing.T) {
	for _, terminal := range []string{"succeeded", "failed", "paused"} {
		t.Run(terminal, func(t *testing.T) {
			models := append(workflowstore.Models(), &orm.WorkflowSession{}, &orm.WorkflowSessionStep{},
				&orm.WorkflowTransitionCommand{}, &orm.SubAgentTask{}, &orm.SubAgentStep{})
			db := orm.MigrateTestDB(t, models...).DB
			repo := workflowstore.New(db)
			if err := db.Create(&orm.WorkflowSession{ID: "s1", CreateUserID: "owner", Status: "active"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.WorkflowSessionStep{ID: "attempt-1", SessionID: "s1", StepID: "generate",
				TaskID: "task-1", Attempt: 1, Status: "running", Validity: "effective"}).Error; err != nil {
				t.Fatal(err)
			}
			var launches atomic.Int32
			accepted := make(chan struct{}, 1)
			delegate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				launches.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["hand_off"] != true {
					t.Errorf("handoff checkpoint lost: %v, %v", body, err)
				}
				common.ReplyOK(w, map[string]any{"accepted": true, "session_id": "s1", "task_id": "task-1"})
				accepted <- struct{}{}
			})
			h := Handler{Store: repo, Projection: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var attempt orm.WorkflowSessionStep
				if err := db.First(&attempt, "id = ?", "attempt-1").Error; err != nil {
					t.Error(err)
				}
				display := attempt.Status
				if attempt.TerminalCode == "WORKFLOW_STOPPED" {
					display = "paused"
				}
				common.ReplyOK(w, map[string]any{"display": map[string]any{"status": display}})
			})}
			body := []byte(`{"contract_version":"workflow.v1","command_id":"handoff-1","tool":"advance_step_and_hand_off","session_id":"s1","expected_state_version":1,"steps":[{"step_id":"generate"}]}`)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := mux.SetURLVars(request(http.MethodPost, "/workflow-sessions/s1:advance-step-and-hand-off", "owner", body).WithContext(ctx),
				map[string]string{"session_id": "s1"})
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); h.Command(delegate)(w, r) }()
			defer func() { cancel(); <-done }()
			select {
			case <-accepted:
			case <-done:
				t.Fatalf("command rejected: %s", w.Body.String())
			case <-ctx.Done():
				t.Fatal("command was not accepted")
			}
			assertWaiting := func() {
				t.Helper()
				select {
				case <-done:
					t.Fatalf("handoff returned before execution exited: %s", w.Body.String())
				case <-time.After(250 * time.Millisecond):
				}
			}
			update := func(status, code string) {
				t.Helper()
				if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Updates(
					map[string]any{"status": status, "terminal_code": code}).Error; err != nil {
					t.Fatal(err)
				}
			}
			assertWaiting()
			wantStatus := terminal
			if terminal == "paused" {
				update("interrupted", "WORKFLOW_STOP_REQUESTED")
				assertWaiting()
				update("interrupted", "WORKFLOW_STOPPED")
				wantStatus = "interrupted"
			} else {
				update(terminal, "")
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("handoff did not return after execution exited")
			}
			var result struct {
				Data struct {
					AttemptStatus string `json:"attempt_status"`
					ExecutionMode string `json:"execution_mode"`
					WorkflowState struct {
						Display struct{ Status string } `json:"display"`
					} `json:"workflow_state"`
				} `json:"result"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK ||
				result.Data.AttemptStatus != wantStatus || result.Data.ExecutionMode != "synchronous" ||
				result.Data.WorkflowState.Display.Status != terminal {
				t.Fatalf("terminal result missing: %d %s (%v)", w.Code, w.Body.String(), err)
			}
			// Retrying the same command reads its result without launching another task.
			replay := httptest.NewRecorder()
			r = mux.SetURLVars(request(http.MethodPost, r.URL.Path, "owner", body), map[string]string{"session_id": "s1"})
			h.Command(delegate)(replay, r)
			if replay.Code != http.StatusOK || launches.Load() != 1 {
				t.Fatalf("handoff replay launched another task: %d %s", launches.Load(), replay.Body.String())
			}
		})
	}
}
