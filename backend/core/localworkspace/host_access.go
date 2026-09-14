package localworkspace

// Host access admits declared filesystem effects of heterogeneous tools. The
// algorithm service resolves and enforces canonical paths on its own host;
// Core checks run ownership, persisted grants and approval state, never files.
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

const hostAccessExecutionMode = "host_access"
const maxHostAccessBatch = 16

type OperationBatchRequest struct {
	Calls []OperationRequest `json:"calls"`
}

type OperationBatchResult struct {
	Operations []OperationResult `json:"operations"`
}

// PrepareOperationBatch validates the entire envelope before admitting any
// operations. Admission is recoverable by the immutable call/intent IDs. Store
// failures may leave prepared approvals, but never grant execution: callers
// must receive all decisions and separately claim every intent before IO.
func PrepareOperationBatch(ctx context.Context, db *gorm.DB, stateStore state.Store, request OperationBatchRequest) (OperationBatchResult, error) {
	if db == nil || stateStore == nil || len(request.Calls) == 0 || len(request.Calls) > maxHostAccessBatch {
		return OperationBatchResult{}, Error("invalid_selection", 400, "invalid request")
	}
	first := request.Calls[0]
	seen := map[string]bool{}
	calls := map[string]OperationRequest{}
	for _, req := range request.Calls {
		if req.ExecutionMode != hostAccessExecutionMode && req.ExecutionMode != localExecutionMode {
			return OperationBatchResult{}, Error("invalid_selection", 400, "invalid request")
		}
		if err := validateOperationRequest(req); err != nil {
			return OperationBatchResult{}, err
		}
		issued, _, ok := strings.Cut(req.CallID, "/")
		started, parseErr := strconv.ParseInt(issued, 10, 64)
		age := time.Now().UnixMilli() - started
		if !ok || parseErr != nil || age < -5000 || age >= operationStateTTL.Milliseconds() {
			return OperationBatchResult{}, Error("selection_expired", 409, "conflict")
		}
		if req.UserID != first.UserID || req.ConversationID != first.ConversationID || req.WorkspaceID != first.WorkspaceID ||
			req.HistoryID != first.HistoryID || req.RunID != first.RunID || req.TaskID != first.TaskID || req.Generation != first.Generation ||
			req.AttemptID != first.AttemptID || req.LeaseToken != first.LeaseToken {
			return OperationBatchResult{}, Error("binding_conflict", 409, "conflict")
		}
		keyBytes, _ := json.Marshal([]string{req.CallID, req.HostIntentID})
		key := string(keyBytes)
		if seen[key] {
			return OperationBatchResult{}, Error("invalid_selection", 400, "invalid request")
		}
		seen[key] = true
		if prior, exists := calls[req.CallID]; exists && (prior.ArgumentsDigest != req.ArgumentsDigest || prior.ToolName != req.ToolName) {
			return OperationBatchResult{}, Error("binding_conflict", 409, "conflict")
		}
		calls[req.CallID] = req
	}
	// Authenticate all calls before persisting any approval, including batches
	// assembled with inconsistent or unauthorized run identities.
	for _, req := range request.Calls {
		run, err := validateOperationRun(ctx, db, stateStore, req)
		if err != nil {
			return OperationBatchResult{}, err
		}
		if _, err := resolveOperation(ctx, db, req, run); err != nil {
			return OperationBatchResult{}, err
		}
	}
	result := OperationBatchResult{Operations: make([]OperationResult, 0, len(request.Calls))}
	for _, req := range request.Calls {
		operation, err := PrepareOperation(ctx, db, stateStore, req)
		if err != nil {
			return OperationBatchResult{}, err
		}
		result.Operations = append(result.Operations, operation)
	}
	return result, nil
}

// This resolver deliberately does not call ResolveActiveForBinding: checking a
// directory on Core would authorize the wrong filesystem when hosts differ.
func resolveHostAccessWorkspace(ctx context.Context, db *gorm.DB, userID, conversationID string) (*ContextSnapshot, error) {
	var conversation orm.Conversation
	if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ?", conversationID, userID).First(&conversation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, Error("workspace_not_found", 404, "resource not found")
		}
		return nil, err
	}
	if !conversation.IsTaskConv {
		return nil, ModeError()
	}
	var binding orm.ConversationWorkspaceBinding
	if err := db.WithContext(ctx).Where("conversation_id = ?", conversationID).First(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, Error("workspace_not_found", 404, "resource not found")
		}
		return nil, err
	}
	var workspace orm.LocalWorkspace
	if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ?", binding.WorkspaceID, userID).First(&workspace).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, Error("workspace_not_found", 404, "resource not found")
		}
		return nil, err
	}
	if workspace.Status == StatusRevoked {
		return nil, Error("revoked", 409, "conflict")
	}
	if workspace.Status != StatusActive {
		return nil, Error("path_unavailable", 409, "conflict")
	}
	return snapshot(workspace, binding.PermissionMode, binding.PermissionVersion), nil
}

func InternalPrepareOperationBatch(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) || !requireOperationServiceToken(w, r) {
		return
	}
	var request OperationBatchRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*maxOperationBytes+256*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", 400, "invalid request"))
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		common.ReplyAppErr(w, Error("invalid_selection", 400, "invalid request"))
		return
	}
	for i := range request.Calls {
		request.Calls[i].UserID = store.UserID(r)
		request.Calls[i].ConversationID = mux.Vars(r)["conversation_id"]
	}
	result, err := PrepareOperationBatch(r.Context(), store.DB(), store.State(), request)
	if !replyError(w, err) {
		common.ReplyOK(w, result)
	}
}
