package displaystate

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestExternalChatCompletionDoesNotCompleteAnUnstartedWorkflow(t *testing.T) {
	for _, sample := range []struct {
		name, runStatus, stepStatus, sessionStatus, resumedStatus, want, reason string
	}{
		{"driver preparing", "running", "", "active", "", "running", "preparing"},
		{"driver completed before begin", "completed", "", "active", "", "failed", "execution_not_started"},
		{"driver failed before begin", "failed", "", "active", "", "failed", "execution_not_started"},
		{"driver stopped before begin", "stopped", "", "active", "", "paused", "user_stopped"},
		{"pending continuation", "completed", "", "active", "pending", "running", "preparing"},
		{"running continuation", "completed", "", "active", "running", "running", "preparing"},
		{"ended continuation", "completed", "", "active", "completed", "failed", "execution_not_started"},
		{"failed continuation", "completed", "succeeded", "active", "failed", "failed", "external_driver_failed"},
		{"stopped continuation", "completed", "succeeded", "active", "stopped", "paused", "user_stopped"},
		{"successful step handoff", "completed", "succeeded", "active", "", "waiting", "next_step_required"},
		{"driver between steps", "running", "succeeded", "active", "", "running", "preparing"},
		{"failed step despite completed reply", "completed", "failed", "active", "", "failed", "failed"},
		{"background execution", "completed", "running", "active", "", "running", "executing"},
		{"queued execution", "completed", "queued", "active", "", "running", "executing"},
		{"workflow completed", "completed", "", "completed", "", "completed", "completed"},
		{"workflow failed", "completed", "", "failed", "", "failed", "execution_failed"},
		{"workflow paused", "completed", "", "stopped", "", "paused", "user_stopped"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.WorkflowSessionStep{}, &orm.ExternalChatRun{})
			now := time.Now().UTC()
			session := orm.WorkflowSession{ID: "session", ConversationID: "conversation", CreateUserID: "owner",
				Status: sample.sessionStatus, ControllerHost: "external-agent", OriginRef: "run_origin"}
			run := orm.ExternalChatRun{ID: session.OriginRef, RequestID: "request", ConversationID: session.ConversationID,
				ActorUserID: session.CreateUserID, Status: sample.runStatus, CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			if sample.resumedStatus != "" {
				resumed := run
				resumed.ID, resumed.RequestID, resumed.Status = "run_resume", "resume-request", sample.resumedStatus
				resumed.CreatedAt = now.Add(time.Second)
				if err := db.Create(&resumed).Error; err != nil {
					t.Fatal(err)
				}
			}
			if sample.stepStatus != "" {
				if err := db.Create(&orm.WorkflowSessionStep{ID: "attempt", SessionID: session.ID, StepID: "step",
					TaskID: "task", Status: sample.stepStatus, Validity: "effective"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			view, err := Read(db.DB, session)
			if err != nil || view.Status != sample.want || view.Reason != sample.reason {
				t.Fatalf("view=%+v err=%v want=%s/%s", view, err, sample.want, sample.reason)
			}
		})
	}
}

func TestExternalDriverFailurePreservesPendingApproval(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowSessionStep{}, &orm.WorkflowReviewCheckpoint{}, &orm.ExternalChatRun{})
	session := orm.WorkflowSession{ID: "session", ConversationID: "conversation", CreateUserID: "owner",
		Status: "active", ControllerHost: "external-agent", ControlProtocol: "workflow.control.v1", OriginRef: "run_origin"}
	if err := db.Create(&orm.ExternalChatRun{ID: session.OriginRef, RequestID: "request", ConversationID: session.ConversationID,
		ActorUserID: session.CreateUserID, Status: "failed"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowSessionStep{ID: "attempt", SessionID: session.ID, StepID: "review",
		TaskID: "task", Status: "succeeded", Validity: "effective"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowReviewCheckpoint{ID: "checkpoint", SessionID: session.ID, AttemptID: "attempt", StepID: "review", Status: "pending"}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := Read(db.DB, session)
	if err != nil || !view.ApprovalRequired() || view.ApprovalStepID != "review" {
		t.Fatalf("pending review must remain actionable: view=%+v err=%v", view, err)
	}
}
