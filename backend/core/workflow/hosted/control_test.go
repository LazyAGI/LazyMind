package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
)

func controlledService(t *testing.T) (*Service, *gorm.DB, Execution) {
	t.Helper()
	service, db := hostedTestService(t)
	if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowApprovalPreference{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Updates(map[string]any{
		"control_protocol": controlpolicy.Protocol, "control_binding_json": `{}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("review_required", true).Error; err != nil {
		t.Fatal(err)
	}
	execution, err := service.Begin(context.Background(), "owner", "session-1", "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	return service, db, execution
}

func successfulSubmission(handle string) Submission {
	return Submission{Outcome: "succeeded", ExecutionHandle: handle,
		Artifacts: []executor.Artifact{{Slot: "report", ContentType: "text/plain", Seq: 1, Value: json.RawMessage(`{"text":"review me"}`)}}}
}

func TestControlledHumanSubmitConfirmAndFreshReplay(t *testing.T) {
	service, db, execution := controlledService(t)
	ctx := context.Background()
	input := successfulSubmission(execution.ExecutionHandle)
	result, err := service.Submit(ctx, "owner", "session-1", "attempt-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Control.Continuation != "awaiting_user" || result.Control.Admission.CanBegin || len(result.Control.Reviews) != 1 {
		t.Fatalf("human result is not guarded: %+v", result.Control)
	}
	var session orm.WorkflowSession
	if err := db.First(&session, "id = ?", "session-1").Error; err != nil {
		t.Fatal(err)
	}
	if err := controlstore.GuardBegin(db, session); err == nil {
		t.Fatal("direct Core admission bypassed the review")
	}
	review := result.Control.Reviews[0]
	controller := workflowcore.WorkflowControlService{DB: db}
	command := workflowcore.WorkflowControlCommand{CommandID: "confirm-1", Kind: "confirm",
		ReviewID: review.ID, ReviewVersion: review.Version, ManifestHash: review.ManifestHash}
	confirmed, err := controller.Execute(ctx, "owner", session.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Control.Continuation != "completed" || confirmed.Control.Reviews[0].Status != "accepted" {
		t.Fatalf("last step did not complete after confirmation: %+v", confirmed.Control)
	}
	replay, err := service.Submit(ctx, "owner", session.ID, "attempt-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.AlreadyTerminal || replay.Control.Continuation != "completed" || replay.Receipt.CommandID != result.Receipt.CommandID {
		t.Fatalf("historical receipt restored stale control: %+v", replay)
	}
	input.Summary = "different submission"
	if _, err := service.Submit(ctx, "owner", session.ID, "attempt-1", input); err == nil {
		t.Fatal("conflicting terminal replay was accepted")
	}
	command.ManifestHash = "different content"
	if _, err := controller.Execute(ctx, "owner", session.ID, command); err == nil {
		t.Fatal("command id accepted different confirmation content")
	}
}

func TestControlledStaleHandleCannotPublishArtifacts(t *testing.T) {
	service, db, previous := controlledService(t)
	current, err := service.Resume(context.Background(), "owner", "session-1", "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	if previous.ExecutionHandle == current.ExecutionHandle {
		t.Fatal("resume did not rotate the execution handle")
	}
	_, err = service.Submit(context.Background(), "owner", "session-1", "attempt-1", successfulSubmission(previous.ExecutionHandle))
	var rejected *controlstore.Error
	if !errors.As(err, &rejected) || rejected.Code != "EXECUTION_FENCED" {
		t.Fatalf("stale handle: %v", err)
	}
	var count int64
	db.Model(&orm.WorkflowSlotRevision{}).Count(&count)
	if count != 0 {
		t.Fatal("a fenced worker polluted the selected artifacts")
	}
}

func TestControlledFinalizationRollsBackArtifactsAndTerminalOnReviewFailure(t *testing.T) {
	service, db, execution := controlledService(t)
	if err := db.Callback().Create().Before("gorm:create").Register("test:reject-review", func(tx *gorm.DB) {
		if tx.Statement.Table == "workflow_review_checkpoints" {
			tx.AddError(errors.New("review storage unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("test:reject-review")
	if _, err := service.Submit(context.Background(), "owner", "session-1", "attempt-1", successfulSubmission(execution.ExecutionHandle)); err == nil {
		t.Fatal("expected injected storage failure")
	}
	var attempt orm.WorkflowSessionStep
	db.First(&attempt, "id = ?", "attempt-1")
	if attempt.Status != "claimed" || attempt.SubmissionHash != "" {
		t.Fatalf("partial terminal commit: %+v", attempt)
	}
	for _, model := range []any{&orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowReviewCheckpoint{}, &orm.WorkflowCommand{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("partial write to %T: %d", model, count)
		}
	}
}

func TestConfirmationRejectsStaleContentAndRollsBackRouteFailure(t *testing.T) {
	service, db, execution := controlledService(t)
	result, err := service.Submit(context.Background(), "owner", "session-1", "attempt-1", successfulSubmission(execution.ExecutionHandle))
	if err != nil {
		t.Fatal(err)
	}
	review := result.Control.Reviews[0]
	controller := workflowcore.WorkflowControlService{DB: db}
	command := workflowcore.WorkflowControlCommand{CommandID: "confirm-1", Kind: "confirm", ReviewID: review.ID,
		ReviewVersion: review.Version, ManifestHash: "old hash"}
	if _, err := controller.Execute(context.Background(), "owner", "session-1", command); err == nil {
		t.Fatal("stale content was confirmed")
	}
	command.ManifestHash = review.ManifestHash
	if err := db.Callback().Create().Before("gorm:create").Register("test:reject-route", func(tx *gorm.DB) {
		if tx.Statement.Table == "plugin_route_decisions" {
			tx.AddError(errors.New("route storage unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("test:reject-route")
	if _, err := controller.Execute(context.Background(), "owner", "session-1", command); err == nil {
		t.Fatal("expected route transaction failure")
	}
	if err := db.First(&review, "id = ?", review.ID).Error; err != nil {
		t.Fatal(err)
	}
	if review.Status != "pending" || review.AcceptedAt != nil {
		t.Fatalf("partial confirmation: %+v", review)
	}
}

func TestAutoFinalSubmissionReturnsCommittedCompletion(t *testing.T) {
	service, db, execution := controlledService(t)
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("review_required", false).Error; err != nil {
		t.Fatal(err)
	}
	result, err := service.Submit(context.Background(), "owner", "session-1", "attempt-1", successfulSubmission(execution.ExecutionHandle))
	if err != nil || result.Control.Continuation != "completed" {
		t.Fatalf("submit returned pre-commit lifecycle: %+v %v", result, err)
	}
}

func TestConfirmationCannotAcceptADeletedRequiredOutput(t *testing.T) {
	service, db, execution := controlledService(t)
	ctx := context.Background()
	result, err := service.Submit(ctx, "owner", "session-1", "attempt-1", successfulSubmission(execution.ExecutionHandle))
	if err != nil {
		t.Fatal(err)
	}
	if err := controlstore.Transaction(ctx, db, "session-1", func(tx *gorm.DB, session *orm.WorkflowSession) error {
		if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ?", "session-1").Update("selected", false).Error; err != nil {
			return err
		}
		return controlstore.RefreshReviews(tx, session)
	}); err != nil {
		t.Fatal(err)
	}
	review := result.Control.Reviews[0]
	if err := db.First(&review, "id = ?", review.ID).Error; err != nil {
		t.Fatal(err)
	}
	_, err = (workflowcore.WorkflowControlService{DB: db}).Execute(ctx, "owner", "session-1", workflowcore.WorkflowControlCommand{CommandID: "confirm-empty", Kind: "confirm", ReviewID: review.ID, ReviewVersion: review.Version, ManifestHash: review.ManifestHash})
	var problem *controlstore.Error
	if !errors.As(err, &problem) || problem.Code != "REVIEW_OUTPUT_MISSING" {
		t.Fatalf("accepted missing required output: %v", err)
	}
	db.First(&review, "id = ?", review.ID)
	if review.Status != "pending" {
		t.Fatal("invalid confirmation changed the checkpoint")
	}
}
