package localworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestHostOperationCompletionIsIdempotent(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAlwaysAsk)
	req := hostRequest(grant, conversation, "output.txt", OperationWrite)
	prepared, err := PrepareOperation(t.Context(), db.DB, states, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(t.Context(), db.DB, states, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimLocalOperation(t.Context(), db.DB, states, prepared.OperationID, req); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimLocalOperation(t.Context(), db.DB, states, prepared.OperationID, req); err == nil {
		t.Fatal("claim replayed")
	}
	if _, err := os.Stat(req.Path); !os.IsNotExist(err) {
		t.Fatalf("Core touched host path: %v", err)
	}
	if err := db.Model(&orm.LocalWorkspace{}).Where("id = ?", grant.WorkspaceID).Update("status", StatusRevoked).Error; err != nil {
		t.Fatal(err)
	}
	completion := LocalOperationCompletion{OperationRequest: req, Status: operationCompleted}
	for i := 0; i < 2; i++ {
		result, err := CompleteLocalOperation(t.Context(), states, prepared.OperationID, completion)
		if err != nil || result.Status != operationCompleted || result.ExecuteAllowed {
			t.Fatalf("completion=%+v err=%v", result, err)
		}
	}
	completion.Status = operationFailed
	if _, err := CompleteLocalOperation(t.Context(), states, prepared.OperationID, completion); err == nil {
		t.Fatal("conflicting completion accepted")
	}
}

func TestLocalOperationDenialRevocationExpiryAndTampering(t *testing.T) {
	for _, scenario := range []string{"denied", "revoked", "expired", "arguments", "path", "owner", "run", "cloud"} {
		t.Run(scenario, func(t *testing.T) {
			db, grant, states, conversation := operationFixture(t, PermissionAlwaysAsk)
			req := hostRequest(grant, conversation, filepath.Join(t.TempDir(), "outside.txt"), OperationWrite)
			prepared, err := PrepareOperation(t.Context(), db.DB, states, req)
			if err != nil {
				t.Fatal(err)
			}
			action := "allow_once"
			if scenario == "denied" {
				action = "reject"
			}
			if _, err := DecideOperation(t.Context(), db.DB, states, prepared.OperationID, action, "owner"); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "revoked":
				if err := db.Model(&orm.LocalWorkspace{}).Where("id = ?", grant.WorkspaceID).Updates(map[string]any{"status": StatusRevoked, "version": 2}).Error; err != nil {
					t.Fatal(err)
				}
			case "expired":
				value, _ := loadOperationState(t.Context(), states, prepared.OperationID)
				value.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
				if err := saveOperationState(t.Context(), states, value); err != nil {
					t.Fatal(err)
				}
			case "arguments":
				req.ArgumentsDigest = digestBytes([]byte("different"))
			case "path":
				req.Path += "different"
			case "owner":
				req.UserID = "other"
			case "run":
				req.RunID = "other"
			case "cloud":
				t.Setenv("LAZYMIND_RUNTIME_MODE", "cloud")
			}
			result, err := ClaimLocalOperation(t.Context(), db.DB, states, prepared.OperationID, req)
			if err == nil || result.ExecuteAllowed {
				t.Fatalf("unsafe claim=%+v err=%v", result, err)
			}
		})
	}
}

func TestLocalOperationSingleConcurrentClaim(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAllowAll)
	req := hostRequest(grant, conversation, filepath.Join(grant.Path, "new.txt"), OperationWrite)
	prepared, err := PrepareOperation(t.Context(), db.DB, states, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(t.Context(), db.DB, states, prepared.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan bool, 4)
	for i := 0; i < 4; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := ClaimLocalOperation(context.Background(), db.DB, states, prepared.OperationID, req)
			results <- err == nil && result.ExecuteAllowed
		}()
	}
	wait.Wait()
	close(results)
	count := 0
	for permitted := range results {
		if permitted {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("granted %d execution permits", count)
	}
}

func TestLocalOperationModelNoticeDoesNotLeakInternalProtocol(t *testing.T) {
	notice := ModelNotice(ContextSnapshot{WorkspaceID: "hidden-workspace", Root: "/project", PermissionMode: PermissionAllowAll, PermissionVersion: 99})
	for _, secret := range []string{"hidden-workspace", "workspace_id", "permission_version", "source_id"} {
		if strings.Contains(notice, secret) {
			t.Fatalf("model notice contains %s", secret)
		}
	}
	if !strings.Contains(notice, "写入和删除按权限模式审批") {
		t.Fatal("missing permission mode notice")
	}
}

func TestHostOperationRetiredModesCannotPrepareOrResume(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAlwaysAsk)
	for _, mode := range []string{"", "local"} {
		req := hostRequest(grant, conversation, "file.txt", OperationWrite)
		req.ExecutionMode = mode
		if _, err := PrepareOperation(t.Context(), db.DB, states, req); err == nil {
			t.Fatalf("retired mode %q prepared", mode)
		}
		id := "retired-" + mode
		value := operationState{OperationID: id, Request: req, Decision: DecisionAllowed, Status: operationAllowed, Slot: -1, ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}
		if err := saveOperationState(t.Context(), states, value); err != nil {
			t.Fatal(err)
		}
		if _, err := ClaimLocalOperation(t.Context(), db.DB, states, id, req); err == nil {
			t.Fatalf("retired mode %q claimed", mode)
		}
		if _, err := DecideOperation(t.Context(), db.DB, states, id, "allow_once", "owner"); err == nil {
			t.Fatalf("retired mode %q approved", mode)
		}
		if _, err := CompleteLocalOperation(t.Context(), states, id, LocalOperationCompletion{OperationRequest: req, Status: operationCompleted}); err == nil {
			t.Fatalf("retired mode %q completed", mode)
		}
	}
}

func TestHostOperationStateWriteFailureNeverReissuesClaim(t *testing.T) {
	for _, phase := range []string{"claim", "complete"} {
		t.Run(phase, func(t *testing.T) {
			db, grant, states, conversation := operationFixture(t, PermissionAlwaysAsk)
			req := hostRequest(grant, conversation, "file.txt", OperationWrite)
			prepared, err := PrepareOperation(t.Context(), db.DB, states, req)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecideOperation(t.Context(), db.DB, states, prepared.OperationID, "allow_once", "owner"); err != nil {
				t.Fatal(err)
			}
			failed := &failedOperationWriteStore{Store: states, key: operationKey(prepared.OperationID), err: errors.New("state unavailable")}
			if phase == "claim" {
				if result, err := ClaimLocalOperation(t.Context(), db.DB, failed, prepared.OperationID, req); err == nil || result.ExecuteAllowed {
					t.Fatalf("failed claim=%+v %v", result, err)
				}
			} else {
				if _, err := ClaimLocalOperation(t.Context(), db.DB, states, prepared.OperationID, req); err != nil {
					t.Fatal(err)
				}
				completion := LocalOperationCompletion{OperationRequest: req, Status: operationCompleted}
				if _, err := CompleteLocalOperation(t.Context(), failed, prepared.OperationID, completion); err == nil {
					t.Fatal("completion write failure hidden")
				}
				if result, err := CompleteLocalOperation(t.Context(), states, prepared.OperationID, completion); err != nil || result.Status != operationCompleted {
					t.Fatalf("completion recovery=%+v %v", result, err)
				}
			}
			if result, err := ClaimLocalOperation(t.Context(), db.DB, states, prepared.OperationID, req); err == nil || result.ExecuteAllowed {
				t.Fatalf("claim replay=%+v %v", result, err)
			}
		})
	}
}
