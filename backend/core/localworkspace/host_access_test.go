package localworkspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

func hostRequest(grant PublicWorkspace, conversation, path string, kind OperationKind) OperationRequest {
	return OperationRequest{ExecutionMode: hostAccessExecutionMode, HostIntentID: "0", ArgumentsDigest: digestBytes([]byte("frozen arguments")),
		UserID: "owner", ConversationID: conversation, WorkspaceID: grant.WorkspaceID, HistoryID: "history", RunID: "run",
		CallID: operationTestCallID("host"), ToolName: "DocumentWriter_write", Operation: kind, Path: filepath.Join(grant.Path, path)}
}

func TestHostAccessBatchHeterogeneousIntentsAndNoCoreFilesystem(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAllowAll)
	snapshot, err := ResolveForConversation(t.Context(), db.DB, "owner", conversation)
	if err != nil {
		t.Fatal(err)
	}
	SetValidateOperationRunFunc(func(_ context.Context, _ *gorm.DB, _ state.Store, req OperationRequest) (*ContextSnapshot, error) {
		if req.RunID != "run" || req.HistoryID != "history" {
			return nil, Error("execution_inactive", 409, "conflict")
		}
		return snapshot, nil
	})
	// The workspace does not exist on Core; only stored metadata remains.
	if err := os.RemoveAll(grant.Path); err != nil {
		t.Fatal(err)
	}
	first := hostRequest(grant, conversation, "missing/nested/output.docx", OperationWrite)
	second := hostRequest(grant, conversation, "input/image.png", OperationRead)
	second.CallID, second.ToolName, second.HostIntentID = first.CallID, first.ToolName, "1"
	third := hostRequest(grant, conversation, "old/video.mp4", OperationDelete)
	third.ToolName, third.CallID = "MediaToolkit_convert", operationTestCallID("third")
	batch := OperationBatchRequest{Calls: []OperationRequest{first, second, third}}
	result, err := PrepareOperationBatch(t.Context(), db.DB, states, batch)
	if err != nil || len(result.Operations) != 3 {
		t.Fatalf("batch=%+v err=%v", result, err)
	}
	ids := map[string]bool{}
	for i, operation := range result.Operations {
		if operation.Decision != DecisionPending || operation.ExecuteAllowed || ids[operation.OperationID] {
			t.Fatalf("invalid admission %+v", operation)
		}
		ids[operation.OperationID] = true
		req := batch.Calls[i]
		if _, err := DecideOperation(t.Context(), db.DB, states, operation.OperationID, "allow_once", "owner"); err != nil {
			t.Fatal(err)
		}
		claim, err := ClaimLocalOperation(t.Context(), db.DB, states, operation.OperationID, req)
		if err != nil || !claim.ExecuteAllowed || claim.Status != operationExecuting {
			t.Fatalf("claim=%+v err=%v", claim, err)
		}
		if _, err := ClaimLocalOperation(t.Context(), db.DB, states, operation.OperationID, req); err == nil {
			t.Fatal("claim replay accepted")
		}
		completion := LocalOperationCompletion{OperationRequest: req, Status: operationCompleted}
		for retry := 0; retry < 2; retry++ {
			completed, err := CompleteLocalOperation(t.Context(), states, operation.OperationID, completion)
			if err != nil || completed.Status != operationCompleted || completed.ExecuteAllowed {
				t.Fatalf("complete=%+v err=%v", completed, err)
			}
		}
	}
	if _, err := os.Stat(grant.Path); !os.IsNotExist(err) {
		t.Fatalf("Core performed filesystem IO: %v", err)
	}
	if snapshot.Root != grant.Path || snapshot.PermissionMode != PermissionAllowAll {
		t.Fatal("shared run snapshot mutated")
	}
}

func TestHostAccessBatchRejectsMalformedEnvelopeBeforeAdmission(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAllowAll)
	first := hostRequest(grant, conversation, "out.bin", OperationWrite)
	cases := map[string]func(*OperationRequest){
		"missing intent":   func(r *OperationRequest) { r.HostIntentID = "" },
		"duplicate intent": func(r *OperationRequest) { *r = first },
		"wrong owner":      func(r *OperationRequest) { r.UserID = "other" },
		"wrong run":        func(r *OperationRequest) { r.RunID = "other" },
		"relative path":    func(r *OperationRequest) { r.Path = "relative" },
		"digest":           func(r *OperationRequest) { r.ArgumentsDigest = "bad" },
		"mode":             func(r *OperationRequest) { r.ExecutionMode = "local" },
		"kind":             func(r *OperationRequest) { r.Operation = "create" },
		"conflicting call": func(r *OperationRequest) { r.CallID = first.CallID; r.ToolName = "DifferentTool"; r.HostIntentID = "1" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			second := hostRequest(grant, conversation, "other.bin", OperationRead)
			mutate(&second)
			if _, err := PrepareOperationBatch(t.Context(), db.DB, states, OperationBatchRequest{Calls: []OperationRequest{first, second}}); err == nil {
				t.Fatal("invalid batch admitted")
			}
			index, err := states.HGetAll(t.Context(), "local-workspace-operation-index:"+conversation)
			if err != nil || len(index) != 0 {
				t.Fatalf("partial invalid batch persisted: %+v %v", index, err)
			}
		})
	}
	for _, calls := range [][]OperationRequest{nil, make([]OperationRequest, maxHostAccessBatch+1)} {
		if _, err := PrepareOperationBatch(t.Context(), db.DB, states, OperationBatchRequest{Calls: calls}); err == nil {
			t.Fatal("invalid batch size admitted")
		}
	}
	first.UserID = "other"
	if _, err := PrepareOperationBatch(t.Context(), db.DB, states, OperationBatchRequest{Calls: []OperationRequest{first}}); err == nil {
		t.Fatal("non-owner admitted")
	}
}

func TestHostAccessApprovalRejectionAndFrozenRunPermission(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAlwaysAsk)
	req := hostRequest(grant, conversation, "new/output.docx", OperationWrite)
	result, err := PrepareOperationBatch(t.Context(), db.DB, states, OperationBatchRequest{Calls: []OperationRequest{req}})
	if err != nil || result.Operations[0].Decision != DecisionPending {
		t.Fatalf("prepare=%+v %v", result, err)
	}
	id := result.Operations[0].OperationID
	if _, err := ClaimLocalOperation(t.Context(), db.DB, states, id, req); err == nil {
		t.Fatal("pending claimed")
	}
	if _, err := DecideOperation(t.Context(), db.DB, states, id, "reject", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimLocalOperation(t.Context(), db.DB, states, id, req); err == nil {
		t.Fatal("rejected claimed")
	}
	if err := db.Model(&orm.ConversationWorkspaceBinding{}).Where("conversation_id = ?", conversation).Updates(map[string]any{"permission_mode": PermissionAllowAll, "permission_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	req.CallID = operationTestCallID("frozen")
	result, err = PrepareOperationBatch(t.Context(), db.DB, states, OperationBatchRequest{Calls: []OperationRequest{req}})
	if err != nil || result.Operations[0].Decision != DecisionPending {
		t.Fatalf("permission not frozen: %+v %v", result, err)
	}
	id = result.Operations[0].OperationID
	if _, err := DecideOperation(t.Context(), db.DB, states, id, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	altered := req
	altered.ArgumentsDigest = digestBytes([]byte("changed"))
	if _, err := ClaimLocalOperation(t.Context(), db.DB, states, id, altered); err == nil {
		t.Fatal("changed arguments claimed")
	}
	if err := db.Model(&orm.LocalWorkspace{}).Where("id = ?", grant.WorkspaceID).Updates(map[string]any{"status": StatusRevoked, "version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimLocalOperation(t.Context(), db.DB, states, id, req); err == nil {
		t.Fatal("revoked workspace claimed")
	}
}

func TestHostAccessDoesNotRecomputeAllowAll(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAllowAll)
	req := hostRequest(grant, conversation, "unused", OperationRead)
	req.Path = filepath.Join(t.TempDir(), "missing/input.bin")
	result, err := PrepareOperationBatch(t.Context(), db.DB, states, OperationBatchRequest{Calls: []OperationRequest{req}})
	if err != nil || result.Operations[0].Decision != DecisionPending {
		t.Fatalf("external=%+v %v", result, err)
	}
}

func TestHostAccessBatchHTTPStrictBodyAndAuthenticatedIdentity(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAllowAll)
	store.Init(db.DB, nil, states)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "host-test-token")
	req := hostRequest(grant, conversation, "new/out.bin", OperationWrite)
	req.UserID, req.ConversationID = "spoofed", "spoofed"
	encoded, err := json.Marshal(OperationBatchRequest{Calls: []OperationRequest{req}})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"calls":[],"unknown":true}`, string(encoded) + ` {}`, `{"calls":null}`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		request.Header.Set("X-User-Id", "owner")
		request.Header.Set("X-LazyMind-Internal-Token", "host-test-token")
		request = mux.SetURLVars(request, map[string]string{"conversation_id": conversation})
		InternalPrepareOperationBatch(response, request)
		if response.Code != 400 {
			t.Fatalf("strict body status=%d %s", response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(encoded)))
	request.Header.Set("X-User-Id", "owner")
	request.Header.Set("X-LazyMind-Internal-Token", "host-test-token")
	request = mux.SetURLVars(request, map[string]string{"conversation_id": conversation})
	InternalPrepareOperationBatch(response, request)
	if response.Code != 200 {
		t.Fatalf("authenticated batch status=%d %s", response.Code, response.Body.String())
	}
}

func TestUnboundHostAccessUsesOwnedRunAndApproval(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAlwaysAsk)
	if err := db.Where("conversation_id = ?", conversation).Delete(&orm.ConversationWorkspaceBinding{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", conversation).Update("is_task_conv", false).Error; err != nil {
		t.Fatal(err)
	}
	run := UnboundContext()
	SetValidateOperationRunFunc(func(_ context.Context, _ *gorm.DB, _ state.Store, req OperationRequest) (*ContextSnapshot, error) {
		if req.RunID != "run" || req.HistoryID != "history" {
			return nil, Error("execution_inactive", 409, "conflict")
		}
		return run, nil
	})
	req := hostRequest(grant, conversation, "output.txt", OperationWrite)
	req.WorkspaceID = ""
	prepared, err := PrepareOperationBatch(t.Context(), db.DB, states, OperationBatchRequest{Calls: []OperationRequest{req}})
	if err != nil || len(prepared.Operations) != 1 || prepared.Operations[0].Decision != DecisionPending {
		t.Fatalf("unbound preparation: %+v %v", prepared, err)
	}
	id := prepared.Operations[0].OperationID
	if _, err := ClaimLocalOperation(t.Context(), db.DB, states, id, req); err == nil {
		t.Fatal("pending call executed")
	}
	if _, err := DecideOperation(t.Context(), db.DB, states, id, "allow_once", "other"); err == nil {
		t.Fatal("foreign approval accepted")
	}
	if _, err := DecideOperation(t.Context(), db.DB, states, id, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
	claimed, err := ClaimLocalOperation(t.Context(), db.DB, states, id, req)
	if err != nil || !claimed.ExecuteAllowed {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if _, err := ClaimLocalOperation(t.Context(), db.DB, states, id, req); err == nil {
		t.Fatal("duplicate execution accepted")
	}
	if _, err := os.Stat(req.Path); !os.IsNotExist(err) {
		t.Fatal("Core performed file IO")
	}
}

func TestShellConversationApprovalLifecycle(t *testing.T) {
	for _, action := range []string{"allow_once", "allow_future", "reject"} {
		t.Run(action, func(t *testing.T) {
			db, grant, states, conversation := operationFixture(t, PermissionAskAsNeeded)
			req := hostRequest(grant, conversation, "unused", OperationShell)
			req.Path, req.Capability, req.ToolName, req.Command = "", "shell", "shell", "echo hello"
			result, err := PrepareOperation(t.Context(), db.DB, states, req)
			if err != nil || result.Status != operationPending {
				t.Fatalf("prepare: %+v %v", result, err)
			}
			if _, err := ClaimLocalOperation(t.Context(), db.DB, states, result.OperationID, req); err == nil {
				t.Fatal("unapproved shell executed")
			}
			approved, err := DecideOperation(t.Context(), db.DB, states, result.OperationID, action, "owner")
			if err != nil || approved.ShellGranted != (action == "allow_future") {
				t.Fatalf("decision: %+v %v", approved, err)
			}
			snapshot, err := ResolveForConversation(t.Context(), db.DB, "owner", conversation)
			if err != nil {
				t.Fatal(err)
			}
			if (len(snapshot.OpaqueToolGrants) == 1) != (action == "allow_future") {
				t.Fatalf("grants: %+v", snapshot)
			}
			params, err := RebuildSubagentParams(t.Context(), db.DB, "owner", conversation, nil)
			if err != nil {
				t.Fatal(err)
			}
			childSnapshot := SnapshotFromParams(params)
			if childSnapshot == nil || (len(childSnapshot.OpaqueToolGrants) == 1) != (action == "allow_future") {
				t.Fatalf("subagent grants lost: %+v", childSnapshot)
			}
			other, err := withToolGrants(t.Context(), db.DB, "owner", "other-conversation", UnboundContext())
			if err != nil || len(other.OpaqueToolGrants) != 0 {
				t.Fatalf("grant leaked: %+v %v", other, err)
			}
			claimed, err := ClaimLocalOperation(t.Context(), db.DB, states, result.OperationID, req)
			if action == "reject" {
				if err == nil {
					t.Fatal("rejected shell executed")
				}
				return
			}
			if err != nil || !claimed.ExecuteAllowed {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			changed := req
			changed.ArgumentsDigest = digestBytes([]byte("different command"))
			if _, err := ClaimLocalOperation(t.Context(), db.DB, states, result.OperationID, changed); err == nil {
				t.Fatal("changed command admitted")
			}
			completed, err := CompleteLocalOperation(t.Context(), states, result.OperationID, LocalOperationCompletion{OperationRequest: req, Status: operationCompleted})
			if err != nil || completed.Status != operationCompleted {
				t.Fatalf("complete: %+v %v", completed, err)
			}
		})
	}
}

func TestGenericToolApprovalUsesFrozenModeAndIdentity(t *testing.T) {
	for _, mode := range []string{PermissionAlwaysAsk, PermissionAskAsNeeded} {
		t.Run(mode, func(t *testing.T) {
			db, grant, states, conversation := operationFixture(t, mode)
			req := hostRequest(grant, conversation, "", OperationTool)
			req.Path, req.Capability, req.ToolName = "", "tool", "remote_echo"
			req.ToolIdentity = "mcp:v1:" + strings.Repeat("a", 64)
			result, err := PrepareOperation(t.Context(), db.DB, states, req)
			if err != nil || result.Status != operationPending {
				t.Fatalf("prepare: %+v %v", result, err)
			}
			other := req
			other.ToolIdentity = "mcp:v1:" + strings.Repeat("b", 64)
			if _, err := PrepareOperation(t.Context(), db.DB, states, other); err == nil {
				t.Fatal("changed tool identity accepted")
			}
			other = req
			other.ArgumentsDigest = digestBytes([]byte("changed"))
			if _, err := ClaimLocalOperation(t.Context(), db.DB, states, result.OperationID, other); err == nil {
				t.Fatal("changed arguments accepted")
			}
			nextMode := PermissionAlwaysAsk
			if mode == PermissionAlwaysAsk {
				nextMode = PermissionAskAsNeeded
			}
			if err := db.Model(&orm.ConversationWorkspaceBinding{}).Where("conversation_id = ?", conversation).
				Updates(map[string]any{"permission_mode": nextMode, "permission_version": 2}).Error; err != nil {
				t.Fatal(err)
			}
			approved, err := DecideOperation(t.Context(), db.DB, states, result.OperationID, "allow_future", "owner")
			if mode == PermissionAlwaysAsk {
				if err == nil {
					t.Fatal("always_ask accepted a future grant")
				}
				approved, err = DecideOperation(t.Context(), db.DB, states, result.OperationID, "allow_once", "owner")
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := ""
			if mode == PermissionAskAsNeeded {
				expected = "tool:" + req.ToolIdentity
			}
			if approved.ToolGranted != expected || approved.ShellGranted {
				t.Fatalf("wrong grant: %+v", approved)
			}
			snapshot, err := ResolveForConversation(t.Context(), db.DB, "owner", conversation)
			if err != nil {
				t.Fatal(err)
			}
			if expected != "" && (len(snapshot.OpaqueToolGrants) != 1 || snapshot.OpaqueToolGrants[0] != expected) {
				t.Fatalf("wrong persisted identity: %+v", snapshot)
			}
			if expected == "" && len(snapshot.OpaqueToolGrants) != 0 {
				t.Fatal("allow_once persisted a grant")
			}
			otherConversation, err := withToolGrants(t.Context(), db.DB, "owner", "other", UnboundContext())
			if err != nil || len(otherConversation.OpaqueToolGrants) != 0 {
				t.Fatal("grant crossed conversation")
			}
			claimed, err := ClaimLocalOperation(t.Context(), db.DB, states, result.OperationID, req)
			if err != nil || !claimed.ExecuteAllowed {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
		})
	}
}

func TestTemporaryToolOnlyAllowsOnce(t *testing.T) {
	db, grant, states, conversation := operationFixture(t, PermissionAskAsNeeded)
	req := hostRequest(grant, conversation, "", OperationTool)
	req.Path, req.Capability, req.ToolIdentity = "", "tool", "temporary:"+strings.Repeat("a", 32)
	result, err := PrepareOperation(t.Context(), db.DB, states, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecideOperation(t.Context(), db.DB, states, result.OperationID, "allow_future", "owner"); err == nil {
		t.Fatal("temporary grant persisted")
	}
	if _, err := DecideOperation(t.Context(), db.DB, states, result.OperationID, "allow_once", "owner"); err != nil {
		t.Fatal(err)
	}
}
