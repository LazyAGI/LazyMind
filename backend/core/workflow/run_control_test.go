package workflow

import (
	"testing"

	"lazymind/core/common/orm"
	workflowattempt "lazymind/core/workflow/attempt"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/graphengine"
)

func TestControlledAdvanceRequiresReviewWhileSiblingDrains(t *testing.T) {
	for _, provider := range []string{"deepseek-harness", "workbuddy", "codex"} {
		for _, siblingStatus := range []string{"pending", "queued", "claimed", "running"} {
			for _, operation := range []string{"execute", "retry"} {
				t.Run(provider+"/"+siblingStatus+"/"+operation, func(t *testing.T) {
					must := func(err error) {
						t.Helper()
						if err != nil {
							t.Fatal(err)
						}
					}
					graph := graphengine.CompiledStateGraph{StartRoute: "all", Nodes: map[string]graphengine.CompiledNode{}}
					for _, id := range []string{"review", "sibling", "target"} {
						mode := "auto"
						if id == "review" {
							mode = "human"
						}
						graph.Nodes[id] = graphengine.CompiledNode{ID: id, Mode: mode}
						graph.ControlEdges = append(graph.ControlEdges,
							graphengine.CompiledEdge{ID: "start-" + id, From: "__start__", To: id},
							graphengine.CompiledEdge{ID: "end-" + id, From: id, To: "__end__"})
					}
					db, session := newControllerTestSession(t, provider, graph)
					reviewed := orm.WorkflowSessionStep{ID: "reviewed", TaskID: "reviewed-task", SessionID: session.ID, StepID: "review", Attempt: 1, Status: "succeeded", Validity: "effective", ReviewRequired: true}
					must(db.Create(&reviewed).Error)
					must(controlstore.CreateReview(db.DB, session, reviewed, nil))
					must(db.Create(&orm.WorkflowSessionStep{ID: "sibling", TaskID: "sibling-task", SessionID: session.ID, StepID: "sibling", Attempt: 1, Status: siblingStatus, Validity: "effective", ExecutorHost: "external-agent"}).Error)
					var wantAttempts int64
					if operation == "retry" {
						must(db.Create(&orm.WorkflowSessionStep{ID: "failed", TaskID: "failed-task", SessionID: session.ID, StepID: "target", Attempt: 1, Status: "failed", Validity: "effective"}).Error)
						wantAttempts = 1
					}
					state, err := controlstore.Read(db.DB, session)
					must(err)
					if state.Continuation != "draining" || state.Admission.CanBegin || state.Admission.Reason != "review_pending" {
						t.Fatalf("expected review barrier with draining sibling: %+v", state)
					}

					svc := WorkflowControlService{DB: db.DB}
					_, err = svc.Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "blocked", Kind: "begin", StepID: "target", StateVersion: session.StateVersion})
					expectControlCode(t, err, "WORKFLOW_APPROVAL_REQUIRED")
					var count int64
					must(db.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND step_id = ?", session.ID, "target").Count(&count).Error)
					if count != wantAttempts {
						t.Fatalf("review barrier admitted new work: attempts=%d want %d", count, wantAttempts)
					}
					version := session.StateVersion
					must(db.First(&session, "id = ?", session.ID).Error)
					if session.StateVersion != version {
						t.Fatalf("rejected advance changed session version: got %d want %d", session.StateVersion, version)
					}
					if operation == "retry" {
						var failed orm.WorkflowSessionStep
						must(db.First(&failed, "id = ?", "failed").Error)
						if failed.Validity != "effective" {
							t.Fatalf("rejected retry invalidated the previous attempt: %+v", failed)
						}
					}
					if siblingStatus == "queued" {
						_, err = workflowattempt.New(db.DB, workflowattempt.Config{}).ClaimQueuedAttemptForHost(t.Context(), "sibling", "executor", "external-agent")
						must(err)
					}

					var review orm.WorkflowReviewCheckpoint
					must(db.First(&review, "attempt_id = ?", reviewed.ID).Error)
					if review.Status != "pending" {
						t.Fatalf("advance cleared pending review: %+v", review)
					}
					_, err = svc.Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "approve", Kind: "confirm", ReviewID: review.ID, ReviewVersion: review.Version, ManifestHash: review.ManifestHash})
					must(err)
					must(db.First(&session, "id = ?", session.ID).Error)
					result, err := svc.Execute(t.Context(), session.CreateUserID, session.ID, WorkflowControlCommand{CommandID: "allowed", Kind: "begin", StepID: "target", StateVersion: session.StateVersion})
					must(err)
					if result.Receipt.ExecutionID == "" {
						t.Fatal("approved workflow did not grant the target execution")
					}
				})
			}
		}
	}
}
