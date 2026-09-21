package localworkspace

// The host execution protocol shares the existing approval/run boundary. It
// grants one execution attempt; it never performs file IO on behalf of Python.
import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func ClaimLocalOperation(ctx context.Context, db *gorm.DB, stateStore state.Store, id string, req OperationRequest) (OperationResult, error) {
	if err := validateOperationRequest(req); err != nil {
		return OperationResult{}, err
	}
	if stateStore == nil || db == nil {
		return OperationResult{}, Error("selection_forbidden", 403, "forbidden")
	}
	value, err := loadOperationState(ctx, stateStore, id)
	if err != nil {
		return OperationResult{}, err
	}
	if !matchesOperation(value, req) {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockOperationRun(tx, req); err != nil {
			return err
		}
		for _, lock := range []struct {
			model  any
			where  string
			id     string
			column string
		}{
			{&orm.LocalWorkspace{}, "id = ?", req.WorkspaceID, "version"},
			{&orm.ConversationWorkspaceBinding{}, "conversation_id = ?", req.ConversationID, "permission_version"},
		} {
			if err := tx.Model(lock.model).Where(lock.where, lock.id).UpdateColumn(lock.column, gorm.Expr(lock.column)).Error; err != nil {
				return err
			}
		}
		var err error
		value, err = loadOperationState(ctx, stateStore, id)
		if err != nil {
			return err
		}
		if err := validateLiveOperation(ctx, tx, stateStore, value); err != nil {
			return err
		}
		if value.ExpiresAt <= time.Now().UnixMilli() {
			return Error("selection_expired", 409, "conflict")
		}
		if value.Decision != DecisionAllowed {
			return Error("selection_forbidden", 403, "forbidden")
		}
		if value.Status != operationAllowed {
			return Error("binding_conflict", 409, "conflict")
		}
		claimed, err := stateStore.SetNX(ctx, operationLockKey(id), []byte(req.CallID), operationClaimTTL)
		if err != nil {
			return err
		}
		if !claimed {
			return Error("binding_conflict", 409, "conflict")
		}
		// Retain the claim even if the next write or transaction commit fails.
		// An ambiguous response must never issue a second execution permission.
		value.Status = operationExecuting
		return saveOperationState(ctx, stateStore, value)
	})
	if err != nil {
		return OperationResult{}, err
	}
	result := operationResult(value)
	result.ExecuteAllowed = true
	return result, nil
}

type LocalOperationCompletion struct {
	OperationRequest
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func CompleteLocalOperation(ctx context.Context, stateStore state.Store, id string, request LocalOperationCompletion) (OperationResult, error) {
	if !Enabled() {
		return OperationResult{}, ModeError()
	}
	if stateStore == nil || request.ExecutionMode != hostAccessExecutionMode ||
		(request.Status != operationCompleted && request.Status != operationFailed && request.Status != operationUncertain) {
		return OperationResult{}, Error("invalid_selection", 400, "invalid request")
	}
	if request.Reason != "" && request.Reason != "binding_conflict" && request.Reason != "path_invalid" &&
		request.Reason != "unsupported_file" && request.Reason != "operation_uncertain" && request.Reason != "execution_inactive" {
		return OperationResult{}, Error("invalid_selection", 400, "invalid request")
	}
	value, err := loadOperationState(ctx, stateStore, id)
	if err != nil {
		return OperationResult{}, err
	}
	if !matchesOperation(value, request.OperationRequest) {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}

	same := value.Status == request.Status && value.Result.Reason == request.Reason
	if same {
		return operationResult(value), nil
	}
	if value.Status != operationExecuting {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	// A completion may arrive after revocation: it only records the outcome of
	// the already-claimed attempt, and cannot authorize more filesystem access.
	encoded, err := json.Marshal([]string{request.Status, request.Reason})
	if err != nil {
		return OperationResult{}, err
	}
	key := "local-workspace-operation-completion:" + id
	claimed, err := stateStore.SetNX(ctx, key, encoded, operationClaimTTL)
	if err != nil {
		return OperationResult{}, err
	}
	if !claimed {
		previous, err := stateStore.Get(ctx, key)
		if err != nil || string(previous) != string(encoded) {
			return OperationResult{}, Error("binding_conflict", 409, "conflict")
		}
	}
	value.Status = request.Status
	value.Result = OperationResult{Reason: request.Reason}
	if err := saveOperationState(ctx, stateStore, value); err != nil {
		return OperationResult{}, err
	}
	return operationResult(value), nil
}

func InternalClaimLocalOperation(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) || !requireOperationServiceToken(w, r) {
		return
	}
	var request OperationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOperationRequestBytes)).Decode(&request); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", 400, "invalid request"))
		return
	}
	request.UserID, request.ConversationID = store.UserID(r), mux.Vars(r)["conversation_id"]
	result, err := ClaimLocalOperation(r.Context(), store.DB(), store.State(), mux.Vars(r)["operation_id"], request)
	if !replyError(w, err) {
		common.ReplyOK(w, result)
	}
}

func InternalCompleteLocalOperation(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) || !requireOperationServiceToken(w, r) {
		return
	}
	var request LocalOperationCompletion
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOperationRequestBytes)).Decode(&request); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", 400, "invalid request"))
		return
	}
	request.UserID, request.ConversationID = store.UserID(r), mux.Vars(r)["conversation_id"]
	result, err := CompleteLocalOperation(r.Context(), store.State(), mux.Vars(r)["operation_id"], request)
	if !replyError(w, err) {
		common.ReplyOK(w, result)
	}
}
