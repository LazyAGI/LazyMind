package localworkspace

import (
	"context"
	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/state"
)

func TestWorkspaceApprovalAllowsOnceAndRejectsSecondDecision(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	callID := operationTestCallID("call-1")
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "approved.txt", Content: "ok", CallID: callID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Decision != DecisionPending {
		t.Fatalf("decision=%s", prepared.Decision)
	}
	if _, err := DecideOperation(context.Background(), db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(context.Background(), db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err == nil {
		t.Fatal("second decision unexpectedly succeeded")
	}
	if _, err := ExecuteOperation(context.Background(), db.DB, stateStore, prepared.OperationID, OperationRequest{HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "approved.txt", Content: "ok", CallID: callID,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceApprovalRejectsMismatchedCall(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "approved.txt", Content: "ok", CallID: operationTestCallID("call-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(context.Background(), db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	_, err = ExecuteOperation(context.Background(), db.DB, stateStore, prepared.OperationID, OperationRequest{HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "approved.txt", Content: "tampered", CallID: operationTestCallID("call-2"),
	})
	if err == nil {
		t.Fatal("mismatched call unexpectedly executed")
	}
}

func TestWorkspaceApprovalConcurrentDecisionsConsumeOneWinner(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "concurrent.txt", Content: "ok", CallID: operationTestCallID("call-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, action := range []string{"allow_once", "reject"} {
		go func(action string) {
			_, callErr := DecideOperation(context.Background(), db.DB, stateStore, prepared.OperationID, action, "owner")
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

func TestWorkspaceClaimExpiryCannotOverwriteDecision(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	ctx := context.Background()
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, OperationRequest{HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: "notes.txt", Content: "seed", CallID: operationTestCallID("stalled-decision"),
	})
	if err != nil {
		t.Fatal(err)
	}
	delayed := &claimSnapshotStore{Store: stateStore, CompareAndDeleteStore: stateStore.(state.CompareAndDeleteStore),
		claimKey: operationDecisionKey(prepared.OperationID), stateKey: operationKey(prepared.OperationID)}
	succeeded := 0
	interleaved := false
	delayed.afterSnapshot = func() {
		interleaved = true
		_, err := DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "reject", "owner")
		if err == nil {
			succeeded++
		} else {
			requireWorkspaceReason(t, err, 409, "conflict", "binding_conflict")
		}
	}
	_, err = DecideOperation(ctx, db.DB, delayed, prepared.OperationID, "allow_once", "owner")
	if err == nil {
		succeeded++
	} else {
		requireWorkspaceReason(t, err, 409, "conflict", "binding_conflict")
	}
	if !interleaved || succeeded != 1 {
		t.Fatalf("interleaved=%v, successful decisions=%d, want one", interleaved, succeeded)
	}
}

func TestWorkspaceClaimInvalidDecisionDoesNotConsumeApproval(t *testing.T) {
	for _, field := range []string{"owner", "action"} {
		t.Run(field, func(t *testing.T) {
			db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
			ctx := context.Background()
			prepared, err := PrepareOperation(ctx, db.DB, stateStore, OperationRequest{HistoryID: "history", RunID: "run",
				UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
				Operation: OperationCreate, Path: "notes.txt", Content: "seed", CallID: operationTestCallID("valid-decision"),
			})
			if err != nil {
				t.Fatal(err)
			}
			if field == "owner" {
				_, err = DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "allow_once", "other-owner")
				requireWorkspaceReason(t, err, 404, "resource not found", "workspace_not_found")
			} else {
				_, err = DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "invalid", "owner")
				requireWorkspaceReason(t, err, 400, "invalid request", "invalid_selection")
			}
			result, err := DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "allow_once", "owner")
			if err != nil || result.Decision != DecisionAllowed {
				t.Fatalf("invalid request consumed valid decision: result=%+v err=%v", result, err)
			}
		})
	}
}

func TestWorkspaceApprovalListRetainsReceiptsAfterRevokeAndMissingIndex(t *testing.T) {
	db, grant, stateStore, conversation := operationFixture(t, PermissionAllowAll)
	store.Init(db.DB, nil, stateStore)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	ctx := context.Background()
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: operationTestCallID("receipt"), Operation: OperationCreate, Path: "created.txt", Content: "private body"}
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.HSet(ctx, "local-workspace-operation-index:"+conversation, map[string]any{"15": "expired-record"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.LocalWorkspace{}).Where("id = ?", grant.WorkspaceID).Update("status", StatusRevoked).Error; err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/conversations/"+conversation+":workspace-approvals", nil)
	request.Header.Set("X-User-Id", "owner")
	request = mux.SetURLVars(request, map[string]string{"conversation_id": conversation})
	response := httptest.NewRecorder()
	ListOperationApprovals(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "completed") || strings.Contains(response.Body.String(), "private body") || strings.Contains(response.Body.String(), "lease_token") {
		t.Fatalf("list %d %s", response.Code, response.Body.String())
	}
	request.Header.Set("X-User-Id", "other")
	response = httptest.NewRecorder()
	ListOperationApprovals(response, request)
	if response.Code != 404 {
		t.Fatalf("cross owner %d", response.Code)
	}
}
