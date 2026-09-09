package localworkspace

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

func DecideOperation(ctx context.Context, db *gorm.DB, stateStore state.Store, operationID, action, userID string) (OperationResult, error) {
	if stateStore == nil {
		return OperationResult{}, common.ResolveAppError("store not initialized", 500)
	}
	value, err := loadOperationState(ctx, stateStore, operationID)
	if err != nil {
		return OperationResult{}, err
	}
	if value.Request.UserID != userID {
		return OperationResult{}, Error("workspace_not_found", 404, "resource not found")
	}
	if err := validateLiveOperation(ctx, db, stateStore, value); err != nil {
		return OperationResult{}, err
	}
	if value.Status != operationPending {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	var decision Decision
	var nextStatus string
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "allow_once":
		decision, nextStatus = DecisionAllowed, operationAllowed
	case "reject":
		decision, nextStatus = DecisionDenied, operationRejected
	default:
		return OperationResult{}, Error("invalid_selection", 400, "invalid request")
	}
	decisionKey := operationDecisionKey(operationID)
	claimed, err := stateStore.SetNX(ctx, decisionKey, []byte(userID), operationClaimTTL)
	if err != nil {
		return OperationResult{}, err
	}
	if !claimed {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	value, err = loadOperationState(ctx, stateStore, operationID)
	if err != nil {
		return OperationResult{}, err
	}
	if value.Request.UserID != userID || value.Status != operationPending {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	if err := validateLiveOperation(ctx, db, stateStore, value); err != nil {
		return OperationResult{}, err
	}
	value.Decision, value.Status = decision, nextStatus
	if err := saveOperationState(ctx, stateStore, value); err != nil {
		return OperationResult{}, err
	}
	return operationResult(value), nil
}

// InternalPrepareOperation prepares one operation for the authenticated algorithm host.
func InternalPrepareOperation(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) || !requireOperationServiceToken(w, r) {
		return
	}
	var request OperationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*maxOperationBytes+4096)).Decode(&request); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", http.StatusBadRequest, "invalid request"))
		return
	}
	request.UserID = store.UserID(r)
	request.ConversationID = mux.Vars(r)["conversation_id"]
	result, err := PrepareOperation(r.Context(), store.DB(), store.State(), request)
	if replyError(w, err) {
		return
	}
	common.ReplyOK(w, result)
}

// InternalOperationStatus returns the current decision for an operation.
func InternalOperationStatus(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) || !requireOperationServiceToken(w, r) {
		return
	}
	operationID := mux.Vars(r)["operation_id"]
	value, err := loadOperationState(r.Context(), store.State(), operationID)
	if err == nil && (value.Request.ConversationID != mux.Vars(r)["conversation_id"] || value.Request.UserID != store.UserID(r)) {
		err = Error("workspace_not_found", 404, "resource not found")
	}
	result := OperationResult{}
	if err == nil {
		q, request := r.URL.Query(), value.Request
		if q.Get("run_id") != request.RunID || q.Get("history_id") != request.HistoryID || q.Get("task_id") != request.TaskID || q.Get("generation") != request.Generation || q.Get("attempt_id") != request.AttemptID {
			err = Error("binding_conflict", 409, "conflict")
		} else {
			err = validateLiveOperation(r.Context(), store.DB(), store.State(), value)
		}
		if err == nil {
			result, err = projectOperation(r.Context(), store.State(), value)
		}
	}
	if replyError(w, err) {
		return
	}
	common.ReplyOK(w, result)
}

// InternalExecuteOperation executes only the exact operation previously prepared.
func InternalExecuteOperation(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) || !requireOperationServiceToken(w, r) {
		return
	}
	var request OperationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*maxOperationBytes+4096)).Decode(&request); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", http.StatusBadRequest, "invalid request"))
		return
	}
	request.UserID = store.UserID(r)
	request.ConversationID = mux.Vars(r)["conversation_id"]
	result, err := ExecuteOperation(r.Context(), store.DB(), store.State(), mux.Vars(r)["operation_id"], request)
	if replyError(w, err) {
		return
	}
	common.ReplyOK(w, result)
}

// DecideOperationHandler records one user decision for a pending operation.
func DecideOperationHandler(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) {
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", http.StatusBadRequest, "invalid request"))
		return
	}
	operationID := mux.Vars(r)["operation_id"]
	value, err := loadOperationState(r.Context(), store.State(), operationID)
	if err == nil && (value.Request.ConversationID != mux.Vars(r)["conversation_id"] || value.Request.UserID != store.UserID(r)) {
		err = Error("workspace_not_found", 404, "resource not found")
	}
	result := OperationResult{}
	if err == nil {
		result, err = DecideOperation(r.Context(), store.DB(), store.State(), operationID, body.Action, store.UserID(r))
	}
	if replyError(w, err) {
		return
	}
	common.ReplyOK(w, result)
}

func requireOperationServiceToken(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	provided := strings.TrimSpace(r.Header.Get("X-LazyMind-Internal-Token"))
	if expected == "" || len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		common.ReplyAppErr(w, Error("mode_forbidden", http.StatusUnauthorized, "forbidden"))
		return false
	}
	return true
}

func validateLiveOperation(ctx context.Context, db *gorm.DB, stateStore state.Store, value operationState) error {
	if err := validateOperationRun(ctx, db, stateStore, value.Request); err != nil {
		return err
	}
	snapshot, err := resolveOperation(ctx, db, value.Request)
	if err != nil {
		return err
	}
	if snapshot.WorkspaceVersion != value.WorkspaceVersion || snapshot.PermissionVersion != value.PermissionVersion {
		return Error("selection_forbidden", 403, "forbidden")
	}
	return nil
}

// ListOperationApprovals exposes only bounded summaries, never content or execution credentials.
func ListOperationApprovals(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) {
		return
	}
	ctx, stateStore := r.Context(), store.State()
	conversationID, userID := mux.Vars(r)["conversation_id"], store.UserID(r)
	if stateStore == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return
	}
	var count int64
	err := store.DB().WithContext(ctx).Model(&orm.Conversation{}).Where("id = ? AND create_user_id = ?", conversationID, userID).Count(&count).Error
	if err == nil && count != 1 {
		err = Error("workspace_not_found", 404, "resource not found")
	}
	if replyError(w, err) {
		return
	}
	index, err := stateStore.HGetAll(ctx, "local-workspace-operation-index:"+conversationID)
	if replyError(w, err) {
		return
	}
	items := []map[string]any{}
	for slot := 0; slot < 16; slot++ {
		id := index[strconv.Itoa(slot)]
		if id == "" {
			continue
		}
		value, err := loadOperationState(ctx, stateStore, id)
		if err != nil {
			var app *common.AppError
			if errors.As(err, &app) && app.HTTPStatus == 404 {
				continue
			}
			replyError(w, err)
			return
		}
		if value.Request.UserID != userID || value.Request.ConversationID != conversationID {
			continue
		}
		projected, err := projectOperation(ctx, stateStore, value)
		if replyError(w, err) {
			return
		}
		value.Status = projected.Status
		reason := projected.Reason
		if unfinishedOperation(value.Status) {
			if err := validateLiveOperation(ctx, store.DB(), stateStore, value); err != nil {
				value.Status, reason = operationExpired, "execution_inactive"
			}
		}
		if value.ExpiresAt <= time.Now().UnixMilli() && unfinishedOperation(value.Status) {
			value.Status = operationExpired
		}
		items = append(items, map[string]any{"operation_id": id, "path": value.Request.Path, "operation": value.Request.Operation,
			"tool_name": value.Request.ToolName, "task_id": value.Request.TaskID, "attempt_id": value.Request.AttemptID,
			"version": value.Version, "content_digest": value.ContentDigest, "status": value.Status, "expires_at": value.ExpiresAt, "reason": reason})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["expires_at"].(int64) < items[j]["expires_at"].(int64) })
	common.ReplyOK(w, map[string]any{"items": items})
}

// A retained claim can outlive a failed state write. Never show that operation
// as still actionable; the original claimant alone may finish it before expiry.
func projectOperation(ctx context.Context, store state.Store, value operationState) (OperationResult, error) {
	key := ""
	if value.Status == operationPending {
		key = operationDecisionKey(value.OperationID)
	}
	if value.Status == operationAllowed {
		key = operationLockKey(value.OperationID)
	}
	if key != "" {
		consumed, err := store.Exists(ctx, key)
		if err != nil {
			return OperationResult{}, err
		}
		if consumed {
			if value.Status == operationPending {
				value.Status = "preparing"
			} else {
				value.Status = operationExecuting
			}
		}
	}
	return operationResult(value), nil
}
