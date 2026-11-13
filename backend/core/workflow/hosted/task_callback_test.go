package hosted

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/displaystate"
	"lazymind/core/workflow/execution"
	"lazymind/core/workflow/graphengine"
)

func callbackService(t *testing.T, provider, mode, scenario string) (*Service, *gorm.DB, attempt.Claim) {
	t.Helper()
	s, db := hostedTestService(t)
	s.Completion = &execution.Service{DB: db, Store: s.Store, Attempts: s.Attempts, Contexts: s.Contexts}
	if err := db.AutoMigrate(&orm.WorkflowResource{}, &orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowTransitionCommand{}); err != nil {
		t.Fatal(err)
	}
	binding, _ := json.Marshal(map[string]any{"provider": provider, "connector_id": "connector", "driver_session_id": "driver"})
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Updates(map[string]any{
		"control_protocol": controlpolicy.Protocol, "control_binding_json": string(binding), "workflow_mode": mode,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Updates(map[string]any{
		"executor_host": "lazymind", "task_id": "native-task", "review_required": scenario == "review",
	}).Error; err != nil {
		t.Fatal(err)
	}
	var revision orm.WorkflowRevision
	if err := db.First(&revision, "id = ?", "revision-1").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	if scenario != "final" {
		graph.Nodes["next"] = graphengine.CompiledNode{ID: "next", Mode: "auto", Prompt: "use the saved report"}
		graph.ControlEdges[1].To = "next"
		graph.ControlEdges = append(graph.ControlEdges, graphengine.CompiledEdge{ID: "next-end", From: "next", To: "__end__"})
	}
	if scenario == "review" {
		node := graph.Nodes["write"]
		node.Mode = "human"
		graph.Nodes["write"] = node
	}
	if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	yaml := []byte("id: writer\nslots:\n  - id: report\n    type: text\n")
	hash := "callback-workflow-yaml"
	if err := db.Create(&orm.WorkflowBlob{Hash: hash, Content: yaml, Size: int64(len(yaml))}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: revision.ID, Path: "workflow.yaml", EntryType: "file", BlobHash: &hash}).Error; err != nil {
		t.Fatal(err)
	}
	claim, err := s.Attempts.ClaimForHost(t.Context(), "native", "lazymind")
	if err != nil {
		t.Fatal(err)
	}
	return s, db, claim
}

func callbackState(t *testing.T, db *gorm.DB) []byte {
	t.Helper()
	state := map[string]any{}
	for _, model := range []struct {
		name  string
		value any
	}{
		{"sessions", &orm.WorkflowSession{}}, {"attempts", &orm.WorkflowSessionStep{}},
		{"events", &orm.WorkflowEvent{}}, {"routes", &orm.WorkflowRouteDecision{}},
		{"reviews", &orm.WorkflowReviewCheckpoint{}}, {"actions", &orm.WorkflowHostAction{}},
		{"revisions", &orm.WorkflowSlotRevision{}},
	} {
		var rows []map[string]any
		if err := db.Model(model.value).Order("id").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		state[model.name] = rows
	}
	result, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLocalTaskCallbackPreservesControllerCompletion(t *testing.T) {
	for _, provider := range []string{"deepseek-harness", "workbuddy", "codex"} {
		for _, mode := range []string{"dynamic", "auto"} {
			for _, scenario := range []string{"next", "review", "final", "failed"} {
				t.Run(provider+"/"+mode+"/"+scenario, func(t *testing.T) {
					s, db, claim := callbackService(t, provider, mode, scenario)
					handOff := true
					pctx := &workflowcore.WorkflowChatContext{SessionID: "session-1", StepID: "write", WorkflowID: "writer", WorkflowMode: mode, HandOff: &handOff}
					var feedback int
					callback := func(status string) {
						workflowcore.OnSubAgentDone(t.Context(), db, nil, "native-task", status, "native result", func(kind string, payload map[string]any) {
							if kind != "workflow_step_feedback" {
								t.Errorf("legacy callback took over execution: %s %v", kind, payload)
							}
							feedback++
						}, pctx)
					}
					outcome, want := "succeeded", "running"
					if scenario == "review" {
						want = "waiting"
					}
					if scenario == "final" {
						want = "completed"
					}
					if scenario == "failed" {
						outcome, want = "failed", "failed"
					}
					if outcome == "succeeded" {
						contract, err := s.Contexts.LoadAttemptContext(t.Context(), claim.AttemptID)
						if err != nil {
							t.Fatal(err)
						}
						contract.ExecutionHandle = claim.LeaseToken
						for _, artifact := range successfulSubmission(claim.LeaseToken).Artifacts {
							if err := s.Artifacts.Save(t.Context(), contract, artifact); err != nil {
								t.Fatal(err)
							}
						}
					}
					if err := finishNative(s, t.Context(), claim.AttemptID, claim.LeaseToken, outcome, "", json.RawMessage(`{"summary":"native result"}`)); err != nil {
						t.Fatal(err)
					}
					before := callbackState(t, db)
					callback(outcome)
					callback(outcome)
					if feedback != 2 || !bytes.Equal(before, callbackState(t, db)) {
						var current orm.WorkflowSession
						db.First(&current, "id = ?", "session-1")
						view, _ := displaystate.Read(db, current)
						t.Fatalf("notification replay changed authoritative state: run=%s display=%+v feedback=%d", current.Status, view, feedback)
					}
					for _, status := range []string{"waiting", "failed", "active", "completed"} {
						if err := workflowcore.UpdateSessionStatus(t.Context(), db, "session-1", status); err != nil {
							t.Fatal(err)
						}
						if err := workflowcore.UpdateStepStatus(t.Context(), db, "native-task", status); err != nil {
							t.Fatal(err)
						}
					}
					if !bytes.Equal(before, callbackState(t, db)) {
						t.Fatal("legacy status writer changed a controlled execution")
					}
					var session orm.WorkflowSession
					if err := db.First(&session, "id = ?", "session-1").Error; err != nil {
						t.Fatal(err)
					}
					view, err := displaystate.Read(db, session)
					if err != nil || view.Status != want {
						t.Fatalf("display=%+v want=%s err=%v", view, want, err)
					}
					var events []orm.WorkflowEvent
					if err := db.Order("id").Find(&events).Error; err != nil {
						t.Fatal(err)
					}
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
						if outcome == "succeeded" && payload.Display.Status == "failed" {
							t.Fatalf("transient failure event: %s", event.PayloadJSON)
						}
					}
					if scenario == "next" {
						if view.Reason != "preparing" {
							t.Fatalf("handoff lost preparation: %+v", view)
						}
						_, err := (workflowcore.WorkflowControlService{DB: db}).Execute(t.Context(), "owner", session.ID,
							workflowcore.WorkflowControlCommand{CommandID: "begin-next", Kind: "begin", StepID: "next", StateVersion: session.StateVersion})
						if err != nil {
							t.Fatalf("next execution could not start: %v", err)
						}
						before = callbackState(t, db)
						callback("failed")
						callback("succeeded")
						if !bytes.Equal(before, callbackState(t, db)) {
							t.Fatal("old callback changed the next execution")
						}
					}
				})
			}
		}
	}
}

func TestLocalTaskNotificationCannotReplaceLeasedCompletion(t *testing.T) {
	_, db, _ := callbackService(t, "deepseek-harness", "dynamic", "next")
	before := callbackState(t, db)
	workflowcore.OnSubAgentDone(t.Context(), db, nil, "native-task", "failed", "stream disconnected",
		func(kind string, payload map[string]any) {
			t.Errorf("uncommitted result was published: %s %v", kind, payload)
		},
		&workflowcore.WorkflowChatContext{SessionID: "session-1", StepID: "write", WorkflowID: "writer", WorkflowMode: "dynamic"})
	if !bytes.Equal(before, callbackState(t, db)) {
		t.Fatal("notification bypassed the execution handle")
	}
}

func TestLocalTaskNotificationCannotUndoStop(t *testing.T) {
	s, db, claim := callbackService(t, "deepseek-harness", "dynamic", "next")
	if err := db.AutoMigrate(&orm.SubAgentTask{}); err != nil {
		t.Fatal(err)
	}
	if _, err := (workflowcore.WorkflowControlService{DB: db}).Execute(t.Context(), "owner", "session-1",
		workflowcore.WorkflowControlCommand{CommandID: "stop", Kind: "stop"}); err != nil {
		t.Fatal(err)
	}
	if err := finishNative(s, t.Context(), claim.AttemptID, claim.LeaseToken, "succeeded", "", json.RawMessage(`{"summary":"late result"}`)); err == nil {
		t.Fatal("stopped execution accepted its old handle")
	}
	before := callbackState(t, db)
	for _, status := range []string{"succeeded", "failed", "interrupted"} {
		workflowcore.OnSubAgentDone(t.Context(), db, nil, "native-task", status, "late result",
			func(kind string, payload map[string]any) { t.Errorf("stopped callback emitted %s %v", kind, payload) },
			&workflowcore.WorkflowChatContext{SessionID: "session-1", StepID: "write", WorkflowID: "writer", WorkflowMode: "dynamic"})
		if err := workflowcore.UpdateSessionStatus(t.Context(), db, "session-1", "active"); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(before, callbackState(t, db)) {
		t.Fatal("callback changed stop state or delivery")
	}
}

func TestLocalTaskNotificationReadFailureDoesNotPublishExecutionFailure(t *testing.T) {
	_, db, _ := callbackService(t, "deepseek-harness", "dynamic", "next")
	before := callbackState(t, db)
	const name = "test:notification-read-failure"
	if err := db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "plugin_session_steps" {
			tx.AddError(errors.New("notification read unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Query().Remove(name) })
	workflowcore.OnSubAgentDone(t.Context(), db, nil, "native-task", "succeeded", "native result",
		func(kind string, payload map[string]any) {
			t.Errorf("read failure became an execution event: %s %v", kind, payload)
		},
		&workflowcore.WorkflowChatContext{SessionID: "session-1", StepID: "write", WorkflowID: "writer", WorkflowMode: "dynamic"})
	db.Callback().Query().Remove(name)
	if !bytes.Equal(before, callbackState(t, db)) {
		t.Fatal("read failure changed execution state")
	}
}
