package workflow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/graphengine"
)

// Exercise admission, leased execution and finalization rather than constructing
// a successful graph with an already incorrect session status.
func TestRetrySuccessKeepsRunActiveUntilGraphCompletion(t *testing.T) {
	for _, variant := range []string{"native", "external_controller_native_executor", "external_controller_external_executor"} {
		for _, scenario := range []string{"success_only", "automatic_retry", "manual_retry"} {
			t.Run(variant+"/"+scenario, func(t *testing.T) {
				controller, executorHost := "external-agent", "lazymind"
				tools := []string{"image_generator"}
				if variant == "native" {
					controller = "lazymind"
				}
				if variant == "external_controller_external_executor" {
					executorHost, tools = "external-agent", nil
				}
				db, hash := setupBatchTransitionSession(t)
				if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowCommand{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}); err != nil {
					t.Fatal(err)
				}
				putRevisionWorkflowYAML(t, db, "batch-revision", "id: batch-plugin\n")
				steps := []string{"generate_backgrounds", "build_outline", "plan_page_prompts", "generate_ppt"}
				graph := graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion, GraphHash: hash, StartRoute: "all", Nodes: map[string]graphengine.CompiledNode{}}
				previous := "__start__"
				for _, id := range steps {
					graph.Nodes[id] = graphengine.CompiledNode{ID: id, Mode: "auto", Route: "all", LegacyTools: tools}
					graph.ControlEdges = append(graph.ControlEdges, graphengine.CompiledEdge{ID: previous + id, From: previous, To: id})
					previous = id
				}
				graph.ControlEdges = append(graph.ControlEdges, graphengine.CompiledEdge{ID: "end", From: previous, To: "__end__"})
				if err := db.Model(&orm.WorkflowRevision{}).Where("id = ?", "batch-revision").Update("compiled_graph", graph.JSON()).Error; err != nil {
					t.Fatal(err)
				}
				protocol := ""
				if controller == "external-agent" {
					protocol = controlpolicy.Protocol
				}
				if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Updates(map[string]any{"controller_host": controller, "control_protocol": protocol, "workflow_mode": "dynamic"}).Error; err != nil {
					t.Fatal(err)
				}
				oldDB, oldState := store.DB(), store.State()
				store.Init(db.DB, db.DB, nil)
				t.Cleanup(func() { store.Init(oldDB, oldDB, oldState) })
				service := WorkflowControlService{DB: db.DB}
				executions := attempt.New(db.DB, attempt.Config{})
				command := 0
				begin := func(step string) string {
					t.Helper()
					session, err := GetSession(t.Context(), db.DB, "batch-session")
					if err != nil {
						t.Fatal(err)
					}
					command++
					executionID := ""
					manualRetry := scenario == "manual_retry" && command == 2
					if controller == "external-agent" {
						kind := "begin"
						if manualRetry {
							kind = "retry"
						}
						result, err := service.Execute(t.Context(), "batch-user", session.ID, WorkflowControlCommand{CommandID: fmt.Sprintf("retry-status-%d", command), Kind: kind, StepID: step, StateVersion: session.StateVersion})
						if err != nil {
							t.Fatal(err)
						}
						executionID = result.Receipt.ExecutionID
					} else {
						operation, origin := "advance", "automatic"
						if manualRetry {
							operation, origin = "retry", "user"
						}
						result, status := transitionWorkflowSession(t.Context(), db.DB, session.ID, transitionCommandRequest{CommandID: fmt.Sprintf("retry-status-%d", command), Operation: operation, RetryOrigin: origin, TargetStepID: step, ExpectedStateVersion: session.StateVersion, HandOff: true})
						if status != http.StatusOK || !result.Accepted {
							t.Fatalf("native admission: %+v status=%d", result, status)
						}
						var row orm.WorkflowSessionStep
						if err := db.Where("task_id = ?", result.TaskID).First(&row).Error; err != nil {
							t.Fatal(err)
						}
						executionID = row.ID
					}
					latest, err := GetSession(t.Context(), db.DB, session.ID)
					if err != nil {
						t.Fatal(err)
					}
					if latest.Status != "active" {
						t.Errorf("admitted %s: session=%s, want active", step, latest.Status)
					}
					return executionID
				}
				settle := func(executionID, step, status string) {
					t.Helper()
					claim, err := executions.ClaimAttemptForHost(t.Context(), executionID, "retry-status-worker", executorHost)
					if err != nil {
						t.Fatal(err)
					}
					if err := executions.WithControlTransaction(db.DB).Terminal(t.Context(), executionID, claim.LeaseToken, status, "", json.RawMessage(`{}`)); err != nil {
						t.Fatal(err)
					}
					if err := FinalizeHostAttempt(t.Context(), db.DB, "batch-session", step, executionID, status); err != nil {
						t.Fatal(err)
					}
				}
				first := begin(steps[0])
				if scenario != "success_only" {
					settle(first, steps[0], "failed")
					session, err := GetSession(t.Context(), db.DB, "batch-session")
					if err != nil || session.Status != "failed" {
						t.Fatalf("failure not reflected in session: %+v %v", session, err)
					}
					old := first
					first = begin(steps[0])
					var prior orm.WorkflowSessionStep
					if err := db.First(&prior, "id = ?", old).Error; err != nil || prior.Validity != "stale" {
						t.Fatalf("retry did not supersede old attempt: %+v %v", prior, err)
					}
				}
				for i, id := range steps {
					executionID := first
					if i > 0 {
						executionID = begin(id)
					}
					settle(executionID, id, "succeeded")
					session, err := GetSession(t.Context(), db.DB, "batch-session")
					if err != nil {
						t.Fatal(err)
					}
					projected, err := projectSession(t.Context(), db.DB, session)
					if err != nil {
						t.Fatal(err)
					}
					wantSession, wantDisplay, wantReason := "active", "running", "preparing"
					if i == len(steps)-1 {
						wantSession, wantDisplay, wantReason = "completed", "completed", "completed"
					} else if len(projected.Projection.Ready) != 1 || projected.Projection.Ready[0] != steps[i+1] {
						t.Fatalf("after %s: unexpected ready steps %v", id, projected.Projection.Ready)
					}
					if session.Status != wantSession || projected.Display.Status != wantDisplay || projected.Display.Reason != wantReason || projected.Projection.Nodes[id].Execution != "succeeded" {
						t.Errorf("after %s: session=%s display=%+v node=%s", id, session.Status, projected.Display, projected.Projection.Nodes[id].Execution)
					}
				}
			})
		}
	}
}
