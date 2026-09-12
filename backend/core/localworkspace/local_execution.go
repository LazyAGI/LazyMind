package localworkspace

// The local execution protocol shares the existing approval/run boundary. It
// grants one execution attempt; it never performs file IO on behalf of Python.
import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

const localExecutionMode = "local"

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

// Compare identities on the Core host to reject mismatched filesystem views.
// The executor additionally pins directory handles and checks the opened file
// identity: this metadata check is not a replacement for safe local IO.
func validateLocalTarget(req OperationRequest, identity string) error {
	parent := filepath.Dir(req.Path)
	canonical, err := filepath.EvalSymlinks(parent)
	if err != nil || canonical != parent {
		return Error("path_invalid", 400, "invalid request")
	}
	actualParent, err := currentDirectoryIdentity(parent)
	if err != nil || actualParent != req.ParentIdentity {
		return Error("path_unavailable", 409, "conflict")
	}
	info, err := os.Lstat(req.Path)
	if errors.Is(err, os.ErrNotExist) {
		if identity != "missing" || (req.DependsOn == "" && req.Operation != OperationCreate && req.Operation != OperationMkdir) {
			return Error("binding_conflict", 409, "conflict")
		}
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
		return Error("path_invalid", 400, "invalid request")
	}
	actual, err := platformDirectoryIdentity(req.Path, info)
	if err != nil || actual != identity {
		return Error("binding_conflict", 409, "conflict")
	}
	if req.DependsOn == "" {
		return validateOperationTarget(req, info)
	}
	return nil
}

func localDependency(ctx context.Context, stateStore state.Store, req OperationRequest) (string, string, error) {
	if req.DependsOn == "" {
		return req.ExpectedVersion, req.TargetIdentity, nil
	}
	previous, err := loadOperationState(ctx, stateStore, req.DependsOn)
	if err != nil {
		return "", "", err
	}
	a, b := previous.Request, req
	if a.ExecutionMode != localExecutionMode || a.UserID != b.UserID || a.ConversationID != b.ConversationID ||
		a.WorkspaceID != b.WorkspaceID || a.RunID != b.RunID || a.HistoryID != b.HistoryID ||
		a.TaskID != b.TaskID || a.Generation != b.Generation || a.AttemptID != b.AttemptID || a.LeaseToken != b.LeaseToken ||
		a.Path != b.Path || previous.Status != operationCompleted {
		return "", "", Error("binding_conflict", 409, "conflict")
	}
	version := req.ExpectedVersion
	if version == "" {
		version = previous.Version
	}
	return version, previous.Result.TargetIdentity, nil
}

func ClaimLocalOperation(ctx context.Context, db *gorm.DB, stateStore state.Store, id string, req OperationRequest) (OperationResult, error) {
	if err := validateOperationRequest(req); err != nil {
		return OperationResult{}, err
	}
	if req.ExecutionMode != localExecutionMode || stateStore == nil || db == nil {
		return OperationResult{}, Error("selection_forbidden", 403, "forbidden")
	}
	value, err := loadOperationState(ctx, stateStore, id)
	if err != nil {
		return OperationResult{}, err
	}
	if !matchesOperation(value, req) {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	var version, identity string
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
		version, identity, err = localDependency(ctx, stateStore, req)
		if err != nil {
			return err
		}
		if err := validateLocalTarget(req, identity); err != nil {
			return err
		}
		if !readOperation(req.Operation) && req.Operation != OperationCreate && req.Operation != OperationMkdir && !validDigest(version) {
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
	result.ExecuteAllowed, result.Version, result.TargetIdentity = true, version, identity
	return result, nil
}

type LocalOperationCompletion struct {
	OperationRequest
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	Version        string `json:"version,omitempty"`
	ResultIdentity string `json:"result_identity,omitempty"`
}

func CompleteLocalOperation(ctx context.Context, stateStore state.Store, id string, request LocalOperationCompletion) (OperationResult, error) {
	if !Enabled() {
		return OperationResult{}, ModeError()
	}
	if stateStore == nil || request.ExecutionMode != localExecutionMode ||
		(request.Status != operationCompleted && request.Status != operationFailed && request.Status != operationUncertain) ||
		(request.Version != "" && !validDigest(request.Version)) || len(request.ResultIdentity) > 160 {
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
	if request.Status == operationCompleted && (request.ResultIdentity == "" ||
		(!readOperation(request.Operation) && request.Operation != OperationMkdir && request.Operation != OperationDelete && request.Version == "")) {
		return OperationResult{}, Error("invalid_selection", 400, "invalid request")
	}
	same := value.Status == request.Status && value.Version == request.Version && value.Result.Reason == request.Reason && value.Result.TargetIdentity == request.ResultIdentity
	if same {
		return operationResult(value), nil
	}
	if value.Status != operationExecuting {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	// A completion may arrive after revocation: it only records the outcome of
	// the already-claimed attempt, and cannot authorize more filesystem access.
	encoded, err := json.Marshal([]string{request.Status, request.Reason, request.Version, request.ResultIdentity})
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
	value.Status, value.Version = request.Status, request.Version
	value.Result = OperationResult{Reason: request.Reason, TargetIdentity: request.ResultIdentity}
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*maxOperationBytes+8192)).Decode(&request); err != nil {
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*maxOperationBytes+8192)).Decode(&request); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", 400, "invalid request"))
		return
	}
	request.UserID, request.ConversationID = store.UserID(r), mux.Vars(r)["conversation_id"]
	result, err := CompleteLocalOperation(r.Context(), store.State(), mux.Vars(r)["operation_id"], request)
	if !replyError(w, err) {
		common.ReplyOK(w, result)
	}
}
