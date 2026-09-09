package localworkspace

import (
	"context"
	"testing"
)

func TestWorkspaceApprovalAllowsOnceAndRejectsSecondDecision(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "approved.txt", Content: "ok", CallID: "call-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Decision != DecisionPending {
		t.Fatalf("decision=%s", prepared.Decision)
	}
	if _, err := DecideOperation(context.Background(), stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(context.Background(), stateStore, prepared.OperationID, "allow_once", "owner"); err == nil {
		t.Fatal("second decision unexpectedly succeeded")
	}
	if _, err := ExecuteOperation(context.Background(), db.DB, stateStore, prepared.OperationID, OperationRequest{
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "approved.txt", Content: "ok", CallID: "call-1",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceApprovalRejectsMismatchedCall(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "approved.txt", Content: "ok", CallID: "call-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(context.Background(), stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	_, err = ExecuteOperation(context.Background(), db.DB, stateStore, prepared.OperationID, OperationRequest{
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "approved.txt", Content: "tampered", CallID: "call-2",
	})
	if err == nil {
		t.Fatal("mismatched call unexpectedly executed")
	}
}

func TestWorkspaceApprovalConcurrentDecisionsConsumeOneWinner(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "concurrent.txt", Content: "ok", CallID: "call-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, action := range []string{"allow_once", "reject"} {
		go func(action string) {
			_, callErr := DecideOperation(context.Background(), stateStore, prepared.OperationID, action, "owner")
			results <- callErr
		}(action)
	}
	var success, conflict int
	for range 2 {
		if err := <-results; err == nil {
			success++
		} else {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}
