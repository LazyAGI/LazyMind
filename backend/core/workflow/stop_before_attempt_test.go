package workflow

import (
	"testing"

	"lazymind/core/taskcenter"
)

func TestStopBeforeFirstAttemptIsNotAnApproval(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()
	session, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "stop-before-attempt", ConversationID: "stop-before-attempt-conv", WorkflowID: "writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	StopActiveWorkflowSession(ctx, db.DB, nil, session.ConversationID)
	if !taskcenter.WorkflowWasStopped(ctx, db.DB, session.ID) {
		t.Fatal("a workflow stopped before its first attempt still looks like an approval")
	}
	stopped, err := GetSession(ctx, db.DB, session.ID)
	if err != nil || stopped.LastStoppedAt == nil {
		t.Fatalf("missing durable stop time: %v", err)
	}
	// Repeating stop cannot erase the durable reason when there is no attempt.
	StopActiveWorkflowSession(ctx, db.DB, nil, session.ConversationID)
	if !taskcenter.WorkflowWasStopped(ctx, db.DB, session.ID) {
		t.Fatal("repeated stop lost its reason")
	}
	repeated, err := GetSession(ctx, db.DB, session.ID)
	if err != nil || repeated.LastStoppedAt == nil || !repeated.LastStoppedAt.Equal(*stopped.LastStoppedAt) {
		t.Fatalf("repeated stop changed its original time: %v", err)
	}
	if err := UpdateSessionStatus(ctx, db.DB, session.ID, SessionStatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSessionStep(ctx, db.DB, session.ID, "write", "resumed-first-attempt", 1); err != nil {
		t.Fatal(err)
	}
	if taskcenter.WorkflowWasStopped(ctx, db.DB, session.ID) {
		t.Fatal("a resumed attempt must supersede the old stop")
	}
	if err := UpdateStepStatus(ctx, db.DB, "resumed-first-attempt", StepStatusSucceeded); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSessionStatus(ctx, db.DB, session.ID, SessionStatusWaiting); err != nil {
		t.Fatal(err)
	}
	if taskcenter.WorkflowWasStopped(ctx, db.DB, session.ID) {
		t.Fatal("a new approval after resume must not inherit cancellation")
	}
}

func TestStopDoesNotOverwriteSessionCompletion(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()
	session, err := CreateSession(ctx, db.DB, CreateSessionInput{
		SessionID: "completed-before-stop", ConversationID: "completed-before-stop-conv", WorkflowID: "writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateSessionStatus(ctx, db.DB, session.ID, SessionStatusCompleted); err != nil {
		t.Fatal(err)
	}
	stopWorkflowSession(ctx, db.DB, nil, session)
	completed, err := GetSession(ctx, db.DB, session.ID)
	if err != nil || completed.Status != SessionStatusCompleted || completed.LastStoppedAt != nil {
		t.Fatalf("stop overwrote completed session: %+v, %v", completed, err)
	}
}
