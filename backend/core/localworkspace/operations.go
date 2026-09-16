package localworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"

	"lazymind/core/state"
)

type OperationKind string

const (
	OperationShell  OperationKind = "shell"
	OperationTool   OperationKind = "tool"
	OperationRead   OperationKind = "read"
	OperationWrite  OperationKind = "write"
	OperationDelete OperationKind = "delete"
)

type Decision string

const (
	DecisionAllowed Decision = "allowed"
	DecisionPending Decision = "pending"
	DecisionDenied  Decision = "denied"
)

const (
	operationPending   = "pending"
	operationAllowed   = "allowed"
	operationRejected  = "rejected"
	operationExpired   = "expired"
	operationExecuting = "executing"
	operationCompleted = "completed"
	operationFailed    = "failed"
	operationUncertain = "uncertain"
)

type OperationRequest struct {
	ToolIdentity    string        `json:"tool_identity,omitempty"`
	ToolOrigin      string        `json:"tool_origin,omitempty"`
	Command         string        `json:"command,omitempty"`
	Capability      string        `json:"capability,omitempty"`
	HostIntentID    string        `json:"host_intent_id,omitempty"`
	ExecutionMode   string        `json:"execution_mode,omitempty"`
	ArgumentsDigest string        `json:"arguments_digest,omitempty"`
	UserID          string        `json:"user_id"`
	ConversationID  string        `json:"conversation_id"`
	WorkspaceID     string        `json:"workspace_id"`
	HistoryID       string        `json:"history_id,omitempty"`
	Generation      string        `json:"generation,omitempty"`
	LeaseToken      string        `json:"lease_token,omitempty"`
	RunID           string        `json:"run_id,omitempty"`
	TaskID          string        `json:"task_id,omitempty"`
	AttemptID       string        `json:"attempt_id,omitempty"`
	CallID          string        `json:"call_id"`
	ToolName        string        `json:"tool_name,omitempty"`
	Operation       OperationKind `json:"operation"`
	Path            string        `json:"path"`
}

type OperationResult struct {
	ToolGranted    string   `json:"tool_granted,omitempty"`
	ShellGranted   bool     `json:"shell_granted,omitempty"`
	ExecuteAllowed bool     `json:"execute_allowed,omitempty"`
	Receipt        bool     `json:"receipt,omitempty"`
	OperationID    string   `json:"operation_id,omitempty"`
	Path           string   `json:"path"`
	Decision       Decision `json:"decision,omitempty"`
	Status         string   `json:"status,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	ExpiresAt      int64    `json:"expires_at,omitempty"`
}

type operationState struct {
	DecisionAction    string           `json:"decision_action,omitempty"`
	OperationID       string           `json:"operation_id"`
	Request           OperationRequest `json:"request"`
	Decision          Decision         `json:"decision"`
	Status            string           `json:"status"`
	Result            OperationResult  `json:"result,omitempty"`
	ExpiresAt         int64            `json:"expires_at"`
	Slot              int              `json:"slot"`
	WorkspaceVersion  int64            `json:"workspace_version"`
	PermissionMode    string           `json:"permission_mode"`
	PermissionVersion int64            `json:"permission_version"`
}

const (
	operationStateTTL        = 5 * time.Minute
	operationClaimTTL        = 24 * time.Hour
	maxOperationRequestBytes = 256 << 10
)

func PrepareOperation(ctx context.Context, db *gorm.DB, stateStore state.Store, req OperationRequest) (OperationResult, error) {
	if stateStore == nil || db == nil {
		return OperationResult{}, errors.New("store not initialized")
	}
	if err := validateOperationRequest(req); err != nil {
		return OperationResult{}, err
	}
	runSnapshot, err := validateOperationRun(ctx, db, stateStore, req)
	if err != nil {
		return OperationResult{}, err
	}
	identityFields := []string{req.UserID, req.ConversationID, req.RunID, req.HistoryID, req.TaskID, req.Generation, req.AttemptID, req.LeaseToken, req.CallID}
	identityFields = append(identityFields, hostAccessExecutionMode, req.HostIntentID)
	identity, _ := json.Marshal(identityFields)
	operationID := digestBytes(identity)
	exists, err := stateStore.Exists(ctx, operationKey(operationID))
	if err != nil {
		return OperationResult{}, err
	}
	if exists {
		value, err := loadOperationState(ctx, stateStore, operationID)
		if err != nil {
			return OperationResult{}, err
		}
		if !matchesOperation(value, req) {
			return OperationResult{}, Error("binding_conflict", 409, "conflict")
		}
		if err := validateLiveOperation(ctx, db, stateStore, value); err != nil {
			return OperationResult{}, err
		}
		if value.Status == "preparing" {
			return completePreparingOperation(ctx, stateStore, value)
		}
		return operationResult(value), nil
	}
	// The immutable invocation timestamp prevents reissuing a call after its
	// bounded deduplication record expires, even if the same run is still active.
	issued, _, ok := strings.Cut(req.CallID, "/")
	started, parseErr := strconv.ParseInt(issued, 10, 64)
	age := time.Now().UnixMilli() - started
	if !ok || parseErr != nil || age < -5000 || age >= operationStateTTL.Milliseconds() {
		return OperationResult{}, Error("selection_expired", 409, "conflict")
	}
	snapshot, err := resolveOperation(ctx, db, req, runSnapshot)
	if err != nil {
		return OperationResult{}, err
	}
	value := operationState{OperationID: operationID, Request: req,
		Decision: DecisionPending, Status: "preparing", Slot: -1,
		ExpiresAt: time.Now().Add(operationStateTTL).UnixMilli(), WorkspaceVersion: snapshot.WorkspaceVersion,
		PermissionMode: snapshot.PermissionMode, PermissionVersion: snapshot.PermissionVersion}
	encoded, err := json.Marshal(value)
	if err != nil {
		return OperationResult{}, err
	}
	claimed, err := stateStore.SetNX(ctx, operationKey(operationID), encoded, operationClaimTTL)
	if err != nil {
		return OperationResult{}, err
	}
	if !claimed {
		existing, err := loadOperationState(ctx, stateStore, operationID)
		if err != nil {
			return OperationResult{}, err
		}
		if !matchesOperation(existing, req) {
			return OperationResult{}, Error("binding_conflict", 409, "conflict")
		}
		if existing.Status == "preparing" {
			return completePreparingOperation(ctx, stateStore, existing)
		}
		return operationResult(existing), nil
	}
	return completePreparingOperation(ctx, stateStore, value)
}

// completePreparingOperation makes a claimed preparing operation recoverable.
// Redis and SQLite do not share a cross-key transaction, so a retry by the
// same operation owner resumes the slot/index/state sequence.
func completePreparingOperation(ctx context.Context, store state.Store, value operationState) (OperationResult, error) {
	if value.Slot < 0 {
		var err error
		value.Slot, err = findOperationSlot(ctx, store, value.Request.ConversationID, value.OperationID)
		if err != nil {
			return OperationResult{}, err
		}
	}
	if value.Slot < 0 {
		for slot := 0; slot < 16; slot++ {
			if err := cleanOperationSlot(ctx, store, value.Request.ConversationID, slot); err != nil {
				return OperationResult{}, err
			}
			claimed, err := store.SetNX(ctx, operationSlotKey(value.Request.ConversationID, slot), []byte(value.OperationID), operationClaimTTL)
			if err != nil {
				return OperationResult{}, err
			}
			if claimed {
				value.Slot = slot
				break
			}
			value.Slot, err = findOperationSlot(ctx, store, value.Request.ConversationID, value.OperationID)
			if err != nil {
				return OperationResult{}, err
			}
			if value.Slot >= 0 {
				break
			}
		}
	}
	if value.Slot < 0 {
		value.Status, value.Result.Reason = operationFailed, "approval_capacity"
	} else {
		value.Status = decisionStatus(value.Decision)
		if err := store.HSet(ctx, "local-workspace-operation-index:"+value.Request.ConversationID, map[string]any{fmt.Sprint(value.Slot): value.OperationID}, operationClaimTTL); err != nil {
			return OperationResult{}, err
		}
	}
	if err := saveOperationState(ctx, store, value); err != nil {
		return OperationResult{}, err
	}
	return operationResult(value), nil
}

func findOperationSlot(ctx context.Context, store state.Store, conversation, operationID string) (int, error) {
	for slot := 0; slot < 16; slot++ {
		key := operationSlotKey(conversation, slot)
		exists, err := store.Exists(ctx, key)
		if err != nil {
			return -1, err
		}
		if !exists {
			continue
		}
		id, err := store.Get(ctx, key)
		if err != nil {
			return -1, err
		}
		if string(id) == operationID {
			return slot, nil
		}
	}
	return -1, nil
}

func resolveOperation(ctx context.Context, db *gorm.DB, req OperationRequest, runSnapshot *ContextSnapshot) (*ContextSnapshot, error) {
	live, err := resolveHostAccessWorkspace(ctx, db, req.UserID, req.ConversationID)
	if err != nil {
		return nil, err
	}
	if live == nil || live.WorkspaceID != req.WorkspaceID {
		return nil, Error("workspace_not_found", 404, "resource not found")
	}
	if runSnapshot == nil {
		return nil, Error("selection_forbidden", 403, "forbidden")
	}
	if runSnapshot.WorkspaceID != req.WorkspaceID || live.WorkspaceVersion != runSnapshot.WorkspaceVersion {
		return nil, Error("selection_forbidden", 403, "forbidden")
	}
	resolved := *runSnapshot
	resolved.Root, resolved.DirectoryIdentity = live.Root, live.DirectoryIdentity
	return &resolved, nil
}

func validateOperationRequest(req OperationRequest) error {
	if req.Operation == OperationShell && req.Capability != "shell" {
		return Error("invalid_selection", 400, "invalid request")
	}
	shell := req.ExecutionMode == hostAccessExecutionMode && req.Capability == "shell" && req.Operation == OperationShell && req.ToolName == "shell"
	tool := req.ExecutionMode == hostAccessExecutionMode && req.Capability == "tool" && req.Operation == OperationTool
	if (req.Operation == OperationTool && !tool) || (tool && !validToolIdentity(req.ToolIdentity)) || (!tool && (req.ToolIdentity != "" || req.ToolOrigin != "")) || len(req.ToolOrigin) > 512 {
		return Error("invalid_selection", 400, "invalid request")
	}
	if (req.Capability != "" && !shell && !tool) || len(req.Command) > 4096 || (!shell && req.Command != "") {
		return Error("invalid_selection", 400, "invalid request")
	}
	if !Enabled() {
		return ModeError()
	}
	validPath := validHostPath(req.Path)
	if shell || tool {
		validPath = req.Path == ""
	}
	if req.ExecutionMode != hostAccessExecutionMode || len(req.Path) > 4096 || !validPath ||
		!validDigest(req.ArgumentsDigest) || req.HostIntentID == "" || len(req.HostIntentID) > 256 ||
		strings.TrimSpace(req.ToolName) == "" || len(req.ToolName) > 512 ||
		req.UserID == "" || req.ConversationID == "" || req.CallID == "" || len(req.CallID) > 512 ||
		(!shell && !tool && req.Operation != OperationRead && req.Operation != OperationWrite && req.Operation != OperationDelete) {
		return Error("invalid_selection", 400, "invalid request")
	}
	return nil
}

func digestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func decisionStatus(decision Decision) string {
	if decision == DecisionPending {
		return operationPending
	}
	return operationAllowed
}

func matchesOperation(value operationState, req OperationRequest) bool {
	return value.Request == req
}

func operationKey(operationID string) string { return "local-workspace-operation:" + operationID }
func operationDecisionKey(operationID string) string {
	return "local-workspace-operation-decision:" + operationID
}
func operationLockKey(operationID string) string {
	return "local-workspace-operation-lock:" + operationID
}

func operationSlotKey(conversation string, slot int) string {
	return fmt.Sprintf("local-workspace-operation-slot:%s:%d", conversation, slot)
}

func operationResult(value operationState) OperationResult {
	result := value.Result
	result.ShellGranted = value.DecisionAction == "allow_future" && value.Decision == DecisionAllowed && value.Request.Capability == "shell"
	if value.DecisionAction == "allow_future" && value.Decision == DecisionAllowed && value.Request.Capability == "tool" {
		result.ToolGranted = "tool:" + value.Request.ToolIdentity
	}
	result.Receipt = value.Status == operationCompleted
	result.OperationID, result.Path = value.OperationID, value.Request.Path
	result.Status, result.Decision, result.ExpiresAt = value.Status, value.Decision, value.ExpiresAt
	switch value.Status {
	case "preparing":
		result.Decision = DecisionPending
	case operationFailed, operationUncertain, operationRejected, operationExpired:
		result.Decision = DecisionDenied
	}
	if value.Status == operationUncertain {
		result.Reason = "operation_uncertain"
	}
	if value.Status == operationExpired {
		result.Reason = "selection_expired"
	}
	return result
}

func workspaceErrorReason(err *common.AppError) string {
	detail, _ := err.Detail.(map[string]any)
	reason, _ := detail["reason"].(string)
	return reason
}

func unfinishedOperation(status string) bool {
	return status == "preparing" || status == operationPending || status == operationAllowed || status == operationExecuting
}

func saveOperationState(ctx context.Context, store state.Store, value operationState) error {
	content, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := store.Set(ctx, operationKey(value.OperationID), content, operationClaimTTL); err != nil {
		return err
	}
	if !unfinishedOperation(value.Status) && value.Slot >= 0 {
		if atomic, ok := store.(state.CompareAndDeleteStore); ok {
			_, _ = atomic.CompareAndDelete(ctx, operationSlotKey(value.Request.ConversationID, value.Slot), []byte(value.OperationID))
		}
	}
	return nil
}

func loadOperationState(ctx context.Context, store state.Store, operationID string) (operationState, error) {
	var value operationState
	if store == nil {
		return value, errors.New("store not initialized")
	}
	exists, err := store.Exists(ctx, operationKey(operationID))
	if err != nil {
		return value, err
	}
	if !exists {
		return value, Error("workspace_not_found", 404, "resource not found")
	}
	content, err := store.Get(ctx, operationKey(operationID))
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(content, &value); err != nil {
		return value, err
	}
	if value.Request.ExecutionMode != hostAccessExecutionMode {
		return value, Error("invalid_selection", 400, "invalid request")
	}
	if value.ExpiresAt > 0 && value.ExpiresAt <= time.Now().UnixMilli() && unfinishedOperation(value.Status) {
		if value.Status == operationExecuting || value.Status == "preparing" {
			value.Status = operationUncertain
		} else {
			value.Status = operationExpired
		}
	}
	return value, nil
}

func cleanOperationSlot(ctx context.Context, store state.Store, conversation string, slot int) error {
	key := operationSlotKey(conversation, slot)
	exists, err := store.Exists(ctx, key)
	if err != nil || !exists {
		return err
	}
	id, err := store.Get(ctx, key)
	if err != nil {
		return err
	}
	value, err := loadOperationState(ctx, store, string(id))
	var app *common.AppError
	missing := errors.As(err, &app) && app.HTTPStatus == 404
	if err != nil && !missing {
		return err
	}
	if missing || !unfinishedOperation(value.Status) {
		atomic, ok := store.(state.CompareAndDeleteStore)
		if !ok {
			return common.ResolveAppError("store not initialized", 500)
		}
		_, err = atomic.CompareAndDelete(ctx, key, id)
	}
	return err
}
