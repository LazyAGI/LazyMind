package facade

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
	workflowstore "lazymind/core/workflow/store"
)

func lifecycleHandler(t *testing.T, controlled bool) (Handler, *gorm.DB, workflowcore.WorkflowHostIdentity) {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{},
		&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}, &orm.WorkflowEvent{}).DB
	h := Handler{Store: workflowstore.New(db)}
	session := orm.WorkflowSession{ID: "run", CreateUserID: "owner", Status: "active", StateVersion: 1}
	if controlled {
		session.ControlProtocol = controlpolicy.Protocol
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	identity := workflowcore.WorkflowHostIdentity{ConnectorID: "connector", Credential: strings.Repeat("x", 64), InstanceID: "driver-process"}
	if controlled {
		_, err := (workflowcore.WorkflowControlService{DB: db}).Bind(context.Background(), "owner", "run", workflowcore.WorkflowHostBindingRequest{
			ConnectorID: identity.ConnectorID, Credential: identity.Credential, Provider: "deepseek-harness", DriverSessionID: "driver",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowReviewCheckpoint{ID: "review", SessionID: "run", AttemptID: "finished", Status: "pending", Version: 7, ManifestHash: "reviewed-version"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	expires := time.Now().Add(time.Hour)
	if err := db.Create(&orm.WorkflowSessionStep{ID: "attempt", TaskID: "attempt", SessionID: "run", StepID: "draft", Status: "running", Validity: "effective", LeaseToken: "old-handle", LeaseExpiresAt: &expires}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowOutbox{ID: "outbox", SessionID: "run", AttemptID: "attempt", Status: "pending", PayloadJSON: []byte(`{}`)}).Error; err != nil {
		t.Fatal(err)
	}
	return h, db, identity
}

func callLifecycle(t *testing.T, h Handler, stopped bool, commandID string, userControl, mcp bool) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"command_id": commandID})
	r := mux.SetURLVars(request(http.MethodPost, "/workflow-sessions/run", "owner", body), map[string]string{"session_id": "run"})
	if userControl {
		r.Header.Set("Origin", "http://localhost:8090")
	}
	if mcp {
		r.Header.Set("X-LazyMind-Invocation-Id", "mcp-call")
	}
	w := httptest.NewRecorder()
	if stopped {
		h.StopWorkflow(w, r)
	} else {
		h.ResumeWorkflow(w, r)
	}
	return w
}

func TestLifecycleHTTPPreservesLegacyAndControlledBehavior(t *testing.T) {
	for _, controlled := range []bool{false, true} {
		name := "legacy"
		if controlled {
			name = "controlled"
		}
		t.Run(name, func(t *testing.T) {
			h, db, identity := lifecycleHandler(t, controlled)
			stopped := callLifecycle(t, h, true, "stop", false, true)
			if stopped.Code != http.StatusOK {
				t.Fatalf("stop: %d %s", stopped.Code, stopped.Body.String())
			}
			replay := callLifecycle(t, h, true, "stop", false, true)
			if replay.Body.String() != stopped.Body.String() {
				t.Fatal("stop replay changed its receipt")
			}
			var attempt orm.WorkflowSessionStep
			if err := db.First(&attempt, "id = ?", "attempt").Error; err != nil {
				t.Fatal(err)
			}
			wantAttempt, wantResume := "interrupted", "active"
			if controlled {
				wantAttempt, wantResume = "cancelled", "waiting"
				if denied := callLifecycle(t, h, false, "agent-resume", true, true); denied.Code != http.StatusForbidden {
					t.Fatalf("MCP bypassed human control: %d %s", denied.Code, denied.Body.String())
				}
				if pending := callLifecycle(t, h, false, "resume", true, false); pending.Code != http.StatusConflict {
					t.Fatalf("resumed before host cancellation: %d %s", pending.Code, pending.Body.String())
				}
				var action orm.WorkflowHostAction
				if err := db.Where("command_id = ? AND kind = ?", "stop", "cancel").First(&action).Error; err != nil {
					t.Fatal(err)
				}
				service := workflowcore.WorkflowControlService{DB: db}
				claim, err := service.ClaimHostAction(context.Background(), "owner", action.ID, identity)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := service.SettleHostAction(context.Background(), "owner", action.ID, workflowcore.WorkflowHostReceipt{
					ConnectorID: identity.ConnectorID, Credential: identity.Credential, InstanceID: identity.InstanceID, DispatchToken: claim.DispatchToken, Status: "accepted",
				}); err != nil {
					t.Fatal(err)
				}
			}
			if attempt.Status != wantAttempt || attempt.LeaseExpiresAt != nil {
				t.Fatalf("attempt after stop: %+v", attempt)
			}
			resumed := callLifecycle(t, h, false, "resume", controlled, false)
			if resumed.Code != http.StatusOK {
				t.Fatalf("resume: %d %s", resumed.Code, resumed.Body.String())
			}
			var session orm.WorkflowSession
			if err := db.First(&session, "id = ?", "run").Error; err != nil || session.Status != wantResume {
				t.Fatalf("resume state: %+v %v", session, err)
			}
			if replay := callLifecycle(t, h, false, "resume", controlled, false); replay.Body.String() != resumed.Body.String() {
				t.Fatal("resume replay changed its receipt")
			}
			if controlled {
				var review orm.WorkflowReviewCheckpoint
				if err := db.First(&review, "id = ?", "review").Error; err != nil || review.Status != "pending" || review.Version != 7 || review.ManifestHash != "reviewed-version" {
					t.Fatalf("resume changed the review: %+v %v", review, err)
				}
				if err := controlstore.ValidateExecution(db, session, "attempt", "old-handle"); err == nil {
					t.Fatal("resume revived an old execution handle")
				}
			}
		})
	}
}

func TestLifecycleReceiptFailureRollsBackAllEffects(t *testing.T) {
	h, db, _ := lifecycleHandler(t, true)
	if err := db.Callback().Create().Before("gorm:create").Register("test:reject-lifecycle-receipt", func(tx *gorm.DB) {
		if tx.Statement.Table == "workflow_commands" {
			tx.AddError(errors.New("receipt unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	w := callLifecycle(t, h, true, "stop", false, true)
	if w.Code == http.StatusOK {
		t.Fatal("stop succeeded without a receipt")
	}
	var session orm.WorkflowSession
	if err := db.First(&session, "id = ?", "run").Error; err != nil || session.Status != "active" {
		t.Fatalf("stop escaped rollback: %+v %v", session, err)
	}
	var attempt orm.WorkflowSessionStep
	if err := db.First(&attempt, "id = ?", "attempt").Error; err != nil || attempt.Status != "running" || attempt.LeaseToken != "old-handle" {
		t.Fatalf("attempt escaped rollback: %+v %v", attempt, err)
	}
	var actions int64
	if err := db.Model(&orm.WorkflowHostAction{}).Count(&actions).Error; err != nil || actions != 0 {
		t.Fatalf("host action escaped rollback: %d %v", actions, err)
	}
}

func TestLifecycleStoredLegacyReceiptRemainsReadable(t *testing.T) {
	h, db, _ := lifecycleHandler(t, false)
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("state_version", 9).Error; err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"session_id": "run", "stopped": true})
	if err := db.Create(&orm.WorkflowCommand{CommandID: "historical-stop", OwnerUserID: "owner", SessionID: "run", ContractVersion: "workflow.v1",
		RequestHash: controlstore.Hash(input), HTTPStatus: http.StatusOK,
		ResponseJSON: []byte(`{"session_id":"run","status":"stopped","state_version":5}`)}).Error; err != nil {
		t.Fatal(err)
	}
	// The run may already have moved on; replay returns the stored receipt only.
	response := callLifecycle(t, h, true, "historical-stop", false, true)
	if response.Code != http.StatusOK {
		t.Fatalf("historical replay: %d %s", response.Code, response.Body.String())
	}
	data := decodeEnvelope(t, response).Data.(map[string]any)
	if data["status"] != "stopped" || data["state_version"] != float64(5) || data["command_id"] != "historical-stop" {
		t.Fatalf("historical receipt changed: %+v", data)
	}
	var session orm.WorkflowSession
	if err := db.First(&session, "id = ?", "run").Error; err != nil || session.Status != "active" || session.StateVersion != 9 {
		t.Fatalf("receipt replay changed current state: %+v %v", session, err)
	}
}
