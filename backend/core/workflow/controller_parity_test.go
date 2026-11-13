package workflow

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/graphengine"
)

// Use the same graph and execution facts through the native and MCP entry paths.
// Transport must not change which advance/recovery the Runtime admits.
func TestControllerAdvanceParity(t *testing.T) {
	for _, scenario := range []string{"ready", "rewind-completed", "rewind-failed", "retry", "retry-with-sibling", "user-retry", "retry-exhausted", "approval", "review-rerun", "skip-approval", "stopped", "stale-version"} {
		for _, controller := range []string{"lazymind", "external-agent"} {
			t.Run(scenario+"/"+controller, func(t *testing.T) {
				graph := graphengine.CompiledStateGraph{StartRoute: "all", Nodes: map[string]graphengine.CompiledNode{"draft": {ID: "draft", Mode: "auto"}},
					ControlEdges: []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "draft"}, {ID: "end", From: "draft", To: "__end__"}}}
				if scenario == "approval" || scenario == "review-rerun" || scenario == "skip-approval" {
					graph.Nodes["draft"] = graphengine.CompiledNode{ID: "draft", Mode: "human"}
				}
				must := func(err error) {
					t.Helper()
					if err != nil {
						t.Fatal(err)
					}
				}
				provider := "native"
				if controller == "external-agent" {
					provider = controller
				}
				db, session := newControllerTestSession(t, provider, graph)
				updates := map[string]any{"status": "active"}
				status, wantCode, origin := "", "", "automatic"
				switch scenario {
				case "rewind-completed":
					status, updates["status"] = "succeeded", "completed"
				case "rewind-failed":
					status, updates["status"] = "succeeded", "failed"
				case "retry", "retry-with-sibling", "user-retry", "retry-exhausted":
					status, updates["status"] = "failed", "failed"
				case "approval":
					status, updates["status"], wantCode = "succeeded", "waiting", "WORKFLOW_APPROVAL_REQUIRED"
				case "review-rerun":
					status, updates["status"], origin = "succeeded", "waiting", "user"
				case "skip-approval":
					status = "succeeded"
				case "stopped":
					status, updates["status"], wantCode = "cancelled", "stopped", "WORKFLOW_PAUSED"
				case "stale-version":
					wantCode = "STATE_VERSION_CONFLICT"
				}
				must(db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(updates).Error)
				must(db.First(&session, "id = ?", "batch-session").Error)
				if status != "" {
					step := orm.WorkflowSessionStep{ID: "old", TaskID: "old-task", SessionID: session.ID, StepID: "draft", Attempt: 1, Status: status, Validity: "effective", ReviewRequired: scenario == "approval" || scenario == "review-rerun"}
					must(db.Create(&step).Error)
					if controller == "external-agent" && step.ReviewRequired {
						must(controlstore.CreateReview(db.DB, session, step, nil))
					}
				}
				if scenario == "skip-approval" {
					_, err := saveWorkflowApprovalPreference(db.DB, session.CreateUserID, session.WorkflowID, "draft", "step")
					must(err)
				}
				if scenario == "retry-with-sibling" {
					must(db.Create(&orm.WorkflowSessionStep{ID: "sibling", TaskID: "sibling-task", SessionID: session.ID, StepID: "sibling", Status: "running", Validity: "effective"}).Error)
				}
				if scenario == "user-retry" || scenario == "retry-exhausted" {
					for _, id := range []string{"one", "two", "three"} {
						must(db.Create(&orm.WorkflowTransitionCommand{CommandID: id, SessionID: session.ID, TargetStepID: "draft", Operation: "retry", RetryOrigin: "automatic", Status: "accepted", ResponseJSON: json.RawMessage(`{}`)}).Error)
					}
					if scenario == "user-retry" {
						origin = "user"
					} else {
						wantCode = "WORKFLOW_AUTOMATIC_RETRY_LIMIT_EXCEEDED"
					}
				}
				version := session.StateVersion
				if scenario == "stale-version" {
					version--
				}
				var err error
				if controller == "external-agent" {
					_, err = (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "advance", Kind: "begin", StepID: "draft", StateVersion: version, RetryOrigin: origin})
				} else {
					err = db.Transaction(func(tx *gorm.DB) error {
						_, _, _, err := applyWorkflowTransition(t.Context(), tx, session.ID, transitionCommandRequest{CommandID: "advance", Operation: "advance", TargetStepID: "draft", ExpectedStateVersion: version, RetryOrigin: origin, HandOff: true})
						return err
					})
				}
				if wantCode != "" {
					expectControlCode(t, err, wantCode)
				} else if err != nil {
					t.Fatal(err)
				}
				var attempts []orm.WorkflowSessionStep
				must(db.Where("session_id = ? AND step_id = ?", session.ID, "draft").Order("attempt").Find(&attempts).Error)
				count := 1
				if status != "" && wantCode == "" {
					count = 2
				}
				if scenario == "stale-version" {
					count = 0
				}
				if len(attempts) != count {
					t.Fatalf("attempts=%+v want %d", attempts, count)
				}
				if count == 2 && (attempts[0].Validity != "stale" || attempts[1].Attempt != 2 || attempts[1].Validity != "effective") {
					t.Fatalf("recovery did not replace the old attempt: %+v", attempts)
				}
				if scenario == "review-rerun" && controller == "external-agent" {
					var review orm.WorkflowReviewCheckpoint
					must(db.Where("attempt_id = ?", "old").First(&review).Error)
					if review.Status != "superseded" || review.DecisionCommandID != "advance" || review.AcceptedAt != nil {
						t.Fatalf("rerun did not replace the old review: %+v", review)
					}
				}
			})
		}
	}
}

func TestControlHTTPAcceptsMCPTransportAndChecksOwner(t *testing.T) {
	for _, owner := range []string{"owner", "other", ""} {
		t.Run("owner="+owner, func(t *testing.T) {
			svc, _ := hostControlFixture(t)
			r := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/workflow-sessions/run/control", bytes.NewBufferString(`{"command_id":"stop","kind":"stop"}`)), map[string]string{"session_id": "run"})
			r.Header.Set("X-User-Id", owner)
			r.Header.Set("X-LazyMind-Invocation-Id", "mcp-call")
			w := httptest.NewRecorder()
			(WorkflowControlHandler{Service: svc}).Command(w, r)
			want := http.StatusForbidden
			if owner == "owner" {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var session orm.WorkflowSession
			if err := svc.DB.First(&session, "id = ?", "run").Error; err != nil {
				t.Fatal(err)
			}
			if (session.Status == "stopped") != (owner == "owner") {
				t.Fatalf("wrong stop effect: %+v", session)
			}
		})
	}
}

func TestFailedControllerWakeupUsesNativeCleanup(t *testing.T) {
	for _, scenario := range []string{"idle", "executing", "stopped", "newer-wakeup"} {
		t.Run(scenario, func(t *testing.T) {
			db, session, review := runControlCheckpoint(t, "deepseek-harness", "next")
			identity := WorkflowHostIdentity{ConnectorID: "connector", Credential: strings.Repeat("x", 64), InstanceID: "driver"}
			binding, err := controlstore.DecodeBinding(session)
			if err != nil {
				t.Fatal(err)
			}
			binding.CredentialHash = controlstore.Hash([]byte(identity.Credential))
			encoded, err := json.Marshal(binding)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&session).Update("control_binding_json", string(encoded)).Error; err != nil {
				t.Fatal(err)
			}
			svc := WorkflowControlService{DB: db.DB}
			result, err := svc.Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "approve", Kind: "confirm_and_continue", ReviewID: review.ID, ReviewVersion: review.Version, ManifestHash: review.ManifestHash})
			if err != nil {
				t.Fatal(err)
			}
			claim, err := svc.ClaimHostAction(t.Context(), session.CreateUserID, result.Receipt.ActionID, identity)
			if err != nil {
				t.Fatal(err)
			}
			want := "active"
			switch scenario {
			case "idle":
				want = "failed"
			case "executing":
				err = db.Create(&orm.WorkflowSessionStep{ID: "next", TaskID: "next-task", SessionID: session.ID, StepID: "next", Status: "queued", Validity: "effective"}).Error
			case "stopped":
				want = "stopped"
				err = db.Model(&session).Update("status", "stopped").Error
			case "newer-wakeup":
				err = db.Create(&orm.WorkflowHostAction{ID: "newer", SessionID: session.ID, Kind: "continue", Status: "pending", CreatedAt: time.Now().Add(time.Second)}).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = svc.SettleHostAction(t.Context(), session.CreateUserID, result.Receipt.ActionID, WorkflowHostReceipt{ConnectorID: identity.ConnectorID, Credential: identity.Credential, InstanceID: identity.InstanceID, DispatchToken: claim.DispatchToken, Status: "failed", Error: "host rejected continuation"})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
				t.Fatal(err)
			}
			if session.Status != want {
				t.Fatalf("status=%s want %s", session.Status, want)
			}
		})
	}
}
