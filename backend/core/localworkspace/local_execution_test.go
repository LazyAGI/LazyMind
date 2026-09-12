package localworkspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func localRequest(t *testing.T, grant PublicWorkspace, conversation, path string, kind OperationKind) OperationRequest {
	t.Helper()
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(parent, filepath.Base(path))
	parentIdentity, err := currentDirectoryIdentity(parent)
	if err != nil {
		t.Fatal(err)
	}
	targetIdentity := "missing"
	if info, err := os.Lstat(path); err == nil {
		targetIdentity, err = platformDirectoryIdentity(path, info)
		if err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return OperationRequest{ExecutionMode: localExecutionMode, ArgumentsDigest: digestString("prepared arguments"),
		ParentIdentity: parentIdentity, TargetIdentity: targetIdentity, UserID: "owner", ConversationID: conversation,
		WorkspaceID: grant.WorkspaceID, HistoryID: "history", RunID: "run", CallID: operationTestCallID("local"),
		Operation: kind, Path: path, ToolName: "LocalFileToolkit_" + string(kind)}
}

func TestLocalOperationExternalAlwaysAsksAndCoreNeverExecutes(t *testing.T) {
	for _, mode := range []string{PermissionAlwaysAsk, PermissionAskAsNeeded, PermissionAllowAll} {
		t.Run(mode, func(t *testing.T) {
			db, grant, states, conversation := operationFixture(t, mode)
			target := filepath.Join(t.TempDir(), "outside.txt")
			req := localRequest(t, grant, conversation, target, OperationCreate)
			req.Content = "approved content"
			prepared, err := PrepareOperation(t.Context(), db.DB, states, req)
			if err != nil || prepared.Decision != DecisionPending || prepared.Content != "" {
				t.Fatalf("prepare=%+v err=%v", prepared, err)
			}
			_, err = ClaimLocalOperation(t.Context(), db.DB, states, prepared.OperationID, req)
			requireWorkspaceReason(t, err, 403, "forbidden", "selection_forbidden")
			_, err = DecideOperation(t.Context(), db.DB, states, prepared.OperationID, "allow_once", "other")
			requireWorkspaceReason(t, err, 404, "resource not found", "workspace_not_found")
			if _, err := DecideOperation(t.Context(), db.DB, states, prepared.OperationID, "allow_once", "owner"); err != nil {
				t.Fatal(err)
			}
			_, err = ExecuteOperation(t.Context(), db.DB, states, prepared.OperationID, req)
			requireWorkspaceReason(t, err, 403, "forbidden", "selection_forbidden")
			claim, err := ClaimLocalOperation(t.Context(), db.DB, states, prepared.OperationID, req)
			if err != nil || !claim.ExecuteAllowed || claim.Status != operationExecuting || claim.TargetIdentity != "missing" {
				t.Fatalf("claim=%+v err=%v", claim, err)
			}
			if _, err := os.Stat(req.Path); !os.IsNotExist(err) {
				t.Fatalf("Core touched local target: %v", err)
			}
			if _, err := ClaimLocalOperation(t.Context(), db.DB, states, prepared.OperationID, req); err == nil {
				t.Fatal("claim replayed")
			}
			if err := os.WriteFile(req.Path, []byte(req.Content), 0o600); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(req.Path)
			identity, _ := platformDirectoryIdentity(req.Path, info)
			completion := LocalOperationCompletion{OperationRequest: req, Status: operationCompleted, Version: digestString(req.Content), ResultIdentity: identity}
			for i := 0; i < 2; i++ {
				result, err := CompleteLocalOperation(t.Context(), states, prepared.OperationID, completion)
				if err != nil || result.Status != operationCompleted || result.ExecuteAllowed {
					t.Fatalf("completion=%+v err=%v", result, err)
				}
			}
			completion.Version = digestString("changed")
			if _, err := CompleteLocalOperation(t.Context(), states, prepared.OperationID, completion); err == nil {
				t.Fatal("changed completion accepted")
			}
			raw, _ := states.Get(t.Context(), operationKey(prepared.OperationID))
			if strings.Contains(string(raw), req.Content) {
				t.Fatal("completion state contains file content")
			}
		})
	}
}

func TestLocalOperationDenialRevocationExpiryAndTampering(t *testing.T) {
	for _, scenario := range []string{"denied", "revoked", "expired", "arguments", "path", "parent", "target", "owner", "run", "cloud"} {
		t.Run(scenario, func(t *testing.T) {
			db, grant, states, conversation := operationFixture(t, PermissionAlwaysAsk)
			req := localRequest(t, grant, conversation, filepath.Join(t.TempDir(), "outside.txt"), OperationCreate)
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
				req.ArgumentsDigest = digestString("different")
			case "path":
				req.Path += "different"
			case "parent":
				req.ParentIdentity = "different"
			case "target":
				if err := os.WriteFile(req.Path, []byte("unapproved replacement"), 0o600); err != nil {
					t.Fatal(err)
				}
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
	req := localRequest(t, grant, conversation, filepath.Join(grant.Path, "new.txt"), OperationCreate)
	prepared, err := PrepareOperation(t.Context(), db.DB, states, req)
	if err != nil {
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

func TestLocalOperationVersionDependencyAndLateCompletion(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAllowAll)
	target := filepath.Join(grant.Path, "notes.txt")
	if err := os.WriteFile(target, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	read := localRequest(t, grant, conversation, target, OperationRead)
	first, err := PrepareOperation(t.Context(), db.DB, states, read)
	if err != nil {
		t.Fatal(err)
	}
	appendReq := localRequest(t, grant, conversation, target, OperationAppend)
	appendReq.DependsOn, appendReq.Content = first.OperationID, "+"
	second, err := PrepareOperation(t.Context(), db.DB, states, appendReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimLocalOperation(t.Context(), db.DB, states, second.OperationID, appendReq); err == nil {
		t.Fatal("uncompleted dependency executed")
	}
	if _, err := ClaimLocalOperation(t.Context(), db.DB, states, first.OperationID, read); err != nil {
		t.Fatal(err)
	}
	if _, err := CompleteLocalOperation(t.Context(), states, first.OperationID, LocalOperationCompletion{OperationRequest: read, Status: operationCompleted, Version: digestString("before"), ResultIdentity: read.TargetIdentity}); err != nil {
		t.Fatal(err)
	}
	claim, err := ClaimLocalOperation(t.Context(), db.DB, states, second.OperationID, appendReq)
	if err != nil || claim.Version != digestString("before") {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if err := db.Model(&orm.LocalWorkspace{}).Where("id = ?", grant.WorkspaceID).Updates(map[string]any{"status": StatusRevoked, "version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := CompleteLocalOperation(t.Context(), states, second.OperationID, LocalOperationCompletion{OperationRequest: appendReq, Status: operationFailed, Reason: "execution_inactive"})
	if err != nil || result.Status != operationFailed {
		t.Fatalf("late completion=%+v err=%v", result, err)
	}
}

func TestLocalOperationModelNoticeDoesNotLeakInternalProtocol(t *testing.T) {
	notice := ModelNotice(ContextSnapshot{WorkspaceID: "hidden-workspace", Root: "/project", PermissionMode: PermissionAllowAll, PermissionVersion: 99})
	for _, secret := range []string{"hidden-workspace", "workspace_id", "permission_version", "source_id"} {
		if strings.Contains(notice, secret) {
			t.Fatalf("model notice contains %s", secret)
		}
	}
	if !strings.Contains(notice, "工作区外的绝对路径需要用户批准") {
		t.Fatal("missing external path approval notice")
	}
}
