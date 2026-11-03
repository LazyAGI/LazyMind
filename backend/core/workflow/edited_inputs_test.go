package workflow

import (
	"encoding/json"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

func editedInputCheckpoint(t *testing.T, provider string) (*orm.DB, orm.WorkflowSession, *orm.WorkflowSlotRevision) {
	t.Helper()
	db, hash := setupBatchTransitionSession(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.AutoMigrate(&orm.WorkflowHumanArtifact{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}))
	graph := graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion, GraphHash: hash, StartRoute: "all",
		Nodes: map[string]graphengine.CompiledNode{
			"outline": {ID: "outline", Mode: "auto", Outputs: []string{"deck_outline"}},
			"plan":    {ID: "plan", Mode: "human", Input: &graphengine.Expression{Material: "deck_outline"}, Outputs: []string{"slide_outline"}},
			"finish":  {ID: "finish", Mode: "auto", Input: &graphengine.Expression{Material: "slide_outline"}},
		},
		ControlEdges:      []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "outline"}, {ID: "plan", From: "outline", To: "plan"}, {ID: "finish", From: "plan", To: "finish"}, {ID: "end", From: "finish", To: "__end__"}},
		MaterialProducers: map[string]graphengine.ProducerRef{"deck_outline": {Kind: "step", StepID: "outline"}, "slide_outline": {Kind: "step", StepID: "plan"}},
	}
	must(db.Model(&orm.WorkflowRevision{}).Where("id = ?", "batch-revision").Update("compiled_graph", graph.JSON()).Error)
	putRevisionWorkflowYAML(t, db, "batch-revision", "id: batch-plugin\n")
	updates := map[string]any{"status": "waiting", "controller_host": "lazymind"}
	if provider != "native" {
		updates["controller_host"] = "external-agent"
		updates["control_protocol"] = controlpolicy.Protocol
		updates["control_binding_json"] = `{"required":true,"connector_id":"connector","driver_session_id":"driver","provider":"` + provider + `"}`
	}
	must(db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(updates).Error)
	var session orm.WorkflowSession
	must(db.First(&session, "id = ?", "batch-session").Error)
	now := time.Now().UTC()
	source := orm.WorkflowSessionStep{ID: "outline-attempt", SessionID: session.ID, StepID: "outline", TaskID: "outline-task", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now.Add(-2 * time.Minute)}
	plan := orm.WorkflowSessionStep{ID: "plan-attempt", SessionID: session.ID, StepID: "plan", TaskID: "plan-task", Attempt: 1, Status: "succeeded", Validity: "effective", ReviewRequired: true, CreatedAt: now.Add(-time.Minute)}
	must(db.Create(&source).Error)
	must(db.Create(&plan).Error)
	for _, row := range []orm.WorkflowSlotRevision{
		{ID: "old-outline", SessionID: session.ID, StepID: "outline", SlotID: "deck_outline", Slot: "deck_outline", Attempt: 1, Revision: 1, Selected: true, Validity: "effective", ProducerAttemptID: source.ID, ContentSnapshot: json.RawMessage(`{"text":"old outline"}`)},
		{ID: "old-plan", SessionID: session.ID, StepID: "plan", SlotID: "slide_outline", Slot: "slide_outline", Attempt: 1, Revision: 1, Selected: true, Validity: "effective", ProducerAttemptID: plan.ID, ContentSnapshot: json.RawMessage(`{"text":"old page plans"}`)},
	} {
		must(db.Create(&row).Error)
	}
	must(db.Create(&orm.WorkflowAttemptInputBinding{ID: "old-binding", SessionID: session.ID, AttemptID: plan.ID, MaterialID: "deck_outline", MaterialRevisionID: "old-outline", SourceType: "artifact"}).Error)
	if provider != "native" {
		must(controlstore.CreateReview(db.DB, session, plan, []string{"slide_outline"}))
	}
	before, err := displaystate.Read(db.DB, session)
	must(err)
	if !before.ApprovalRequired() || before.ApprovalStepID != "plan" {
		t.Fatalf("before edit: %+v", before)
	}
	base := 1
	replacement, err := WriteSlotRevisionWithHumanArtifact(t.Context(), db.DB, session.ID, "deck_outline", "deck_outline", "outline", 1, "single", nil, "text/markdown", json.RawMessage(`{"text":"edited outline"}`), nil, "human", &base, nil)
	must(err)
	must(db.First(&session, "id = ?", session.ID).Error)
	return db, session, replacement
}

func TestEditedInputsWaitAndRegenerateAcrossControllers(t *testing.T) {
	for _, provider := range []string{"native", "deepseek-harness", "workbuddy", "codex"} {
		t.Run(provider, func(t *testing.T) {
			db, session, replacement := editedInputCheckpoint(t, provider)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			view, err := displaystate.Read(db.DB, session)
			must(err)
			if session.Status != "waiting" || view.Status != "waiting" || view.Reason != "inputs_changed" || view.ApprovalStepID != "" {
				t.Fatalf("edited state: session=%s display=%+v", session.Status, view)
			}
			expectControlCode(t, guardRunAdvance(db.DB, session), "EDITS_PENDING_CONTINUE")
			var old orm.WorkflowSessionStep
			must(db.First(&old, "id = ?", "plan-attempt").Error)
			if old.Status != "succeeded" || old.Validity != "stale" {
				t.Fatalf("lost execution history: %+v", old)
			}
			var event orm.WorkflowEvent
			must(db.Where("session_id = ? AND event_type = ?", session.ID, "workflow.snapshot").Order("id DESC").First(&event).Error)
			var snapshot projectionResponse
			must(json.Unmarshal(event.PayloadJSON, &snapshot))
			if snapshot.Display != view || event.StateVersion != session.StateVersion || !containsProjectionStep(snapshot.Projection.Ready, "plan") || snapshot.Projection.Nodes["plan"].Validity != "stale" {
				t.Fatalf("live snapshot differs: %+v", snapshot)
			}
			if provider == "native" {
				_, err = BeginNativeControlTurn(t.Context(), db.DB, session.CreateUserID, session.ConversationID, NativeControlCommand{SessionID: session.ID, Kind: "continue", HistoryID: "new-turn", ExpectedStateVersion: session.StateVersion})
				must(err)
			} else {
				var oldReview orm.WorkflowReviewCheckpoint
				must(db.First(&oldReview, "attempt_id = ?", old.ID).Error)
				if oldReview.Status != "superseded" {
					t.Fatalf("old review=%s", oldReview.Status)
				}
				result, err := (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "regenerate", Kind: "continue", StateVersion: session.StateVersion})
				must(err)
				if result.Receipt.ActionID == "" {
					t.Fatal("host was not notified")
				}
			}
			must(db.First(&session, "id = ?", session.ID).Error)
			if session.Status != "active" {
				t.Fatalf("continuation not admitted: %s", session.Status)
			}
			if provider == "native" {
				must(db.Transaction(func(tx *gorm.DB) error {
					_, _, _, err := applyWorkflowTransition(t.Context(), tx, session.ID, transitionCommandRequest{CommandID: "new-plan", Operation: "advance", TargetStepID: "plan", ExpectedStateVersion: session.StateVersion, RetryOrigin: "user", HandOff: true})
					return err
				}))
			} else {
				_, err = (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "new-plan", Kind: "begin", StepID: "plan", StateVersion: session.StateVersion, RetryOrigin: "user"})
				must(err)
			}
			var attempt orm.WorkflowSessionStep
			must(db.Where("session_id = ? AND step_id = ? AND validity = ?", session.ID, "plan", "effective").First(&attempt).Error)
			if attempt.Attempt != 2 {
				t.Fatalf("replacement attempt=%d", attempt.Attempt)
			}
			var binding orm.WorkflowAttemptInputBinding
			must(db.Where("attempt_id = ? AND material_id = ?", attempt.ID, "deck_outline").First(&binding).Error)
			if binding.MaterialRevisionID != replacement.ID {
				t.Fatalf("used old input: %+v", binding)
			}
			must(db.Model(&attempt).Update("status", "succeeded").Error)
			attempt.Status = "succeeded"
			must(db.Create(&orm.WorkflowSlotRevision{ID: "new-plan-output", SessionID: session.ID, StepID: "plan", SlotID: "slide_outline", Slot: "slide_outline", Attempt: 2, Revision: 2, Selected: true, Validity: "effective", ProducerAttemptID: attempt.ID, ContentSnapshot: json.RawMessage(`{"text":"updated page plans"}`)}).Error)
			must(controlstore.CreateReview(db.DB, session, attempt, []string{"slide_outline"}))
			must(db.Model(&session).Update("status", "waiting").Error)
			session.Status = "waiting"
			after, err := displaystate.Read(db.DB, session)
			must(err)
			if !after.ApprovalRequired() || after.ApprovalStepID != "plan" {
				t.Fatalf("replacement did not receive fresh review: %+v", after)
			}
		})
	}
}

func TestEditedInputsDoNotMaskExecutionOrHistoricalStates(t *testing.T) {
	for _, scenario := range []string{"running", "failed", "stopped", "replacement-failed", "replacement-succeeded", "pruned"} {
		t.Run(scenario, func(t *testing.T) {
			db, session, _ := editedInputCheckpoint(t, "deepseek-harness")
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			want := "failed"
			switch scenario {
			case "failed", "stopped":
				session.Status = scenario
				must(db.Model(&session).Update("status", scenario).Error)
				if scenario == "stopped" {
					want = "paused"
				}
			case "running":
				want = "running"
				must(db.Create(&orm.WorkflowSessionStep{ID: "parallel", SessionID: session.ID, StepID: "parallel", Status: "running", Validity: "effective", Attempt: 1, CreatedAt: time.Now().UTC()}).Error)
			case "replacement-failed", "replacement-succeeded":
				status := "failed"
				if scenario == "replacement-succeeded" {
					status = "succeeded"
				}
				must(db.Create(&orm.WorkflowSessionStep{ID: "replacement", SessionID: session.ID, StepID: "plan", Status: status, Validity: "effective", Attempt: 2, CreatedAt: time.Now().UTC()}).Error)
			case "pruned":
				must(db.Create(&orm.WorkflowRouteDecision{ID: "other-route", SessionID: session.ID, FromStepID: "outline", Validity: "effective", ActivatedJSON: json.RawMessage(`[]`), PrunedJSON: json.RawMessage(`["plan"]`), BypassedJSON: json.RawMessage(`[]`), WitnessJSON: json.RawMessage(`[]`)}).Error)
			}
			view, err := displaystate.Read(db.DB, session)
			must(err)
			if view.Status != want || view.Reason == "inputs_changed" {
				t.Fatalf("state=%+v want=%s", view, want)
			}
		})
	}
}
