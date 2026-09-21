package localworkspace

import (
	"context"
	"errors"
	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/state"
)

func TestWorkspacePendingApprovalSurvivesPermissionChangeWithinRun(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	req := OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID,
		WorkspaceID: grant.WorkspaceID, Operation: OperationWrite, Path: filepath.Join(grant.Path, "approved.txt"), CallID: operationTestCallID("permission-change")}
	prepared, err := PrepareOperation(t.Context(), db.DB, stateStore, req)
	if err != nil || prepared.Decision != DecisionPending {
		t.Fatalf("prepare=%+v err=%v", prepared, err)
	}
	if err := db.Model(&orm.ConversationWorkspaceBinding{}).Where("conversation_id = ?", conversationID).
		Updates(map[string]any{"permission_mode": PermissionAllowAll, "permission_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(t.Context(), db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatalf("permission update invalidated pending operation: %v", err)
	}
	if _, err := ClaimLocalOperation(t.Context(), db.DB, stateStore, prepared.OperationID, req); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceApprovalAllowsOnceAndRejectsSecondDecision(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	callID := operationTestCallID("call-1")
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationWrite, Path: filepath.Join(grant.Path, "approved.txt"), CallID: callID,
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
	if result, err := DecideOperation(context.Background(), db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil || result.Decision != DecisionAllowed {
		t.Fatalf("same decision was not idempotent: %+v, %v", result, err)
	}
	if _, err := DecideOperation(context.Background(), db.DB, stateStore, prepared.OperationID, "reject", "owner"); err == nil {
		t.Fatal("conflicting second decision unexpectedly succeeded")
	}
	if _, err := ClaimLocalOperation(context.Background(), db.DB, stateStore, prepared.OperationID, OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationWrite, Path: filepath.Join(grant.Path, "approved.txt"), CallID: callID,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceDecisionClaimCanRecoverAfterStateWriteFailure(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	prepared, err := PrepareOperation(t.Context(), db.DB, stateStore, OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationWrite, Path: filepath.Join(grant.Path, "decision-recovery.txt"), CallID: operationTestCallID("decision-recovery")})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("state write unavailable")
	failing := &failedOperationWriteStore{Store: stateStore, CompareAndDeleteStore: stateStore.(state.CompareAndDeleteStore), key: operationKey(prepared.OperationID), err: failure}
	if _, err := DecideOperation(t.Context(), db.DB, failing, prepared.OperationID, "allow_once", "owner"); !errors.Is(err, failure) {
		t.Fatalf("expected state write failure, got %v", err)
	}
	if result, err := DecideOperation(t.Context(), db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil || result.Decision != DecisionAllowed {
		t.Fatalf("decision did not recover: %+v, %v", result, err)
	}
}

func TestWorkspaceApprovalRejectsMismatchedCall(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationWrite, Path: filepath.Join(grant.Path, "approved.txt"), CallID: operationTestCallID("call-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(context.Background(), db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	_, err = ClaimLocalOperation(context.Background(), db.DB, stateStore, prepared.OperationID, OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationWrite, Path: filepath.Join(grant.Path, "approved.txt"), CallID: operationTestCallID("call-2"),
	})
	if err == nil {
		t.Fatal("mismatched call unexpectedly executed")
	}
}

func TestWorkspaceApprovalConcurrentDecisionsConsumeOneWinner(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationWrite, Path: filepath.Join(grant.Path, "concurrent.txt"), CallID: operationTestCallID("call-1"),
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
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationWrite, Path: filepath.Join(grant.Path, "notes.txt"), CallID: operationTestCallID("stalled-decision"),
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
			prepared, err := PrepareOperation(ctx, db.DB, stateStore, OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run",
				UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
				Operation: OperationWrite, Path: filepath.Join(grant.Path, "notes.txt"), CallID: operationTestCallID("valid-decision"),
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
	req := OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: operationTestCallID("receipt"), Operation: OperationWrite, Path: filepath.Join(grant.Path, "created.txt")}
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimLocalOperation(ctx, db.DB, stateStore, prepared.OperationID, req); err != nil {
		t.Fatal(err)
	}
	if _, err := CompleteLocalOperation(ctx, stateStore, prepared.OperationID, LocalOperationCompletion{OperationRequest: req, Status: operationCompleted}); err != nil {
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

func TestExpiredApprovalHTTP(t *testing.T) {
	db, grant, ss, conversation := operationFixture(t, PermissionAlwaysAsk)
	store.Init(db.DB, nil, ss)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	req := OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: operationTestCallID("expired"), Operation: OperationWrite, Path: filepath.Join(grant.Path, "expired.txt")}
	prepared, err := PrepareOperation(t.Context(), db.DB, ss, req)
	if err != nil {
		t.Fatal(err)
	}
	value, err := loadOperationState(t.Context(), ss, prepared.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	value.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	if err := saveOperationState(t.Context(), ss, value); err != nil {
		t.Fatal(err)
	}
	SetValidateOperationRunFunc(nil) // Expiration takes precedence over an inactive execution.
	for _, action := range []string{"allow_once", "allow_future", "reject"} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"`+action+`"}`))
		r.Header.Set("X-User-Id", "owner")
		r = mux.SetURLVars(r, map[string]string{"conversation_id": conversation, "operation_id": prepared.OperationID})
		w := httptest.NewRecorder()
		DecideOperationHandler(w, r)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "selection_expired") {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
}

func TestApprovalExpiresDuringDecisionClaim(t *testing.T) {
	db, grant, ss, conversation := operationFixture(t, PermissionAlwaysAsk)
	req := OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: operationTestCallID("expires-during-claim"), Operation: OperationWrite, Path: filepath.Join(grant.Path, "expired.txt")}
	prepared, err := PrepareOperation(t.Context(), db.DB, ss, req)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &expireOnDecisionClaimStore{Store: ss, operationID: prepared.OperationID}
	_, err = DecideOperation(t.Context(), db.DB, wrapped, prepared.OperationID, "allow_once", "owner")
	w := httptest.NewRecorder()
	replyError(w, err)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "selection_expired") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	value, err := loadOperationState(t.Context(), ss, prepared.OperationID)
	if err != nil || value.DecisionAction != "" {
		t.Fatalf("decision persisted: %+v %v", value, err)
	}
}

type expireOnDecisionClaimStore struct {
	state.Store
	operationID string
}

func (s *expireOnDecisionClaimStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	claimed, err := s.Store.SetNX(ctx, key, value, ttl)
	if err != nil || !claimed {
		return claimed, err
	}
	op, err := loadOperationState(ctx, s.Store, s.operationID)
	if err != nil {
		return false, err
	}
	op.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	return claimed, saveOperationState(ctx, s.Store, op)
}
