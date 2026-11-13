package hosted

import (
	"encoding/json"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/execution"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
)

// This exercises the same revision through native and external grant, artifact
// and completion boundaries. No model or tool server is configured or started.
func TestExternalProfileNativeAndExternalLifecycle(t *testing.T) {
	for _, host := range []string{"external-agent", "lazymind"} {
		t.Run(host, func(t *testing.T) {
			service, db := hostedTestService(t)
			service.Completion = &execution.Service{DB: db, Store: service.Store, Attempts: service.Attempts, Contexts: service.Contexts}
			if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowApprovalPreference{}); err != nil {
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
			node := graph.Nodes["write"]
			node.LegacyTools, node.TerminalTools, node.ToolsOnly = []string{"native_generator"}, []string{"native_generator"}, true
			node.Execution = &graphengine.StepExecution{Version: "1", Tools: []graphengine.ExecutionTool{{Name: "inspect", Source: "package"}}}
			graph.Nodes["write"] = node
			graph.Runtime.PostStepChecks = []graphengine.PostStepCheck{{StepID: "write", Tool: "native_check"}}
			graph.Runtime.TransactionalOutputs = true
			graph.Runtime.PublisherOwnedSlots = []string{"report"}
			if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Update("control_protocol", controlpolicy.Protocol).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("executor_host", host).Error; err != nil {
				t.Fatal(err)
			}
			assertSelected := func(want bool) {
				t.Helper()
				var revision orm.WorkflowSlotRevision
				if err := db.Where("producer_attempt_id = ? AND slot_id = ?", "attempt-1", "report").First(&revision).Error; err != nil {
					t.Fatal(err)
				}
				if revision.Selected != want {
					t.Fatalf("transactional report selected = %v, want %v", revision.Selected, want)
				}
			}
			grant, err := service.Begin(t.Context(), "owner", "session-1", "attempt-1")
			if err != nil {
				t.Fatal(err)
			}
			if host == "lazymind" {
				if grant.ExecutionHandle != "" || grant.ExecutorHost != host {
					t.Fatal("external agent took ownership of native execution")
				}
				claim, err := service.Attempts.ClaimForHost(t.Context(), "native-worker", host)
				if err != nil {
					t.Fatal(err)
				}
				contract, err := service.Contexts.LoadAttemptContext(t.Context(), claim.AttemptID)
				if err != nil || contract.Execution == nil || !contract.ToolsOnly || contract.Prompt != "write report" {
					t.Fatalf("native contract: %+v %v", contract, err)
				}
				contract.ExecutionHandle = claim.LeaseToken
				input := successfulSubmission(claim.LeaseToken)
				for _, artifact := range input.Artifacts {
					if err := service.Artifacts.Save(t.Context(), contract, artifact); err != nil {
						t.Fatal(err)
					}
				}
				assertSelected(false)
				if _, err := service.Completion.Complete(t.Context(), "owner", "session-1", claim.AttemptID, executor.Completion{ExecutionHandle: claim.LeaseToken, Outcome: "succeeded"}); err != nil {
					t.Fatal(err)
				}
				assertSelected(true)
				return
			}
			if grant.ExecutionHandle == "" || grant.StepContract.Execution == nil || grant.StepContract.ToolsOnly {
				t.Fatalf("external grant: %+v", grant)
			}
			if _, _, err := executor.AuthorizeExternalToolCall(t.Context(), db, "owner", "session-1", "attempt-1", grant.ExecutionHandle, "inspect"); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Complete(t.Context(), "owner", "session-1", "attempt-1", executor.Completion{ExecutionHandle: grant.ExecutionHandle, Outcome: "succeeded"}); err == nil {
				t.Fatal("external completion accepted missing outputs")
			}
			// External validation does not require a native post-step check receipt.
			input := successfulSubmission(grant.ExecutionHandle)
			for _, artifact := range input.Artifacts {
				if err := service.Publish(t.Context(), "owner", "session-1", "attempt-1", Publication{ExecutionHandle: grant.ExecutionHandle, Artifact: artifact}); err != nil {
					t.Fatal(err)
				}
			}
			assertSelected(false)
			result, err := service.Complete(t.Context(), "owner", "session-1", "attempt-1", executor.Completion{ExecutionHandle: grant.ExecutionHandle, Outcome: "succeeded"})
			if err != nil || result.AttemptStatus != "succeeded" {
				t.Fatalf("complete: %+v %v", result, err)
			}
			assertSelected(true)
			if _, err := executor.AuthorizeExternalExecution(t.Context(), db, "owner", "session-1", "attempt-1", grant.ExecutionHandle); err == nil {
				t.Fatal("completed execution retained tool authority")
			}
			if replay, err := publishAndComplete(service, t.Context(), "owner", "session-1", "attempt-1", input); err != nil || !replay.AlreadyTerminal {
				t.Fatalf("idempotent replay: %+v %v", replay, err)
			}
		})
	}
}
