package localworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/state"
)

func operationFixture(t *testing.T, mode string) (*orm.DB, PublicWorkspace, state.Store, string) {
	t.Helper()
	operationRunValidator.RLock()
	previous := operationRunValidator.fn
	operationRunValidator.RUnlock()
	var runSnapshot *ContextSnapshot
	SetValidateOperationRunFunc(func(ctx context.Context, db *gorm.DB, _ state.Store, request OperationRequest) (*ContextSnapshot, error) {
		if request.HistoryID != "history" || request.RunID != "run" {
			return nil, Error("execution_inactive", 409, "conflict")
		}
		if runSnapshot == nil {
			var err error
			runSnapshot, err = ResolveForConversation(ctx, db, request.UserID, request.ConversationID)
			if err != nil {
				return nil, err
			}
		}
		copy := *runSnapshot
		return &copy, nil
	})
	t.Cleanup(func() { SetValidateOperationRunFunc(previous) })
	db, grant := workspaceFixture(t)
	now := time.Now().UTC()
	conversationID := fmt.Sprintf("operation-%d", time.Now().UnixNano())
	conversation := orm.Conversation{ID: conversationID, IsTaskConv: true,
		BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationWorkspaceBinding{ConversationID: conversation.ID, WorkspaceID: grant.WorkspaceID, PermissionMode: mode, PermissionVersion: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	var stateStore state.Store
	var err error
	if redisURL := os.Getenv("TEST_WORKSPACE_REDIS_URL"); redisURL != "" {
		stateStore, err = state.NewRedisStoreFromURL(redisURL)
	} else {
		stateStore, err = state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	return db, grant, stateStore, conversationID
}

func TestWorkspaceOperationRoundTripAndVersionConflict(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	ctx := context.Background()
	base := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID}

	createReq := base
	createReq.Operation, createReq.Path, createReq.Content, createReq.CallID = OperationCreate, "notes.txt", "one", operationTestCallID("create-1")
	createdPrepared, err := PrepareOperation(ctx, db.DB, stateStore, createReq)
	if err != nil {
		t.Fatal(err)
	}
	created, err := ExecuteOperation(ctx, db.DB, stateStore, createdPrepared.OperationID, createReq)
	if err != nil {
		t.Fatal(err)
	}
	if created.Version == "" || created.Content != "one" {
		t.Fatalf("created=%+v", created)
	}

	appendReq := base
	appendReq.Operation, appendReq.Path, appendReq.Content, appendReq.ExpectedVersion, appendReq.CallID = OperationAppend, "notes.txt", "\ntwo", created.Version, operationTestCallID("append-1")
	appendPrepared, err := PrepareOperation(ctx, db.DB, stateStore, appendReq)
	if err != nil {
		t.Fatal(err)
	}
	appended, err := ExecuteOperation(ctx, db.DB, stateStore, appendPrepared.OperationID, appendReq)
	if err != nil {
		t.Fatal(err)
	}
	if appended.Content != "one\ntwo" {
		t.Fatalf("appended=%+v", appended)
	}

	replaceReq := base
	replaceReq.Operation, replaceReq.Path, replaceReq.OldContent, replaceReq.Content, replaceReq.ExpectedVersion, replaceReq.CallID = OperationReplace, "notes.txt", "two", "THREE", appended.Version, operationTestCallID("replace-1")
	replacePrepared, err := PrepareOperation(ctx, db.DB, stateStore, replaceReq)
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := ExecuteOperation(ctx, db.DB, stateStore, replacePrepared.OperationID, replaceReq)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Content != "one\nTHREE" {
		t.Fatalf("replaced=%+v", replaced)
	}

	readReq := base
	readReq.Operation, readReq.Path, readReq.CallID = OperationRead, "notes.txt", operationTestCallID("read-1")
	readPrepared, err := PrepareOperation(ctx, db.DB, stateStore, readReq)
	if err != nil {
		t.Fatal(err)
	}
	read, err := ExecuteOperation(ctx, db.DB, stateStore, readPrepared.OperationID, readReq)
	if err != nil {
		t.Fatal(err)
	}
	if read.Content != "one\nTHREE" {
		t.Fatalf("read=%+v", read)
	}

	deleteReq := base
	deleteReq.Operation, deleteReq.Path, deleteReq.ExpectedVersion, deleteReq.CallID = OperationDelete, "notes.txt", replaced.Version, operationTestCallID("delete-1")
	deletePrepared, err := PrepareOperation(ctx, db.DB, stateStore, deleteReq)
	if err != nil {
		t.Fatal(err)
	}
	if deletePrepared.Decision != DecisionAllowed {
		t.Fatalf("delete decision=%s", deletePrepared.Decision)
	}
	if _, err := ExecuteOperation(ctx, db.DB, stateStore, deletePrepared.OperationID, deleteReq); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(grant.Path, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file stat=%v", err)
	}
}

func TestWorkspaceOperationPermissionDecisions(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		operation OperationKind
		want      Decision
	}{
		{PermissionAlwaysAsk, OperationCreate, DecisionPending},
		{PermissionAskAsNeeded, OperationCreate, DecisionAllowed},
		{PermissionAskAsNeeded, OperationDelete, DecisionPending},
		{PermissionAllowAll, OperationCreate, DecisionAllowed},
		{PermissionAllowAll, OperationRead, DecisionAllowed},
	} {
		if got := permissionDecision(tc.mode, tc.operation, "/workspace/notes.txt"); got != tc.want {
			t.Errorf("mode=%s operation=%s got=%s want=%s", tc.mode, tc.operation, got, tc.want)
		}
	}
}

func TestWorkspaceOperationRejectsOutsideGitAndSymlinkPaths(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(grant.Path, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, path := range []string{"../outside.txt", ".git/config", "link.txt"} {
		_, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: OperationRead, Path: path, CallID: operationTestCallID("bad-") + path})
		if err == nil {
			t.Errorf("path %q unexpectedly allowed", path)
		}
	}
}

func TestWorkspaceOperationRejectsSensitiveMutationEvenWhenAllAllowed(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	_, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{HistoryID: "history", RunID: "run",
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: ".env", Content: "SECRET=x", CallID: operationTestCallID("sensitive-create"),
	})
	if err == nil {
		t.Fatal("sensitive mutation unexpectedly allowed")
	}
}

func TestWorkspaceOperationKeepsRunPermissionSnapshotUntilNextExecution(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: OperationCreate, Path: "permission.txt", Content: "ok", CallID: operationTestCallID("permission-call")}
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.ConversationWorkspaceBinding{}).Where("conversation_id = ?", conversationID).Updates(map[string]any{"permission_mode": PermissionAlwaysAsk, "permission_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteOperation(context.Background(), db.DB, stateStore, prepared.OperationID, req); err != nil {
		t.Fatalf("same run lost its permission snapshot: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(grant.Path, "permission.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("file=%q err=%v", data, err)
	}
	second := req
	second.Path, second.Content, second.CallID = "same-run.txt", "still allowed", operationTestCallID("same-run")
	prepared, err = PrepareOperation(context.Background(), db.DB, stateStore, second)
	if err != nil || prepared.Decision != DecisionAllowed {
		t.Fatalf("same run used updated permission: result=%+v err=%v", prepared, err)
	}
}

func TestWorkspaceOperationRejectsSymlinkedParentOutsideRoot(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(grant.Path, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: OperationCreate, Path: "linked/escape.txt", Content: "x", CallID: operationTestCallID("linked-create")})
	if err == nil {
		t.Fatal("symlinked parent unexpectedly allowed")
	}
}

func TestWorkspaceOperationStateStoresDigestsInsteadOfContent(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: OperationCreate, Path: "digest.txt", Content: "private-content", CallID: operationTestCallID("digest-call")}
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := stateStore.Get(context.Background(), operationKey(prepared.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), req.Content) {
		t.Fatal("operation state contains content")
	}
	if !strings.Contains(string(raw), digestString(req.Content)) {
		t.Fatal("operation state missing content digest")
	}
}

func TestWorkspaceOperationCompletedStateStoresNoContent(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: OperationCreate, Path: "completed-state.txt", Content: "private-content", CallID: operationTestCallID("completed-call")}
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteOperation(context.Background(), db.DB, stateStore, prepared.OperationID, req); err != nil {
		t.Fatal(err)
	}
	raw, err := stateStore.Get(context.Background(), operationKey(prepared.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), req.Content) {
		t.Fatal("completed operation state contains content")
	}
}

func TestWorkspaceOperationSensitiveReadApproval(t *testing.T) {
	for _, mode := range []string{PermissionAlwaysAsk, PermissionAskAsNeeded, PermissionAllowAll} {
		for _, path := range []string{"notes.txt", ".env", ".env.example"} {
			t.Run(mode+"/"+path, func(t *testing.T) {
				db, grant, stateStore, conversationID := operationFixture(t, mode)
				ctx := context.Background()
				const content = "synthetic-test-content"
				if err := os.WriteFile(filepath.Join(grant.Path, path), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
					Operation: OperationRead, Path: path, CallID: operationTestCallID("read-approval")}
				prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
				if err != nil {
					t.Fatal(err)
				}
				want := DecisionAllowed
				if path == ".env" && mode != PermissionAllowAll {
					want = DecisionPending
				}
				if prepared.Decision != want {
					t.Fatalf("decision=%s, want %s", prepared.Decision, want)
				}
				if prepared.Content != "" {
					t.Fatal("prepare exposed file content")
				}
				if want == DecisionPending {
					result, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
					requireWorkspaceReason(t, err, 403, "forbidden", "selection_forbidden")
					if result.Content != "" {
						t.Fatalf("unapproved read: result=%+v err=%v", result, err)
					}
					if _, err := DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
						t.Fatal(err)
					}
				}
				result, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
				if err != nil || result.Content != content {
					t.Fatalf("approved read: result=%+v err=%v", result, err)
				}
			})
		}
	}
}

// Pause at the state-store boundary before ExecuteOperation claims its lock.
// All SQL, authorization and file operations still use the real implementation.
type delayedOperationClaimStore struct {
	state.Store
	state.CompareAndDeleteStore
	key         string
	beforeClaim func()
}

func (s *delayedOperationClaimStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if key == s.key && s.beforeClaim != nil {
		before := s.beforeClaim
		s.beforeClaim = nil
		before()
	}
	return s.Store.SetNX(ctx, key, value, ttl)
}

func TestWorkspaceOperationDelayedExecutionCannotReplayCompletedAppend(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	ctx := context.Background()
	path := filepath.Join(grant.Path, "notes.txt")
	if err := os.WriteFile(path, []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationAppend, Path: "notes.txt", Content: "+append", ExpectedVersion: digestString("seed"), CallID: operationTestCallID("same-call")}
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	var first OperationResult
	delayed := &delayedOperationClaimStore{Store: stateStore,
		CompareAndDeleteStore: stateStore.(state.CompareAndDeleteStore), key: operationLockKey(prepared.OperationID)}
	delayed.beforeClaim = func() {
		// A second request finishes while the first is delayed before claiming.
		first, err = ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
		if err != nil || first.Content != "seed+append" {
			t.Fatalf("first execution: result=%+v err=%v", first, err)
		}
		// An external editor undoes that append; a duplicate must not redo it.
		if err := os.WriteFile(path, []byte("seed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	replayed, err := ExecuteOperation(ctx, db.DB, delayed, prepared.OperationID, req)
	if err != nil || replayed.Status != operationCompleted || replayed.Version != first.Version {
		t.Fatalf("duplicate must return completed receipt: result=%+v err=%v", replayed, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "seed" {
		t.Fatalf("completed operation changed the file again: %q", content)
	}
}

func TestWorkspaceOperationFailedAppendCannotReuseApproval(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	ctx := context.Background()
	path := filepath.Join(grant.Path, "notes.txt")
	if err := os.WriteFile(path, []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationAppend, Path: "notes.txt", Content: "+append", ExpectedVersion: digestString("seed"), CallID: operationTestCallID("failed-call")}
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external-edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
	requireWorkspaceReason(t, err, 409, "conflict", "binding_conflict")
	failedState, err := loadOperationState(ctx, stateStore, prepared.OperationID)
	failed := operationResult(failedState)
	if err != nil || failed.Status != operationFailed {
		t.Fatalf("expected failed operation: result=%+v err=%v", failed, err)
	}
	if err := os.WriteFile(path, []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, retryErr := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "seed" {
		t.Errorf("failed operation changed the file on retry: %q", content)
	}
	requireWorkspaceReason(t, retryErr, 409, "conflict", "binding_conflict")
}

func TestWorkspaceOperationCompletedReadReturnsReceiptWithoutReadingAgain(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	ctx := context.Background()
	path := filepath.Join(grant.Path, "notes.txt")
	if err := os.WriteFile(path, []byte("first-version"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationRead, Path: "notes.txt", CallID: operationTestCallID("read-once")}
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
	if err != nil || first.Content != "first-version" {
		t.Fatalf("first read: result=%+v err=%v", first, err)
	}
	if err := os.WriteFile(path, []byte("new-unapproved-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	replayed, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
	if err != nil || replayed.Status != operationCompleted || replayed.Version != first.Version || replayed.Content != "" {
		t.Fatalf("completed read must return metadata receipt only: result=%+v err=%v", replayed, err)
	}
}

// Advance only the claim's clock by three minutes after capturing a snapshot.
// The operation's five-minute validity still holds; SQL and file access are real.
type claimSnapshotStore struct {
	state.Store
	state.CompareAndDeleteStore
	claimKey, stateKey string
	claimed            bool
	claimTTL           time.Duration
	afterSnapshot      func()
}

func (s *claimSnapshotStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	claimed, err := s.Store.SetNX(ctx, key, value, ttl)
	if claimed && key == s.claimKey {
		s.claimed, s.claimTTL = true, ttl
	}
	return claimed, err
}

func (s *claimSnapshotStore) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := s.Store.Get(ctx, key)
	if err == nil && key == s.stateKey && s.claimed && s.afterSnapshot != nil {
		after := s.afterSnapshot
		s.afterSnapshot = nil
		if s.claimTTL > 0 && s.claimTTL <= 3*time.Minute {
			if err := s.Store.Del(ctx, s.claimKey); err != nil {
				return nil, err
			}
		}
		after()
	}
	return value, err
}

func TestWorkspaceClaimExpiryCannotReplayAppend(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	ctx := context.Background()
	path := filepath.Join(grant.Path, "notes.txt")
	if err := os.WriteFile(path, []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationAppend, Path: "notes.txt", Content: "+append", ExpectedVersion: digestString("seed"), CallID: operationTestCallID("stalled-append")}
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	delayed := &claimSnapshotStore{Store: stateStore, CompareAndDeleteStore: stateStore.(state.CompareAndDeleteStore),
		claimKey: operationLockKey(prepared.OperationID), stateKey: operationKey(prepared.OperationID)}
	succeeded := 0
	interleaved := false
	delayed.afterSnapshot = func() {
		interleaved = true
		result, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
		if err != nil {
			requireWorkspaceReason(t, err, 409, "conflict", "binding_conflict")
			return
		}
		if result.Content != "seed+append" {
			t.Fatalf("second execution did not append: %+v", result)
		}
		succeeded++
		// Undo the second request's append before the stalled request resumes.
		if err := os.WriteFile(path, []byte("seed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := ExecuteOperation(ctx, db.DB, delayed, prepared.OperationID, req)
	if err == nil {
		if result.Content == "seed+append" {
			succeeded++
		} else if result.Status != operationCompleted || result.Content != "" {
			t.Fatalf("unexpected execution result: %+v", result)
		}
	} else {
		requireWorkspaceReason(t, err, 409, "conflict", "binding_conflict")
	}
	if !interleaved || succeeded != 1 {
		t.Fatalf("interleaved=%v, actual executions=%d, want one (receipts excluded)", interleaved, succeeded)
	}
}

// Deliberately expose only Store, without its optional CompareAndDelete method.
type recordingDeleteStore struct {
	state.Store
	deleted []string
}

func (s *recordingDeleteStore) Del(ctx context.Context, keys ...string) error {
	s.deleted = append(s.deleted, keys...)
	return s.Store.Del(ctx, keys...)
}

func TestWorkspaceClaimNeverUsesNonAtomicDelete(t *testing.T) {
	for _, action := range []string{"execute", "decide"} {
		t.Run(action, func(t *testing.T) {
			db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
			ctx := context.Background()
			req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
				Operation: OperationCreate, Path: "notes.txt", Content: "seed", CallID: operationTestCallID("no-unsafe-release")}
			prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
			if err != nil {
				t.Fatal(err)
			}
			recording := &recordingDeleteStore{Store: stateStore}
			if action == "decide" {
				_, err = DecideOperation(ctx, db.DB, recording, prepared.OperationID, "allow_once", "owner")
			} else {
				if _, err := DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
					t.Fatal(err)
				}
				_, err = ExecuteOperation(ctx, db.DB, recording, prepared.OperationID, req)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(recording.deleted) != 0 {
				t.Fatalf("claim released with non-atomic Del: %v", recording.deleted)
			}
		})
	}
}

func TestWorkspaceClaimInvalidExecutionDoesNotConsumeApproval(t *testing.T) {
	for _, field := range []string{"owner", "call_id", "content"} {
		t.Run(field, func(t *testing.T) {
			db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
			ctx := context.Background()
			req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
				Operation: OperationCreate, Path: "notes.txt", Content: "seed", CallID: operationTestCallID("valid-call")}
			prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
				t.Fatal(err)
			}
			invalid := req
			switch field {
			case "owner":
				invalid.UserID = "other-owner"
			case "call_id":
				invalid.CallID = operationTestCallID("other-call")
			case "content":
				invalid.Content = "tampered"
			}
			_, err = ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, invalid)
			requireWorkspaceReason(t, err, 409, "conflict", "binding_conflict")
			result, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
			if err != nil || result.Content != "seed" {
				t.Fatalf("invalid request consumed valid approval: result=%+v err=%v", result, err)
			}
		})
	}
}

// Fail one state call before delegating; subsequent calls use the real store.
type onceOperationStateFailure struct {
	state.Store
	state.CompareAndDeleteStore
	getRemaining, setNXRemaining int
	err                          error
}

func (s *onceOperationStateFailure) Get(ctx context.Context, key string) ([]byte, error) {
	if s.getRemaining > 0 {
		s.getRemaining--
		if s.getRemaining == 0 {
			return nil, s.err
		}
	}
	return s.Store.Get(ctx, key)
}

func (s *onceOperationStateFailure) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if s.setNXRemaining > 0 {
		s.setNXRemaining--
		if s.setNXRemaining == 0 {
			return false, s.err
		}
	}
	return s.Store.SetNX(ctx, key, value, ttl)
}

func TestWorkspaceOperationStateReadAndClaimFailures(t *testing.T) {
	for _, test := range []struct {
		name               string
		prepare            bool
		getCall, setNXCall int
	}{
		{name: "prepare SetNX", prepare: true, setNXCall: 1},
		{name: "execute lock SetNX", setNXCall: 1},
		{name: "execute first Get", getCall: 1},
		{name: "execute Get after claim", getCall: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, grant, ss, conversationID := operationFixture(t, PermissionAllowAll)
			ctx := t.Context()
			req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID,
				WorkspaceID: grant.WorkspaceID, Operation: OperationAppend, Path: "notes.txt", Content: "!",
				ExpectedVersion: digestString("seed"), CallID: operationTestCallID("state-call")}
			path := filepath.Join(grant.Path, req.Path)
			if err := os.WriteFile(path, []byte("seed"), 0600); err != nil {
				t.Fatal(err)
			}
			var prepared OperationResult
			var err error
			if !test.prepare {
				prepared, err = PrepareOperation(ctx, db.DB, ss, req)
				if err != nil {
					t.Fatal(err)
				}
			}
			failure := errors.New("injected state read/claim failure")
			failing := &onceOperationStateFailure{Store: ss, CompareAndDeleteStore: ss.(state.CompareAndDeleteStore), getRemaining: test.getCall, setNXRemaining: test.setNXCall, err: failure}
			if test.prepare {
				_, err = PrepareOperation(ctx, db.DB, failing, req)
			} else {
				_, err = ExecuteOperation(ctx, db.DB, failing, prepared.OperationID, req)
			}
			if !errors.Is(err, failure) || failing.getRemaining != 0 || failing.setNXRemaining != 0 {
				t.Fatalf("fault was not surfaced at its call: err=%v remaining Get/SetNX=%d/%d", err, failing.getRemaining, failing.setNXRemaining)
			}
			if content, err := os.ReadFile(path); err != nil || string(content) != "seed" {
				t.Fatalf("failed call changed file: %q, %v", content, err)
			}
			recovered, err := PrepareOperation(ctx, db.DB, failing, req)
			if err != nil || (!test.prepare && recovered.OperationID != prepared.OperationID) {
				t.Fatalf("prepare recovery: %+v, %v", recovered, err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				result, err := ExecuteOperation(ctx, db.DB, failing, recovered.OperationID, req)
				if err != nil || result.Status != operationCompleted {
					t.Fatalf("recovered execute %d: %+v, %v", attempt, result, err)
				}
			}
			want := "seed!"
			if content, err := os.ReadFile(path); err != nil || string(content) != want {
				t.Fatalf("recovery/replay content=%q, want %q, err=%v", content, want, err)
			}
		})
	}
}

type failedOperationWriteStore struct {
	state.Store
	state.CompareAndDeleteStore
	key string
	err error
}

type failedOperationIndexWriteStore struct {
	state.Store
	fail bool
	err  error
}

func (s *failedOperationIndexWriteStore) HSet(ctx context.Context, key string, fields map[string]any, ttl time.Duration) error {
	if s.fail && strings.HasPrefix(key, "local-workspace-operation-index:") {
		s.fail = false
		return s.err
	}
	return s.Store.HSet(ctx, key, fields, ttl)
}

func TestWorkspacePrepareClaimRecoversAfterIndexWriteFailure(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID,
		WorkspaceID: grant.WorkspaceID, Operation: OperationCreate, Path: "recovered.txt", Content: "seed", CallID: operationTestCallID("prepare-recovery")}
	failing := &failedOperationIndexWriteStore{Store: stateStore, fail: true, err: errors.New("index unavailable")}
	if _, err := PrepareOperation(t.Context(), db.DB, failing, req); !errors.Is(err, failing.err) {
		t.Fatalf("expected index failure, got %v", err)
	}
	recovered, err := PrepareOperation(t.Context(), db.DB, failing, req)
	if err != nil || recovered.Decision != DecisionPending || recovered.OperationID == "" {
		t.Fatalf("prepare did not recover: %+v, %v", recovered, err)
	}
}

func TestMkdirFailureDoesNotMarkOperationTouched(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("already-exists", 0o700); err != nil {
		t.Fatal(err)
	}
	touched := false
	_, err = executeFileOperation(t.Context(), root, "already-exists", nil, OperationRequest{Operation: OperationMkdir}, "allow_all", &touched, func() error { return nil })
	if err == nil || touched {
		t.Fatalf("mkdir failure touched=%v err=%v", touched, err)
	}
}

func (s *failedOperationWriteStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if key == s.key {
		return s.err
	}
	return s.Store.Set(ctx, key, value, ttl)
}

func TestWorkspaceClaimStateFailureCanRecoverForSameOwner(t *testing.T) {
	for _, action := range []string{"execute", "decide"} {
		t.Run(action, func(t *testing.T) {
			db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
			ctx := context.Background()
			req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
				Operation: OperationCreate, Path: "notes.txt", Content: "seed", CallID: operationTestCallID("state-failure")}
			prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("operation state write unavailable")
			failing := &failedOperationWriteStore{Store: stateStore, CompareAndDeleteStore: stateStore.(state.CompareAndDeleteStore),
				key: operationKey(prepared.OperationID), err: failure}
			if action == "execute" {
				if _, err := DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
					t.Fatal(err)
				}
				_, err = ExecuteOperation(ctx, db.DB, failing, prepared.OperationID, req)
			} else {
				_, err = DecideOperation(ctx, db.DB, failing, prepared.OperationID, "allow_once", "owner")
			}
			if !errors.Is(err, failure) {
				t.Fatalf("expected state write error, got %v", err)
			}
			if _, err := os.Stat(filepath.Join(grant.Path, req.Path)); !os.IsNotExist(err) {
				t.Fatalf("file exists after state failure: %v", err)
			}
			// A same-owner retry resumes the half-completed claim.
			if action == "execute" {
				_, err = ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
				if err != nil {
					t.Fatalf("execute recovery failed: %v", err)
				}
			} else {
				_, err = DecideOperation(ctx, db.DB, stateStore, prepared.OperationID, "allow_once", "owner")
				if err != nil {
					t.Fatalf("decision recovery failed: %v", err)
				}
			}
			if action == "execute" {
				if _, statErr := os.Stat(filepath.Join(grant.Path, req.Path)); statErr != nil {
					t.Errorf("recovered execution did not create the file: %v", statErr)
				}
			}
		})
	}
}

func TestWorkspacePrepareIdentityCapacityAndExpiry(t *testing.T) {
	db, grant, stateStore, conversation := operationFixture(t, PermissionAlwaysAsk)
	ctx := context.Background()
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: operationTestCallID("same"), Operation: OperationCreate, Path: "a.txt", Content: "a"}
	first, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil || again.OperationID != first.OperationID {
		t.Fatalf("retry: %+v %v", again, err)
	}
	changed := req
	changed.Content = "changed"
	if _, err := PrepareOperation(ctx, db.DB, stateStore, changed); err == nil {
		t.Fatal("reused identity changed content")
	}
	results := make(chan OperationResult, 24)
	failures := make(chan error, 24)
	var workers sync.WaitGroup
	for i := 0; i < 24; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			call := req
			call.CallID = operationTestCallID(fmt.Sprint(i))
			result, err := PrepareOperation(ctx, db.DB, stateStore, call)
			results <- result
			failures <- err
		}(i)
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	pending, full := 1, 0
	for result := range results {
		if result.Status == operationPending {
			pending++
		}
		if result.Reason == "approval_capacity" {
			full++
		}
	}
	if pending != 16 || full != 9 {
		t.Fatalf("pending=%d full=%d", pending, full)
	}
	value, err := loadOperationState(ctx, stateStore, first.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	value.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	if err := saveOperationState(ctx, stateStore, value); err != nil {
		t.Fatal(err)
	}
	expired, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil || expired.Status != operationExpired {
		t.Fatalf("expired: %+v %v", expired, err)
	}
	req.CallID = operationTestCallID("after-expiry")
	result, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil || result.Status != operationPending {
		t.Fatalf("freed slot: %+v %v", result, err)
	}
}

func TestWorkspaceDiscoveryAndTextLimits(t *testing.T) {
	db, grant, stateStore, conversation := operationFixture(t, PermissionAskAsNeeded)
	ctx := context.Background()
	for name, content := range map[string]string{"notes.txt": "visible needle", ".env": "secret needle", "binary.txt": "a\x00b"} {
		if err := os.WriteFile(filepath.Join(grant.Path, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: operationTestCallID("search"), Operation: OperationGrep, Path: ".", Pattern: "needle"}
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(result.Data)
	if strings.Contains(string(data), "secret needle") || !strings.Contains(string(data), "visible needle") || !strings.Contains(string(data), "approval_required") {
		t.Fatalf("search: %s", data)
	}
	req.Operation, req.Path, req.CallID = OperationRead, "binary.txt", operationTestCallID("binary")
	prepared, err = PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req); err == nil {
		t.Fatal("binary read accepted")
	}
	req.Path, req.CallID = ".env", operationTestCallID("secret")
	prepared, err = PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil || prepared.Status != operationPending || prepared.Version != "" {
		t.Fatalf("sensitive prepare must not return content hash: %+v %v", prepared, err)
	}
}

func TestWorkspacePinnedRootAndCommitConflict(t *testing.T) {
	db, grant, _, conversation := operationFixture(t, PermissionAllowAll)
	ctx := context.Background()
	snapshot, err := ResolveForConversation(ctx, db.DB, "owner", conversation)
	if err != nil {
		t.Fatal(err)
	}
	original := grant.Path + "-original"
	if err := os.Rename(grant.Path, original); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(original, grant.Path)
	if err := os.Mkdir(grant.Path, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(grant.Path)
	if root, _, _, err := openOperationPath(snapshot, "a.txt"); err == nil {
		root.Close()
		t.Fatal("replacement root accepted")
	}
	if err := os.Remove(grant.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(original, grant.Path); err != nil {
		t.Fatal(err)
	}
	parent, name, info, err := openOperationPath(snapshot, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	touched := false
	req := OperationRequest{Operation: OperationCreate, Path: "a.txt", Content: "ours"}
	_, err = executeFileOperation(ctx, parent, name, info, req, PermissionAllowAll, &touched, func() error { return os.WriteFile(filepath.Join(grant.Path, "a.txt"), []byte("external"), 0600) })
	if err == nil {
		t.Fatal("create replaced concurrent file")
	}
	actual, _ := os.ReadFile(filepath.Join(grant.Path, "a.txt"))
	if string(actual) != "external" {
		t.Fatalf("actual %q", actual)
	}
}

func TestWorkspaceReplacePreservesPermissionsAndCount(t *testing.T) {
	db, grant, stateStore, conversation := operationFixture(t, PermissionAllowAll)
	ctx := context.Background()
	file := filepath.Join(grant.Path, "notes.txt")
	if err := os.WriteFile(file, []byte("aa aa"), 0640); err != nil {
		t.Fatal(err)
	}
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: operationTestCallID("replace"), Operation: OperationReplace, Path: "notes.txt", OldContent: "aa", Content: "bb", ExpectedVersion: digestString("aa aa"), ExpectedReplacements: 2}
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
	if err != nil || result.Content != "bb bb" {
		t.Fatalf("replace %+v %v", result, err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0640 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	req.CallID, req.ExpectedVersion, req.OldContent, req.ExpectedReplacements = operationTestCallID("wrong-count"), result.Version, "bb", 1
	prepared, err = PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req); err == nil {
		t.Fatal("wrong replacement count succeeded")
	}
	actual, _ := os.ReadFile(file)
	if string(actual) != "bb bb" {
		t.Fatal("partial edit")
	}
}

type completionFailureStore struct{ state.Store }

func (s completionFailureStore) Set(ctx context.Context, key string, content []byte, ttl time.Duration) error {
	var value operationState
	if strings.HasPrefix(key, "local-workspace-operation:") && json.Unmarshal(content, &value) == nil && value.Status == operationCompleted {
		return errors.New("injected completion save failure")
	}
	return s.Store.Set(ctx, key, content, ttl)
}
func TestWorkspaceCompletionFailureIsUncertainAndCannotReplay(t *testing.T) {
	db, grant, stateStore, conversation := operationFixture(t, PermissionAllowAll)
	ctx := context.Background()
	file := filepath.Join(grant.Path, "notes.txt")
	if err := os.WriteFile(file, []byte("seed"), 0600); err != nil {
		t.Fatal(err)
	}
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: operationTestCallID("uncertain"), Operation: OperationAppend, Path: "notes.txt", Content: "+one", ExpectedVersion: digestString("seed")}
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteOperation(ctx, db.DB, completionFailureStore{stateStore}, prepared.OperationID, req); err == nil {
		t.Fatal("completion storage failure hidden")
	}
	value, err := loadOperationState(ctx, stateStore, prepared.OperationID)
	if err != nil || value.Status != operationUncertain {
		t.Fatalf("state %+v %v", value, err)
	}
	if _, err := ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req); err == nil {
		t.Fatal("uncertain replay succeeded")
	}
	data, _ := os.ReadFile(file)
	if string(data) != "seed+one" {
		t.Fatalf("data %q", data)
	}
}

func TestWorkspaceDeleteRechecksFileAfterLifecycleValidation(t *testing.T) {
	db, grant, _, conversation := operationFixture(t, PermissionAllowAll)
	ctx := context.Background()
	snapshot, err := ResolveForConversation(ctx, db.DB, "owner", conversation)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(grant.Path, "delete.txt")
	if err := os.WriteFile(target, []byte("approved"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent, name, info, err := openOperationPath(snapshot, "delete.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	touched := false
	req := OperationRequest{Operation: OperationDelete, ExpectedVersion: digestString("approved")}
	_, err = executeFileOperation(ctx, parent, name, info, req, PermissionAllowAll, &touched, func() error {
		return os.WriteFile(target, []byte("replacement"), 0o600)
	})
	if err == nil {
		t.Fatal("delete removed a file changed after validation")
	}
	if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "replacement" {
		t.Fatalf("replacement=%q err=%v", data, readErr)
	}
}

func TestWorkspaceExpiredAtCommitDoesNotMutate(t *testing.T) {
	db, grant, _, conversation := operationFixture(t, PermissionAllowAll)
	ctx := context.Background()
	snapshot, err := ResolveForConversation(ctx, db.DB, "owner", conversation)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []OperationKind{OperationCreate, OperationMkdir, OperationAppend, OperationDelete} {
		t.Run(string(kind), func(t *testing.T) {
			if kind == OperationAppend || kind == OperationDelete {
				if err := os.WriteFile(filepath.Join(grant.Path, "target"), []byte("seed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			parent, name, info, err := openOperationPath(snapshot, "target")
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			touched := false
			req := OperationRequest{Operation: kind, Content: "+one", ExpectedVersion: digestString("seed")}
			_, err = executeFileOperation(ctx, parent, name, info, req, PermissionAllowAll, &touched, func() error { return Error("selection_expired", 409, "conflict") })
			if err == nil || touched {
				t.Fatalf("expired commit %v touched=%v", err, touched)
			}
			if kind == OperationAppend || kind == OperationDelete {
				data, _ := os.ReadFile(filepath.Join(grant.Path, "target"))
				if string(data) != "seed" {
					t.Fatalf("mutated %q", data)
				}
				os.Remove(filepath.Join(grant.Path, "target"))
			}
		})
	}
}

func operationTestCallID(id string) string { return fmt.Sprintf("%d/%s", time.Now().UnixMilli(), id) }

func TestWorkspaceExpiredCallCannotRecreateAfterReceiptEviction(t *testing.T) {
	db, grant, stateStore, conversation := operationFixture(t, PermissionAlwaysAsk)
	req := OperationRequest{HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: fmt.Sprintf("%d/old-call", time.Now().Add(-25*time.Hour).UnixMilli()), Operation: OperationCreate, Path: "old.txt", Content: "old"}
	if _, err := PrepareOperation(t.Context(), db.DB, stateStore, req); err == nil {
		t.Fatal("expired call recreated without its receipt")
	}
	if _, err := os.Stat(filepath.Join(grant.Path, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected file %v", err)
	}
}

func TestWorkspaceRecursiveGlobIncludesZeroAndManyDirectories(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"src/**/*.go", "src/main.go", true}, {"src/**/*.go", "src/a/b/main.go", true},
		{"**/docs/*.md", "docs/readme.md", true}, {"**/docs/*.md", "a/b/docs/readme.md", true},
		{"src/**/*.go", "other/main.go", false}, {"*.go", "src/main.go", true},
	} {
		got, err := matchOperationGlob(tc.pattern, tc.path)
		if err != nil || got != tc.want {
			t.Errorf("%s %s = %v %v", tc.pattern, tc.path, got, err)
		}
	}
}
