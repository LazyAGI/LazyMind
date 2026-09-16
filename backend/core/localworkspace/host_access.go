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
	"path/filepath"
	"regexp"
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

var stableToolIdentity = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}:v1:[0-9a-f]{64}$`)
var temporaryToolIdentity = regexp.MustCompile(`^temporary:[0-9a-f]{32}$`)

func validToolIdentity(identity string) bool {
	return stableToolIdentity.MatchString(identity) || temporaryToolIdentity.MatchString(identity)
}

func allowsFuture(value operationState) bool {
	return value.PermissionMode == PermissionAskAsNeeded &&
		(value.Request.Capability == "shell" ||
			(value.Request.Capability == "tool" && stableToolIdentity.MatchString(value.Request.ToolIdentity)))
}

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
		if prior, exists := calls[req.CallID]; exists && (prior.ArgumentsDigest != req.ArgumentsDigest || prior.ToolName != req.ToolName || prior.ToolIdentity != req.ToolIdentity) {
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
	var binding orm.ConversationWorkspaceBinding
	if err := db.WithContext(ctx).Where("conversation_id = ?", conversationID).First(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return UnboundContext(), nil
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
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHostAccessBatch*maxOperationRequestBytes))
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

// Host paths belong to Algorithm's OS, which can differ from Core's OS.
func validHostPath(path string) bool {
	if path == "" || strings.ContainsRune(path, 0) {
		return false
	}
	windows := (len(path) > 2 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '\\' || path[2] == '/')) || strings.HasPrefix(path, `\\`)
	if !windows {
		return filepath.IsAbs(path) && filepath.Clean(path) == path
	}
	for _, part := range strings.Split(strings.ReplaceAll(path, `\`, "/"), "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}
