package workflow

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
	"testing"
	"time"
)

func runControlCheckpoint(t *testing.T, provider, scenario string) (*orm.DB, orm.WorkflowSession, orm.WorkflowReviewCheckpoint) {
	t.Helper()
	db, hash := setupBatchTransitionSession(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.AutoMigrate(&orm.WorkflowHostAction{}, &orm.WorkflowCommand{}))
	graph := graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion, GraphHash: hash, StartRoute: "all",
		Nodes:        map[string]graphengine.CompiledNode{"review": {ID: "review", Mode: "human"}, "next": {ID: "next", Mode: "auto"}},
		ControlEdges: []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "review"}, {ID: "next", From: "review", To: "next"}, {ID: "end", From: "next", To: "__end__"}},
	}
	if scenario == "final" {
		delete(graph.Nodes, "next")
		graph.ControlEdges = graph.ControlEdges[:2]
		graph.ControlEdges[1].To = "__end__"
	}
	if scenario == "parallel" {
		graph.Nodes["other"] = graphengine.CompiledNode{ID: "other", Mode: "human"}
		graph.ControlEdges = append(graph.ControlEdges, graphengine.CompiledEdge{ID: "other", From: "__start__", To: "other"})
	}
	must(db.Model(&orm.WorkflowRevision{}).Where("id = ?", "batch-revision").Update("compiled_graph", graph.JSON()).Error)
	updates := map[string]any{"controller_host": "lazymind"}
	if provider != "native" {
		updates = map[string]any{"controller_host": "external-agent", "control_protocol": controlpolicy.Protocol,
			"control_binding_json": `{"required":true,"connector_id":"connector","driver_session_id":"driver","provider":"` + provider + `"}`}
	}
	must(db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(updates).Error)
	var session orm.WorkflowSession
	must(db.First(&session, "id = ?", "batch-session").Error)
	var review orm.WorkflowReviewCheckpoint
	now := time.Now().UTC()
	for _, id := range []string{"other", "review"} {
		if _, ok := graph.Nodes[id]; !ok {
			continue
		}
		created := now
		if id == "other" {
			created = now.Add(-time.Second)
		}
		step := orm.WorkflowSessionStep{ID: id + "-attempt", TaskID: id + "-task", SessionID: session.ID, StepID: id, Attempt: 1, Status: "succeeded", Validity: "effective", ReviewRequired: true, CreatedAt: created, UpdatedAt: created}
		must(db.Create(&step).Error)
		if provider != "native" {
			must(controlstore.CreateReview(db.DB, session, step, nil))
			must(FinalizeHostAttempt(t.Context(), db.DB, session.ID, id, step.ID, "succeeded"))
			if id == "review" {
				must(db.First(&review, "attempt_id = ?", step.ID).Error)
			}
		} else {
			must(freezeRouteDecision(t.Context(), db.DB, session.ID, id, step.TaskID))
		}
	}
	must(db.First(&session, "id = ?", session.ID).Error)
	return db, session, review
}

func TestRunControlStateParity(t *testing.T) {
	for _, provider := range []string{"native", "workbuddy", "deepseek-harness", "codex"} {
		for _, scenario := range []string{"next", "skip-following", "final", "parallel"} {
			t.Run(provider+"/"+scenario, func(t *testing.T) {
				db, session, review := runControlCheckpoint(t, provider, scenario)
				before, err := displaystate.Read(db.DB, session)
				if err != nil || before.Status != "waiting" || before.ApprovalStepID != "review" {
					t.Fatalf("checkpoint=%+v err=%v", before, err)
				}
				var eventsBefore int64
				if err := db.Model(&orm.WorkflowEvent{}).Count(&eventsBefore).Error; err != nil {
					t.Fatal(err)
				}
				scope := ""
				if scenario == "skip-following" {
					scope = "following"
				}
				var result WorkflowControlResult
				command := WorkflowControlCommand{CommandID: "approve", Kind: "confirm_and_continue", ReviewID: review.ID, ReviewVersion: review.Version, ManifestHash: review.ManifestHash, PreferenceScope: scope}
				if provider == "native" {
					_, err = BeginNativeControlTurn(t.Context(), db.DB, session.CreateUserID, session.ConversationID, NativeControlCommand{SessionID: session.ID, Kind: "approve", StepID: "review", ExpectedStateVersion: session.StateVersion, PreferenceScope: scope})
				} else {
					result, err = (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID, command)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = db.First(&session, "id = ?", session.ID).Error; err != nil {
					t.Fatal(err)
				}
				after, err := displaystate.Read(db.DB, session)
				wantStatus, wantDisplay := SessionStatusActive, "running"
				if scenario == "final" {
					wantStatus, wantDisplay = SessionStatusCompleted, "completed"
				}
				if scenario == "parallel" {
					wantStatus, wantDisplay = SessionStatusWaiting, "waiting"
				}
				if err != nil || session.Status != wantStatus || after.Status != wantDisplay {
					t.Fatalf("run=%s display=%+v err=%v", session.Status, after, err)
				}
				if wantStatus == SessionStatusActive && after.Reason != "preparing" {
					t.Fatalf("not preparing next step: %+v", after)
				}
				var events []orm.WorkflowEvent
				if err = db.Order("id ASC").Offset(int(eventsBefore)).Find(&events).Error; err != nil {
					t.Fatal(err)
				}
				seenDisplay := false
				for _, event := range events {
					if event.EventType != "workflow.snapshot" && event.EventType != "workflow.patch" {
						continue
					}
					var payload struct {
						Display displaystate.View `json:"display"`
					}
					if err = json.Unmarshal(event.PayloadJSON, &payload); err != nil {
						t.Fatal(err)
					}
					seenDisplay = true
					if payload.Display.Status != wantDisplay {
						t.Fatalf("transient display escaped transaction: %s", event.PayloadJSON)
					}
				}
				if !seenDisplay {
					t.Fatal("missing durable state event")
				}
				if provider != "native" {
					wantAction := wantStatus == SessionStatusActive
					if (result.Receipt.ActionID != "") != wantAction {
						t.Fatalf("unexpected continuation receipt: %+v", result.Receipt)
					}
					replay, err := (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID, command)
					if err != nil || replay.Receipt.ActionID != result.Receipt.ActionID {
						t.Fatalf("replayed continuation: %+v %v", replay, err)
					}
					var actions int64
					if err = db.Model(&orm.WorkflowHostAction{}).Where("kind = ?", "continue").Count(&actions).Error; err != nil {
						t.Fatal(err)
					}
					if (wantAction && actions != 1) || (!wantAction && actions != 0) {
						t.Fatalf("duplicate or unexpected notifications: %d", actions)
					}
					if scenario == "next" {
						begun, err := (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID,
							WorkflowControlCommand{CommandID: "begin-next", Kind: "begin", StepID: "next", StateVersion: session.StateVersion})
						if err != nil || begun.Receipt.ExecutionID == "" {
							t.Fatalf("next step did not begin: %+v %v", begun, err)
						}
						if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
							t.Fatal(err)
						}
						executing, err := displaystate.Read(db.DB, session)
						if err != nil || executing.Status != "running" || executing.Reason != "executing" {
							t.Fatalf("next step display=%+v err=%v", executing, err)
						}
					}
				}
			})
		}
	}
}

func TestExternalContinuationDeliveryFailureRollsBackRunState(t *testing.T) {
	db, session, review := runControlCheckpoint(t, "workbuddy", "next")
	notificationFailure := errors.New("notification storage unavailable")
	if err := db.Callback().Create().Before("gorm:create").Register("test:reject-continuation", func(tx *gorm.DB) {
		if tx.Statement.Table == "workflow_host_actions" {
			tx.AddError(notificationFailure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("test:reject-continuation")
	_, err := (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "approve", Kind: "confirm_and_continue", ReviewID: review.ID, ReviewVersion: review.Version, ManifestHash: review.ManifestHash})
	if !errors.Is(err, notificationFailure) {
		t.Fatalf("did not reach notification failure: %v", err)
	}
	var actual orm.WorkflowSession
	if err = db.First(&actual, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if actual.Status != session.Status || actual.StateVersion != session.StateVersion {
		t.Fatalf("run escaped rollback: %+v", actual)
	}
	if err = db.First(&review, "id = ?", review.ID).Error; err != nil || review.Status != "pending" {
		t.Fatalf("review escaped rollback: %+v %v", review, err)
	}
	view, err := displaystate.Read(db.DB, actual)
	if err != nil || view.Status != "waiting" {
		t.Fatalf("display escaped rollback: %+v %v", view, err)
	}
}

func TestExternalContinueSharesNativePreparationState(t *testing.T) {
	for _, provider := range []string{"native", "workbuddy"} {
		t.Run(provider, func(t *testing.T) {
			db, session, review := runControlCheckpoint(t, provider, "next")
			if provider == "native" {
				if err := db.Create(&orm.WorkflowReviewCheckpoint{ID: "accepted", SessionID: session.ID, AttemptID: "review-attempt", StepID: "review", Status: "accepted"}).Error; err != nil {
					t.Fatal(err)
				}
				admitted, err := prepareContinuation(t.Context(), db.DB, session.ID)
				if err != nil || !admitted {
					t.Fatalf("continuation rejected: %t %v", admitted, err)
				}
			} else {
				if _, err := (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "confirm", Kind: "confirm", ReviewID: review.ID, ReviewVersion: review.Version, ManifestHash: review.ManifestHash}); err != nil {
					t.Fatal(err)
				}
				if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
					t.Fatal(err)
				}
				if _, err := (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "continue", Kind: "continue", StateVersion: session.StateVersion}); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
				t.Fatal(err)
			}
			view, err := displaystate.Read(db.DB, session)
			if err != nil || session.Status != SessionStatusActive || view.Status != "running" || view.Reason != "preparing" {
				t.Fatalf("run=%s view=%+v err=%v", session.Status, view, err)
			}
		})
	}
}
