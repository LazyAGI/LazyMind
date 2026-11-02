package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
)

func failedControlledRetryFixture(t *testing.T, tools ...string) (*orm.DB, WorkflowControlService, WorkflowControlResult) {
	t.Helper()
	db, _ := setupBatchTransitionSession(t)
	first := beginControlledExternalStep(t, db, tools, "id: batch-plugin\n")
	failControlledRetryAttempt(t, db, first.Receipt.ExecutionID)
	return db, WorkflowControlService{DB: db.DB}, first
}

func TestControlledFailedRunNativeRetryPreservesExecutorOwnership(t *testing.T) {
	db, svc, first := failedControlledRetryFixture(t, "image_generator")
	var old orm.WorkflowSessionStep
	if err := db.First(&old, "id = ?", first.Receipt.ExecutionID).Error; err != nil {
		t.Fatal(err)
	}
	if old.ExecutorHost != "lazymind" {
		t.Fatalf("fixture must execute internally: %+v", old)
	}
	state := retryControlState(t, db)
	result, err := svc.Execute(context.Background(), "batch-user", "batch-session", WorkflowControlCommand{CommandID: "native-auto-retry", Kind: "begin", StepID: "branch_b", StateVersion: state.StateVersion})
	if err != nil {
		t.Fatal(err)
	}
	var replacement orm.WorkflowSessionStep
	if err := db.First(&replacement, "id = ?", result.Receipt.ExecutionID).Error; err != nil {
		t.Fatal(err)
	}
	if replacement.ExecutorHost != "lazymind" || replacement.Attempt != 2 || result.Control.Continuation != "awaiting_executor" || result.Control.Admission.CanBegin {
		t.Fatalf("native retry lost ownership or allowed overlapping work: %+v %+v", replacement, result.Control)
	}
}

func TestControlledFailedRunBudgetIsCheckedForRequestedStep(t *testing.T) {
	db, svc, _ := failedControlledRetryFixture(t)
	if err := db.Create(&orm.WorkflowSessionStep{ID: "exhausted", SessionID: "batch-session", StepID: "blocked_d", TaskID: "exhausted", Attempt: 3, Status: "failed", Validity: "effective"}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.Create(&orm.WorkflowTransitionCommand{CommandID: fmt.Sprintf("other-auto-%d", i), SessionID: "batch-session", TargetStepID: "blocked_d", Operation: "retry", RetryOrigin: "automatic", Status: "accepted", ResponseJSON: []byte(`{}`)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	state := retryControlState(t, db)
	if !state.Admission.CanBegin || len(state.AutomaticRetrySteps) != 1 || state.AutomaticRetrySteps[0] != "branch_b" {
		t.Fatalf("wrong eligible steps: %+v", state)
	}
	_, err := svc.Execute(context.Background(), "batch-user", "batch-session", WorkflowControlCommand{CommandID: "wrong-budget", Kind: "begin", StepID: "blocked_d", StateVersion: state.StateVersion})
	var rejection *transitionRejection
	if !errors.As(err, &rejection) || rejection.response.Error.Code != "WORKFLOW_AUTOMATIC_RETRY_LIMIT_EXCEEDED" {
		t.Fatalf("other step's remaining budget allowed exhausted target: %v", err)
	}
}

func failControlledRetryAttempt(t *testing.T, db *orm.DB, executionID string) {
	t.Helper()
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", executionID).Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	// Use the same finalizer as a real step failure: the run becomes failed.
	if err := FinalizeHostAttempt(context.Background(), db.DB, "batch-session", "branch_b", executionID, "failed"); err != nil {
		t.Fatal(err)
	}
}

func retryControlState(t *testing.T, db *orm.DB) *controlstore.Snapshot {
	t.Helper()
	var session orm.WorkflowSession
	if err := db.First(&session, "id = ?", "batch-session").Error; err != nil {
		t.Fatal(err)
	}
	state, err := controlstore.Read(db.DB, session)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestControlledFailedRunRetriesWithinBudgetAndManualRetryAfterLimit(t *testing.T) {
	db, svc, first := failedControlledRetryFixture(t)
	prior := first.Receipt.ExecutionID
	for attempt := 2; attempt <= 3; attempt++ {
		state := retryControlState(t, db)
		if state.Continuation != "continue" || !state.Admission.CanBegin || len(state.AutomaticRetrySteps) != 1 || state.AutomaticRetrySteps[0] != "branch_b" {
			t.Fatalf("attempt %d cannot retry: %+v", attempt, state)
		}
		cmd := WorkflowControlCommand{CommandID: fmt.Sprintf("auto-retry-%d", attempt), Kind: "begin", StepID: "branch_b", StateVersion: state.StateVersion}
		result, err := svc.Execute(context.Background(), "batch-user", "batch-session", cmd)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		replay, err := svc.Execute(context.Background(), "batch-user", "batch-session", cmd)
		if err != nil || replay.Receipt != result.Receipt {
			t.Fatalf("retry replay created a different receipt: %+v %v", replay, err)
		}
		var old, replacement orm.WorkflowSessionStep
		db.First(&old, "id = ?", prior)
		db.First(&replacement, "id = ?", result.Receipt.ExecutionID)
		if old.Validity != "stale" || replacement.Attempt != attempt || replacement.Validity != "effective" || replacement.Status != "queued" {
			t.Fatalf("invalid replacement: old=%+v new=%+v", old, replacement)
		}
		prior = replacement.ID
		failControlledRetryAttempt(t, db, prior)
	}
	state := retryControlState(t, db)
	if state.Continuation != "failed" || state.Admission.CanBegin || len(state.AutomaticRetrySteps) != 0 {
		t.Fatalf("exhausted budget still admitted: %+v", state)
	}
	_, err := svc.Execute(context.Background(), "batch-user", "batch-session", WorkflowControlCommand{CommandID: "over-budget", Kind: "begin", StepID: "branch_b", StateVersion: state.StateVersion})
	var rejected *transitionRejection
	if !errors.As(err, &rejected) || rejected.response.Error.Code != "WORKFLOW_AUTOMATIC_RETRY_LIMIT_EXCEEDED" {
		t.Fatalf("expected budget error, got %v", err)
	}
	var count int64
	db.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND step_id = ?", "batch-session", "branch_b").Count(&count)
	if count != 3 {
		t.Fatalf("replay or rejected begin consumed an attempt: %d", count)
	}
	result, err := svc.Execute(context.Background(), "batch-user", "batch-session", WorkflowControlCommand{CommandID: "explicit-user-retry", Kind: "retry", StepID: "branch_b", StateVersion: state.StateVersion})
	if err != nil || result.Receipt.ExecutionID == "" {
		t.Fatalf("manual retry was blocked by automatic budget: %+v %v", result, err)
	}
}

func TestControlledFailedRunRetryKeepsAdmissionBarriers(t *testing.T) {
	for _, barrier := range []string{"review", "binding", "edit", "stopped", "active", "other-step", "rewind"} {
		t.Run(barrier, func(t *testing.T) {
			db, svc, _ := failedControlledRetryFixture(t)
			target := "branch_b"
			switch barrier {
			case "review":
				if err := db.Create(&orm.WorkflowReviewCheckpoint{ID: "pending-review", SessionID: "batch-session", AttemptID: "prior-native", Status: "pending", Version: 1}).Error; err != nil {
					t.Fatal(err)
				}
			case "binding":
				if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("control_binding_json", `{"required":true}`).Error; err != nil {
					t.Fatal(err)
				}
			case "edit":
				if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("control_binding_json", `{"edit_paused":true}`).Error; err != nil {
					t.Fatal(err)
				}
			case "stopped":
				if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("status", "stopped").Error; err != nil {
					t.Fatal(err)
				}
			case "active":
				if err := db.Create(&orm.WorkflowSessionStep{ID: "parallel", SessionID: "batch-session", StepID: "parallel", TaskID: "parallel", Attempt: 1, Status: "running", Validity: "effective"}).Error; err != nil {
					t.Fatal(err)
				}
			case "other-step":
				target = "blocked_d"
			case "rewind":
				target = "branch_c"
			}
			state := retryControlState(t, db)
			_, err := svc.Execute(context.Background(), "batch-user", "batch-session", WorkflowControlCommand{CommandID: "blocked-begin", Kind: "begin", StepID: target, StateVersion: state.StateVersion})
			if err == nil {
				t.Fatalf("%s allowed automatic execution", barrier)
			}
			var code string
			var controlErr *controlstore.Error
			var rejection *transitionRejection
			if errors.As(err, &controlErr) {
				code = controlErr.Code
			}
			if errors.As(err, &rejection) {
				code = rejection.response.Error.Code
			}
			want := "WORKFLOW_ADMISSION_DENIED"
			if barrier == "edit" {
				want = "EDITS_PENDING_CONTINUE"
			}
			if barrier == "stopped" {
				want = "SESSION_STOPPED"
			}
			if code != want {
				t.Fatalf("%s rejected for wrong reason: %v", barrier, err)
			}
		})
	}
}
