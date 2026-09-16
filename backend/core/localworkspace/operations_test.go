package localworkspace

import (
	"context"
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

// Advance only the claim's clock by three minutes after capturing a snapshot.
// The operation's five-minute validity still holds; SQL and approval state are real.
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
	req := OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversationID,
		WorkspaceID: grant.WorkspaceID, Operation: OperationWrite, Path: filepath.Join(grant.Path, "recovered.txt"), CallID: operationTestCallID("prepare-recovery")}
	failing := &failedOperationIndexWriteStore{Store: stateStore, fail: true, err: errors.New("index unavailable")}
	if _, err := PrepareOperation(t.Context(), db.DB, failing, req); !errors.Is(err, failing.err) {
		t.Fatalf("expected index failure, got %v", err)
	}
	recovered, err := PrepareOperation(t.Context(), db.DB, failing, req)
	if err != nil || recovered.Decision != DecisionPending || recovered.OperationID == "" {
		t.Fatalf("prepare did not recover: %+v, %v", recovered, err)
	}
}

func (s *failedOperationWriteStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if key == s.key {
		return s.err
	}
	return s.Store.Set(ctx, key, value, ttl)
}

func TestWorkspacePrepareIdentityCapacityAndExpiry(t *testing.T) {
	db, grant, stateStore, conversation := operationFixture(t, PermissionAlwaysAsk)
	ctx := context.Background()
	req := OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: operationTestCallID("same"), Operation: OperationWrite, Path: filepath.Join(grant.Path, "a.txt")}
	first, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := PrepareOperation(ctx, db.DB, stateStore, req)
	if err != nil || again.OperationID != first.OperationID {
		t.Fatalf("retry: %+v %v", again, err)
	}
	changed := req
	changed.ArgumentsDigest = digestBytes([]byte("changed"))
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

func operationTestCallID(id string) string { return fmt.Sprintf("%d/%s", time.Now().UnixMilli(), id) }

func TestWorkspaceExpiredCallCannotRecreateAfterReceiptEviction(t *testing.T) {
	db, grant, stateStore, conversation := operationFixture(t, PermissionAlwaysAsk)
	req := OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ToolName: "write", ArgumentsDigest: digestBytes([]byte("arguments")), HistoryID: "history", RunID: "run", UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, CallID: fmt.Sprintf("%d/old-call", time.Now().Add(-25*time.Hour).UnixMilli()), Operation: OperationWrite, Path: filepath.Join(grant.Path, "old.txt")}
	if _, err := PrepareOperation(t.Context(), db.DB, stateStore, req); err == nil {
		t.Fatal("expired call recreated without its receipt")
	}
	if _, err := os.Stat(filepath.Join(grant.Path, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected file %v", err)
	}
}
