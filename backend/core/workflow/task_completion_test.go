package workflow

import (
	"encoding/json"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/subagent"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/graphengine"
)

func TestNativeTaskHandoffKeepsSharedPreparation(t *testing.T) {
	for _, controller := range []string{"dynamic", "inline", "scheduler"} {
		t.Run(controller, func(t *testing.T) {
			graph := graphengine.CompiledStateGraph{StartRoute: "all",
				Nodes:        map[string]graphengine.CompiledNode{"first": {ID: "first", Mode: "auto"}, "next": {ID: "next", Mode: "auto"}},
				ControlEdges: []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "first"}, {ID: "next", From: "first", To: "next"}, {ID: "end", From: "next", To: "__end__"}},
			}
			db, session := newControllerTestSession(t, "native", graph)
			now := time.Now().UTC()
			step := orm.WorkflowSessionStep{ID: "local-attempt", TaskID: "local-task", SessionID: session.ID,
				StepID: "first", Attempt: 1, Status: "running", Validity: "effective", ExecutorHost: "lazymind", CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&step).Error; err != nil {
				t.Fatal(err)
			}
			handOff := controller != "inline"
			pctx := &WorkflowChatContext{SessionID: session.ID, StepID: "first", WorkflowID: session.WorkflowID,
				UserID: session.CreateUserID, WorkflowMode: "dynamic", HandOff: &handOff}
			if controller == "scheduler" {
				pctx.HostedTaskID, pctx.WorkflowMode = "scheduler-task", "auto"
			}
			var callbackErrors []string
			OnSubAgentDone(t.Context(), db.DB, nil, step.TaskID, subagent.StatusSucceeded, "result saved",
				func(kind string, payload map[string]any) {
					if kind == "workflow_error" {
						callbackErrors = append(callbackErrors, payload["message"].(string))
					}
				}, pctx)
			if len(callbackErrors) != 0 {
				t.Fatalf("completion errors: %v", callbackErrors)
			}
			if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
				t.Fatal(err)
			}
			view, err := displaystate.Read(db.DB, session)
			if err != nil || session.Status != "active" || view.Status != "running" || view.Reason != "preparing" {
				t.Fatalf("handoff changed graph state: run=%s view=%+v err=%v", session.Status, view, err)
			}
			// Even an older adapter asking for waiting uses the same pinned graph.
			if err := UpdateSessionStatus(t.Context(), db.DB, session.ID, SessionStatusWaiting); err != nil {
				t.Fatal(err)
			}
			var events []orm.WorkflowEvent
			if err := db.Order("id").Find(&events).Error; err != nil {
				t.Fatal(err)
			}
			seen := false
			for _, event := range events {
				if event.EventType != "workflow.snapshot" && event.EventType != "workflow.patch" {
					continue
				}
				var payload struct {
					Display displaystate.View `json:"display"`
				}
				if err := json.Unmarshal(event.PayloadJSON, &payload); err != nil {
					t.Fatal(err)
				}
				seen = true
				if payload.Display.Status != "running" {
					t.Fatalf("transient handoff state: %s", event.PayloadJSON)
				}
			}
			if !seen {
				t.Fatal("no durable workflow state event")
			}
		})
	}
}

func TestControlledTaskIsNotHealedFromLegacyTaskState(t *testing.T) {
	graph := graphengine.CompiledStateGraph{StartRoute: "all", Nodes: map[string]graphengine.CompiledNode{"work": {ID: "work", Mode: "auto"}},
		ControlEdges: []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "work"}, {ID: "end", From: "work", To: "__end__"}}}
	db, session := newControllerTestSession(t, "deepseek-harness", graph)
	old := time.Now().UTC().Add(-10 * time.Minute)
	if err := db.Model(&session).Update("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	session.UpdatedAt = old
	step := orm.WorkflowSessionStep{ID: "leased", SessionID: session.ID, StepID: "work", TaskID: "task", Attempt: 1,
		Status: "running", Validity: "effective", ExecutorHost: "lazymind", LeaseToken: "live-handle"}
	if err := db.Create(&step).Error; err != nil {
		t.Fatal(err)
	}
	task, err := subagent.CreateTask(t.Context(), db.DB, subagent.CreateTaskInput{TaskID: "task", ConversationID: session.ConversationID,
		CreateUserID: session.CreateUserID, AgentType: "workflow_step"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(task).Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	healStaleActiveSession(t.Context(), db.DB, &session)
	var actual orm.WorkflowSessionStep
	if err := db.First(&actual, "id = ?", step.ID).Error; err != nil {
		t.Fatal(err)
	}
	if actual.Status != "running" || actual.LeaseToken != "live-handle" || session.Status != "active" {
		t.Fatalf("legacy recovery bypassed execution handle: step=%+v run=%s", actual, session.Status)
	}
}

func TestNativeOrphanRecoveryKeepsSharedPreparation(t *testing.T) {
	graph := graphengine.CompiledStateGraph{StartRoute: "all",
		Nodes:        map[string]graphengine.CompiledNode{"work": {ID: "work", Mode: "auto"}, "next": {ID: "next", Mode: "auto"}},
		ControlEdges: []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "work"}, {ID: "next", From: "work", To: "next"}, {ID: "end", From: "next", To: "__end__"}}}
	db, session := newControllerTestSession(t, "native", graph)
	old := time.Now().UTC().Add(-10 * time.Minute)
	if err := db.Model(&session).Update("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	session.UpdatedAt = old
	if err := db.Create(&orm.WorkflowSessionStep{ID: "orphan", SessionID: session.ID, StepID: "work", TaskID: "task", Attempt: 1,
		Status: "running", Validity: "effective", CreatedAt: old, UpdatedAt: old}).Error; err != nil {
		t.Fatal(err)
	}
	task, err := subagent.CreateTask(t.Context(), db.DB, subagent.CreateTaskInput{TaskID: "task", ConversationID: session.ConversationID,
		CreateUserID: session.CreateUserID, AgentType: "workflow_step"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(task).Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	healStaleActiveSession(t.Context(), db.DB, &session)
	var step orm.WorkflowSessionStep
	if err := db.First(&step, "id = ?", "orphan").Error; err != nil {
		t.Fatal(err)
	}
	view, err := displaystate.Read(db.DB, session)
	if err != nil || step.Status != "succeeded" || session.Status != "active" || view.Reason != "preparing" {
		t.Fatalf("recovery differs from live completion: run=%s step=%s view=%+v err=%v", session.Status, step.Status, view, err)
	}
}

func TestFinalizationPreservesNativePauseBeforeCallback(t *testing.T) {
	graph := graphengine.CompiledStateGraph{StartRoute: "all", Nodes: map[string]graphengine.CompiledNode{"work": {ID: "work", Mode: "auto"}},
		ControlEdges: []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "work"}, {ID: "end", From: "work", To: "__end__"}}}
	db, session := newControllerTestSession(t, "native", graph)
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSessionStep{ID: "done", SessionID: session.ID, StepID: "work", TaskID: "task", Attempt: 1,
		Status: "succeeded", Validity: "effective", CreatedAt: now.Add(-time.Second), UpdatedAt: now.Add(-time.Second)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&session).Updates(map[string]any{"status": "waiting", "last_stopped_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := FinalizeHostAttempt(t.Context(), db.DB, session.ID, "work", "done", "succeeded"); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	view, err := displaystate.Read(db.DB, session)
	if err != nil || session.Status != "waiting" || view.Status != "paused" {
		t.Fatalf("completion overrode explicit pause: run=%s view=%+v err=%v", session.Status, view, err)
	}
}

func TestInputCheckpointPreventsCompletionUntilAnswered(t *testing.T) {
	graph := graphengine.CompiledStateGraph{StartRoute: "all", Nodes: map[string]graphengine.CompiledNode{"collect": {ID: "collect", Mode: "auto"}}, ControlEdges: []graphengine.CompiledEdge{{From: "__start__", To: "collect"}, {From: "collect", To: "__end__"}}, Runtime: graphengine.RuntimePolicy{InputCheckpoint: &graphengine.InputCheckpoint{AfterStep: "collect", Slot: "questions", BeforeSteps: []string{"outline"}}}}
	db, session := newControllerTestSession(t, "native", graph)
	if err := db.AutoMigrate(&orm.WorkflowHumanArtifact{}, &orm.WorkflowSlotRevision{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSessionStep{ID: "input-attempt", SessionID: session.ID, StepID: "collect", TaskID: "input-task", Status: "succeeded", Validity: "effective", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	slot := orm.WorkflowSlotRevision{ID: "input-slot", SessionID: session.ID, SlotID: "questions", Slot: "questions", Selected: true, Validity: "effective", ContentSnapshot: json.RawMessage(`{"checkpoint_token":"card","questions":[{"text":"Date?"}]}`)}
	if err := db.Create(&slot).Error; err != nil {
		t.Fatal(err)
	}
	session.Status = "active"
	projected, err := projectSession(t.Context(), db.DB, &session)
	if err != nil || projected.Display.Status != "waiting_input" || projected.Projection.Completed {
		t.Fatalf("unanswered input cannot complete: %+v %v", projected, err)
	}
	if err := db.Model(&slot).Update("content_snapshot", json.RawMessage(`{"checkpoint_token":"card","questions":[{"text":"Date?"}],"answer_text":"Tomorrow"}`)).Error; err != nil {
		t.Fatal(err)
	}
	projected, err = projectSession(t.Context(), db.DB, &session)
	if err != nil || !projected.Projection.Completed {
		t.Fatalf("answered final checkpoint can complete: %+v %v", projected, err)
	}
}
