package localworkspace

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"lazymind/core/common"
	"lazymind/core/state"
	"lazymind/core/store"
)

func DecideOperation(ctx context.Context, stateStore state.Store, operationID, action, userID string) (OperationResult, error) {
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
	value.Decision, value.Status = decision, nextStatus
	if err := saveOperationState(ctx, stateStore, value); err != nil {
		return OperationResult{}, err
	}
	return OperationResult{OperationID: operationID, Path: value.Request.Path, Version: value.Version, Decision: value.Decision, Status: value.Status}, nil
}

func OperationStatus(ctx context.Context, stateStore state.Store, operationID string) (OperationResult, error) {
	if stateStore == nil {
		return OperationResult{}, common.ResolveAppError("store not initialized", 500)
	}
	value, err := loadOperationState(ctx, stateStore, operationID)
	if err != nil {
		return OperationResult{}, err
	}
	return OperationResult{OperationID: operationID, Path: value.Request.Path, Content: value.Result.Content, Version: value.Version, Decision: value.Decision, Status: value.Status}, nil
}

// InternalPrepareOperation prepares one operation for the authenticated algorithm host.
func InternalPrepareOperation(w http.ResponseWriter, r *http.Request) {
	if !requireOperationServiceToken(w, r) {
		return
	}
	var request OperationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOperationBytes+4096)).Decode(&request); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", http.StatusBadRequest, "invalid request"))
		return
	}
	request.UserID = store.UserID(r)
	request.ConversationID = mux.Vars(r)["conversation_id"]
	result, err := PrepareOperation(r.Context(), store.DB(), store.State(), request)
	if replyOperationError(w, err) {
		return
	}
	common.ReplyOK(w, result)
}

// InternalOperationStatus returns the current decision for an operation.
func InternalOperationStatus(w http.ResponseWriter, r *http.Request) {
	if !requireOperationServiceToken(w, r) {
		return
	}
	operationID := mux.Vars(r)["operation_id"]
	value, err := loadOperationState(r.Context(), store.State(), operationID)
	if err == nil && (value.Request.ConversationID != mux.Vars(r)["conversation_id"] || value.Request.UserID != store.UserID(r)) {
		err = Error("workspace_not_found", 404, "resource not found")
	}
	result := OperationResult{}
	if err == nil {
		result = OperationResult{OperationID: operationID, Path: value.Request.Path, Content: value.Result.Content, Version: value.Version, Decision: value.Decision, Status: value.Status}
	}
	if replyOperationError(w, err) {
		return
	}
	common.ReplyOK(w, result)
}

// InternalExecuteOperation executes only the exact operation previously prepared.
func InternalExecuteOperation(w http.ResponseWriter, r *http.Request) {
	if !requireOperationServiceToken(w, r) {
		return
	}
	var request OperationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOperationBytes+4096)).Decode(&request); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", http.StatusBadRequest, "invalid request"))
		return
	}
	request.UserID = store.UserID(r)
	request.ConversationID = mux.Vars(r)["conversation_id"]
	result, err := ExecuteOperation(r.Context(), store.DB(), store.State(), mux.Vars(r)["operation_id"], request)
	if replyOperationError(w, err) {
		return
	}
	common.ReplyOK(w, result)
}

// DecideOperationHandler records one user decision for a pending operation.
func DecideOperationHandler(w http.ResponseWriter, r *http.Request) {
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
		result, err = DecideOperation(r.Context(), store.State(), operationID, body.Action, store.UserID(r))
	}
	if replyOperationError(w, err) {
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

func replyOperationError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		common.ReplyAppErr(w, appErr)
	} else {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
	}
	return true
}
