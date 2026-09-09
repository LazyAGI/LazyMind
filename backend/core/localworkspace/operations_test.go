package localworkspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/state"
)

func operationFixture(t *testing.T, mode string) (*orm.DB, PublicWorkspace, state.Store, string) {
	t.Helper()
	db, grant := workspaceFixture(t)
	now := time.Now().UTC()
	conversationID := "operation-task-" + mode
	conversation := orm.Conversation{ID: conversationID, IsTaskConv: true,
		BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationWorkspaceBinding{ConversationID: conversation.ID, WorkspaceID: grant.WorkspaceID, PermissionMode: mode, PermissionVersion: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	return db, grant, stateStore, conversationID
}

func TestWorkspaceOperationRoundTripAndVersionConflict(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	ctx := context.Background()
	base := OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID}

	createReq := base
	createReq.Operation, createReq.Path, createReq.Content, createReq.CallID = OperationCreate, "notes.txt", "one", "create-1"
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
	appendReq.Operation, appendReq.Path, appendReq.Content, appendReq.ExpectedVersion, appendReq.CallID = OperationAppend, "notes.txt", "\ntwo", created.Version, "append-1"
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
	replaceReq.Operation, replaceReq.Path, replaceReq.OldContent, replaceReq.Content, replaceReq.ExpectedVersion, replaceReq.CallID = OperationReplace, "notes.txt", "two", "THREE", appended.Version, "replace-1"
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
	readReq.Operation, readReq.Path, readReq.CallID = OperationRead, "notes.txt", "read-1"
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
	deleteReq.Operation, deleteReq.Path, deleteReq.ExpectedVersion, deleteReq.CallID = OperationDelete, "notes.txt", replaced.Version, "delete-1"
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
		_, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: OperationRead, Path: path, CallID: "bad-" + path})
		if err == nil {
			t.Errorf("path %q unexpectedly allowed", path)
		}
	}
}

func TestWorkspaceOperationRejectsSensitiveMutationEvenWhenAllAllowed(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	_, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{
		UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationCreate, Path: ".env", Content: "SECRET=x", CallID: "sensitive-create",
	})
	if err == nil {
		t.Fatal("sensitive mutation unexpectedly allowed")
	}
}

func TestWorkspaceOperationRechecksPermissionBeforeExecution(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	req := OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: OperationCreate, Path: "permission.txt", Content: "ok", CallID: "permission-call"}
	prepared, err := PrepareOperation(context.Background(), db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.ConversationWorkspaceBinding{}).Where("conversation_id = ?", conversationID).Updates(map[string]any{"permission_mode": PermissionAlwaysAsk, "permission_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteOperation(context.Background(), db.DB, stateStore, prepared.OperationID, req); err == nil {
		t.Fatal("operation executed after permission tightened")
	}
	if _, err := os.Stat(filepath.Join(grant.Path, "permission.txt")); !os.IsNotExist(err) {
		t.Fatalf("file stat=%v", err)
	}
}

func TestWorkspaceOperationRejectsSymlinkedParentOutsideRoot(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAllowAll)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(grant.Path, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := PrepareOperation(context.Background(), db.DB, stateStore, OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: OperationCreate, Path: "linked/escape.txt", Content: "x", CallID: "linked-create"})
	if err == nil {
		t.Fatal("symlinked parent unexpectedly allowed")
	}
}

func TestWorkspaceOperationStateStoresDigestsInsteadOfContent(t *testing.T) {
	db, grant, stateStore, conversationID := operationFixture(t, PermissionAlwaysAsk)
	req := OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: OperationCreate, Path: "digest.txt", Content: "private-content", CallID: "digest-call"}
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
	req := OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID, Operation: OperationCreate, Path: "completed-state.txt", Content: "private-content", CallID: "completed-call"}
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
				req := OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
					Operation: OperationRead, Path: path, CallID: "read-approval"}
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
					if _, err := DecideOperation(ctx, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
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

// Pause at the state-store boundary after ExecuteOperation has read its snapshot.
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
	req := OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationAppend, Path: "notes.txt", Content: "+append", ExpectedVersion: digestString("seed"), CallID: "same-call"}
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
	req := OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationAppend, Path: "notes.txt", Content: "+append", ExpectedVersion: digestString("seed"), CallID: "failed-call"}
	prepared, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(ctx, stateStore, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external-edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ExecuteOperation(ctx, db.DB, stateStore, prepared.OperationID, req)
	requireWorkspaceReason(t, err, 409, "conflict", "binding_conflict")
	failed, err := OperationStatus(ctx, stateStore, prepared.OperationID)
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
	req := OperationRequest{UserID: "owner", ConversationID: conversationID, WorkspaceID: grant.WorkspaceID,
		Operation: OperationRead, Path: "notes.txt", CallID: "read-once"}
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
