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
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

func lifecycleHandler(t *testing.T, controlled bool) (Handler, *gorm.DB, workflowcore.WorkflowHostIdentity) {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{},
		&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}, &orm.WorkflowEvent{}).DB
	h := Handler{Store: workflowstore.New(db)}
	session := orm.WorkflowSession{ID: "run", CreateUserID: "owner", Status: "active", StateVersion: 1}
	if controlled {
		session.ControllerHost = "external-agent"
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
				var action orm.WorkflowHostAction
				if err := db.Where("command_id = ? AND kind = ?", "stop", "cancel").First(&action).Error; err != nil {
					t.Fatal(err)
				}
				service := workflowcore.WorkflowControlService{DB: db}
				claim, err := service.ClaimHostAction(context.Background(), "owner", action.ID, identity)
				if err != nil {
					t.Fatal(err)
				}
				if pending := callLifecycle(t, h, false, "resume", false, true); pending.Code != http.StatusConflict {
					t.Fatalf("resumed before host cancellation: %d %s", pending.Code, pending.Body.String())
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
			resumed := callLifecycle(t, h, false, "resume", false, true)
			if resumed.Code != http.StatusOK {
				t.Fatalf("resume: %d %s", resumed.Code, resumed.Body.String())
			}
			var session orm.WorkflowSession
			if err := db.First(&session, "id = ?", "run").Error; err != nil || session.Status != wantResume {
				t.Fatalf("resume state: %+v %v", session, err)
			}
			if replay := callLifecycle(t, h, false, "resume", false, true); replay.Body.String() != resumed.Body.String() {
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
	for _, controlled := range []bool{false, true} {
		name := "native"
		if controlled {
			name = "external"
		}
		t.Run(name, func(t *testing.T) {
			h, db, _ := lifecycleHandler(t, controlled)
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
		})
	}
}

func TestMCPStopResumeSchedulesExactlyOneReplacement(t *testing.T) {
	for _, mode := range []string{"auto", "dynamic"} {
		for _, executor := range []string{"external-agent", "lazymind"} {
			t.Run(mode+"/"+executor, func(t *testing.T) {
				h, db, identity := lifecycleHandler(t, true)
				must := func(err error) {
					t.Helper()
					if err != nil {
						t.Fatal(err)
					}
				}
				must(db.AutoMigrate(&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{},
					&orm.WorkflowSlotRevision{}, &orm.WorkflowSlotOrder{}, &orm.WorkflowInputBinding{},
					&orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.WorkflowStepIntent{},
					&orm.WorkflowTransitionCommand{}, &orm.WorkflowApprovalPreference{}, &orm.TaskCenterTask{},
					&orm.SubAgentTask{}, &orm.SubAgentStep{}, &orm.SubAgentArtifact{}, &orm.ChatHistory{}))
				must(db.Where("session_id = ?", "run").Delete(&orm.WorkflowReviewCheckpoint{}).Error)
				node := graphengine.CompiledNode{ID: "draft", Mode: "auto"}
				if executor == "lazymind" {
					node.LegacyTools = []string{"image_generator"}
				}
				graph := graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion, GraphHash: "incident-graph", StartRoute: "all",
					Nodes:        map[string]graphengine.CompiledNode{"draft": node},
					ControlEdges: []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "draft"}, {ID: "end", From: "draft", To: "__end__"}}}
				must(db.Create(&orm.WorkflowRevision{ID: "revision", WorkflowResourceID: "resource", GraphHash: graph.GraphHash, CompiledGraph: graph.JSON(), GraphSchemaVersion: graphengine.SchemaVersion}).Error)
				content := []byte("id: incident-workflow\n")
				hash := "workflow-yaml"
				must(db.Create(&orm.WorkflowBlob{Hash: hash, Content: content, Size: int64(len(content))}).Error)
				must(db.Create(&orm.WorkflowRevisionEntry{RevisionID: "revision", Path: "workflow.yaml", EntryType: "file", BlobHash: &hash, Size: int64(len(content))}).Error)
				must(db.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Updates(map[string]any{"plugin_id": "incident-workflow", "plugin_revision_id": "revision", "graph_hash": graph.GraphHash, "graph_schema_version": graphengine.SchemaVersion, "workflow_mode": mode}).Error)
				must(db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("executor_host", executor).Error)
				stopped := callLifecycle(t, h, true, "stop", false, true)
				if stopped.Code != http.StatusOK {
					t.Fatalf("stop=%d %s", stopped.Code, stopped.Body.String())
				}
				resumed := callLifecycle(t, h, false, "resume", false, true)
				if resumed.Code != http.StatusOK {
					t.Fatalf("resume=%d %s", resumed.Code, resumed.Body.String())
				}
				var state workflowstore.SessionLifecycleState
				encoded, err := json.Marshal(decodeEnvelope(t, resumed).Data)
				must(err)
				must(json.Unmarshal(encoded, &state))
				if state.Status != "active" || state.ExecutionID == "" {
					t.Fatalf("resume did not schedule execution: %+v", state)
				}
				if replay := callLifecycle(t, h, false, "resume", false, true); replay.Code != http.StatusOK || replay.Body.String() != resumed.Body.String() {
					t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
				}
				var attempts []orm.WorkflowSessionStep
				must(db.Where("session_id = ?", "run").Order("attempt").Find(&attempts).Error)
				if len(attempts) != 2 || attempts[0].Validity != "stale" || attempts[1].ID != state.ExecutionID || attempts[1].Attempt != 2 || attempts[1].ExecutorHost != executor {
					t.Fatalf("invalid recovery attempts: %+v", attempts)
				}
				var session orm.WorkflowSession
				must(db.First(&session, "id = ?", "run").Error)
				if err := controlstore.ValidateExecution(db, session, "attempt", "old-handle"); err == nil {
					t.Fatal("old lease revived after resume")
				}
				svc := workflowcore.WorkflowControlService{DB: db}
				claim, err := svc.ClaimHostAction(t.Context(), "owner", state.ActionID, identity)
				must(err)
				_, err = svc.SettleHostAction(t.Context(), "owner", state.ActionID, workflowcore.WorkflowHostReceipt{ConnectorID: identity.ConnectorID, Credential: identity.Credential, InstanceID: identity.InstanceID, DispatchToken: claim.DispatchToken, Status: "failed", Error: "host rejected delivery"})
				must(err)
				var replacement orm.WorkflowSessionStep
				must(db.First(&replacement, "id = ?", state.ExecutionID).Error)
				wantStatus := "queued"
				if executor == "external-agent" {
					wantStatus = "failed"
				}
				if replacement.Status != wantStatus {
					t.Fatalf("failed wakeup changed executor ownership: %+v", replacement)
				}
			})
		}
	}
}

func TestMCPLifecyclePreservesOwnerAndConversationScope(t *testing.T) {
	for _, scenario := range []string{"wrong-owner", "wrong-conversation"} {
		t.Run(scenario, func(t *testing.T) {
			h, db, _ := lifecycleHandler(t, true)
			if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("conversation_id", "original-chat").Error; err != nil {
				t.Fatal(err)
			}
			owner := "owner"
			if scenario == "wrong-owner" {
				owner = "other"
			}
			r := mux.SetURLVars(request(http.MethodPost, "/workflow-sessions/run:stop", owner, []byte(`{"command_id":"stop"}`)), map[string]string{"session_id": "run"})
			r.Header.Set("X-LazyMind-Invocation-Id", "mcp-call")
			if scenario == "wrong-conversation" {
				r = r.WithContext(workflowstore.WithConversationScope(r.Context(), "other-chat"))
			}
			w := httptest.NewRecorder()
			h.StopWorkflow(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var session orm.WorkflowSession
			if err := db.First(&session, "id = ?", "run").Error; err != nil {
				t.Fatal(err)
			}
			if session.Status != "active" {
				t.Fatalf("unauthorized caller changed the run: %+v", session)
			}
		})
	}
}
