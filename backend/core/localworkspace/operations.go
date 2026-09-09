package localworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"

	"lazymind/core/state"
)

type OperationKind string

const (
	OperationRead    OperationKind = "read"
	OperationCreate  OperationKind = "create"
	OperationAppend  OperationKind = "append"
	OperationReplace OperationKind = "replace"
	OperationDelete  OperationKind = "delete"
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
	UserID          string        `json:"user_id"`
	ConversationID  string        `json:"conversation_id"`
	WorkspaceID     string        `json:"workspace_id"`
	RunID           string        `json:"run_id,omitempty"`
	TaskID          string        `json:"task_id,omitempty"`
	AttemptID       string        `json:"attempt_id,omitempty"`
	CallID          string        `json:"call_id"`
	ToolName        string        `json:"tool_name,omitempty"`
	Operation       OperationKind `json:"operation"`
	Path            string        `json:"path"`
	Content         string        `json:"content,omitempty"`
	OldContent      string        `json:"old_content,omitempty"`
	ExpectedVersion string        `json:"expected_version,omitempty"`
}

type OperationResult struct {
	OperationID string   `json:"operation_id,omitempty"`
	Path        string   `json:"path"`
	Content     string   `json:"content,omitempty"`
	Version     string   `json:"version,omitempty"`
	Decision    Decision `json:"decision,omitempty"`
	Status      string   `json:"status,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

type operationState struct {
	OperationID       string           `json:"operation_id"`
	Request           OperationRequest `json:"request"`
	ContentDigest     string           `json:"content_digest,omitempty"`
	OldContentDigest  string           `json:"old_content_digest,omitempty"`
	Decision          Decision         `json:"decision"`
	Status            string           `json:"status"`
	Version           string           `json:"version,omitempty"`
	Result            OperationResult  `json:"result,omitempty"`
	ExpiresAt         int64            `json:"expires_at"`
	PermissionMode    string           `json:"permission_mode"`
	PermissionVersion int64            `json:"permission_version"`
}

const (
	operationStateTTL = 5 * time.Minute
	operationClaimTTL = 24 * time.Hour
	maxOperationBytes = 20 << 20
)

func PrepareOperation(ctx context.Context, db *gorm.DB, stateStore state.Store, req OperationRequest) (OperationResult, error) {
	if stateStore == nil {
		return OperationResult{}, common.ResolveAppError("store not initialized", 500)
	}
	if err := validateOperationRequest(req); err != nil {
		return OperationResult{}, err
	}
	snapshot, path, version, exists, err := resolveOperation(ctx, db, req)
	if err != nil {
		return OperationResult{}, err
	}
	if req.Operation == OperationCreate && exists {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	if req.Operation != OperationCreate && !exists {
		return OperationResult{}, Error("workspace_not_found", 404, "resource not found")
	}
	if req.Operation != OperationRead && req.Operation != OperationCreate && req.ExpectedVersion != version {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	if isSensitivePath(path) && req.Operation != OperationRead {
		return OperationResult{}, Error("selection_forbidden", 403, "forbidden")
	}
	decision := permissionDecision(snapshot.PermissionMode, req.Operation, path)
	operationID, err := newID()
	if err != nil {
		return OperationResult{}, err
	}
	value := operationState{
		OperationID:       operationID,
		Request:           requestWithoutContent(req),
		ContentDigest:     digestString(req.Content),
		OldContentDigest:  digestString(req.OldContent),
		Decision:          decision,
		Status:            decisionStatus(decision),
		Version:           version,
		ExpiresAt:         time.Now().Add(operationStateTTL).UnixMilli(),
		PermissionMode:    snapshot.PermissionMode,
		PermissionVersion: snapshot.PermissionVersion,
	}
	if err := saveOperationState(ctx, stateStore, value); err != nil {
		return OperationResult{}, err
	}
	return OperationResult{
		OperationID: operationID,
		Path:        relativePath(snapshot.Root, path),
		Version:     version,
		Decision:    decision,
		Status:      value.Status,
	}, nil
}

func ExecuteOperation(ctx context.Context, db *gorm.DB, stateStore state.Store, operationID string, req OperationRequest) (OperationResult, error) {
	if stateStore == nil {
		return OperationResult{}, common.ResolveAppError("store not initialized", 500)
	}
	// Invalid requests must not consume another call's authorization.
	value, err := loadOperationState(ctx, stateStore, operationID)
	if err != nil {
		return OperationResult{}, err
	}
	if !sameOperationCall(value.Request, req) || value.ContentDigest != digestString(req.Content) || value.OldContentDigest != digestString(req.OldContent) {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	if value.Status == operationCompleted {
		return value.Result, nil
	}
	if value.Decision != DecisionAllowed {
		return OperationResult{}, Error("selection_forbidden", 403, "forbidden")
	}
	if value.Status != operationAllowed {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}

	// This is a one-time claim, retained beyond the operation's validity even on failure.
	claimed, err := stateStore.SetNX(ctx, operationLockKey(operationID), []byte(req.CallID), operationClaimTTL)
	if err != nil {
		return OperationResult{}, err
	}
	value, err = loadOperationState(ctx, stateStore, operationID)
	if err != nil {
		return OperationResult{}, err
	}
	if value.Status == operationCompleted {
		return value.Result, nil
	}
	if !claimed || value.Status != operationAllowed || value.Decision != DecisionAllowed {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}

	value.Status = operationExecuting
	if err := saveOperationState(ctx, stateStore, value); err != nil {
		return OperationResult{}, err
	}
	snapshot, path, version, exists, err := resolveOperation(ctx, db, req)
	if err != nil {
		return failOperation(ctx, stateStore, &value, err)
	}
	if req.Operation == OperationCreate && exists {
		return failOperation(ctx, stateStore, &value, Error("binding_conflict", 409, "conflict"))
	}
	if req.Operation != OperationCreate && !exists {
		return failOperation(ctx, stateStore, &value, Error("workspace_not_found", 404, "resource not found"))
	}
	if req.Operation != OperationRead && req.Operation != OperationCreate && req.ExpectedVersion != version {
		return failOperation(ctx, stateStore, &value, Error("binding_conflict", 409, "conflict"))
	}
	if snapshot.PermissionVersion != value.PermissionVersion || snapshot.PermissionMode != value.PermissionMode {
		return failOperation(ctx, stateStore, &value, Error("selection_forbidden", 403, "forbidden"))
	}
	result, err := executeFileOperation(path, req, version)
	if err != nil {
		return failOperation(ctx, stateStore, &value, err)
	}
	result.OperationID = operationID
	result.Path = relativePath(snapshot.Root, path)
	result.Status = operationCompleted
	value.Status = operationCompleted
	value.Result = result
	value.Result.Content = ""
	if err := saveOperationState(ctx, stateStore, value); err != nil {
		return OperationResult{}, err
	}
	return result, nil
}

func failOperation(ctx context.Context, store state.Store, value *operationState, err error) (OperationResult, error) {
	value.Status = operationFailed
	_ = saveOperationState(ctx, store, *value)
	return OperationResult{}, err
}

func executeFileOperation(path string, req OperationRequest, version string) (OperationResult, error) {
	if req.Operation == OperationRead {
		content, err := os.ReadFile(path)
		if err != nil {
			return OperationResult{}, err
		}
		return OperationResult{Content: string(content), Version: version}, nil
	}
	if req.Operation == OperationDelete {
		if err := os.Remove(path); err != nil {
			return OperationResult{}, err
		}
		return OperationResult{Version: version}, nil
	}
	if len(req.Content) > maxOperationBytes {
		return OperationResult{}, Error("invalid_selection", 400, "invalid request")
	}

	var content []byte
	switch req.Operation {
	case OperationCreate:
		content = []byte(req.Content)
	case OperationReplace:
		old, err := os.ReadFile(path)
		if err != nil {
			return OperationResult{}, err
		}
		if req.OldContent == "" {
			content = []byte(req.Content)
		} else {
			if strings.Count(string(old), req.OldContent) != 1 {
				return OperationResult{}, Error("binding_conflict", 409, "conflict")
			}
			content = []byte(strings.Replace(string(old), req.OldContent, req.Content, 1))
		}
	case OperationAppend:
		old, err := os.ReadFile(path)
		if err != nil {
			return OperationResult{}, err
		}
		if len(old)+len(req.Content) > maxOperationBytes {
			return OperationResult{}, Error("invalid_selection", 400, "invalid request")
		}
		content = append(append([]byte(nil), old...), []byte(req.Content)...)
	default:
		return OperationResult{}, Error("invalid_selection", 400, "invalid request")
	}
	if err := atomicWrite(path, content); err != nil {
		return OperationResult{}, err
	}
	return OperationResult{Content: string(content), Version: digestBytes(content)}, nil
}

func atomicWrite(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lazymind-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func resolveOperation(ctx context.Context, db *gorm.DB, req OperationRequest) (*ContextSnapshot, string, string, bool, error) {
	snapshot, err := ResolveForConversation(ctx, db, req.UserID, req.ConversationID)
	if err != nil {
		return nil, "", "", false, err
	}
	if snapshot == nil || snapshot.WorkspaceID != req.WorkspaceID {
		return nil, "", "", false, Error("workspace_not_found", 404, "resource not found")
	}
	path, err := resolveOperationPath(snapshot.Root, req.Path, req.Operation == OperationCreate)
	if err != nil {
		return nil, "", "", false, err
	}
	version, exists, err := fileVersion(path)
	if err != nil {
		return nil, "", "", false, err
	}
	return snapshot, path, version, exists, nil
}

func validateOperationRequest(req OperationRequest) error {
	if strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.ConversationID) == "" ||
		strings.TrimSpace(req.WorkspaceID) == "" || strings.TrimSpace(req.CallID) == "" ||
		strings.TrimSpace(req.Path) == "" {
		return Error("invalid_selection", 400, "invalid request")
	}
	switch req.Operation {
	case OperationRead, OperationCreate, OperationAppend, OperationReplace, OperationDelete:
		return nil
	default:
		return Error("invalid_selection", 400, "invalid request")
	}
}

func resolveOperationPath(root, raw string, allowMissing bool) (string, error) {
	if filepath.IsAbs(raw) {
		return "", Error("path_invalid", 400, "invalid request")
	}
	clean := filepath.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", Error("path_invalid", 400, "invalid request")
	}
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == ".git" {
			return "", Error("path_invalid", 400, "invalid request")
		}
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(rootReal, clean)
	if info, err := os.Lstat(candidate); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", Error("path_invalid", 400, "invalid request")
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(rootReal, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", Error("path_invalid", 400, "invalid request")
		}
		return candidate, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if !allowMissing {
		return "", Error("workspace_not_found", 404, "resource not found")
	}
	parentReal, err := filepath.EvalSymlinks(filepath.Dir(candidate))
	if err != nil {
		return "", Error("path_invalid", 400, "invalid request")
	}
	rel, err := filepath.Rel(rootReal, parentReal)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", Error("path_invalid", 400, "invalid request")
	}
	return filepath.Join(parentReal, filepath.Base(candidate)), nil
}

func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func fileVersion(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", true, Error("path_invalid", 400, "invalid request")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", true, err
	}
	return digestBytes(content), true, nil
}

func digestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func permissionDecision(mode string, operation OperationKind, path string) Decision {
	if mode == PermissionAllowAll || (operation == OperationRead && !isSensitivePath(path)) {
		return DecisionAllowed
	}
	if mode == PermissionAlwaysAsk || operation == OperationDelete || isSensitivePath(path) {
		return DecisionPending
	}
	return DecisionAllowed
}

func decisionStatus(decision Decision) string {
	if decision == DecisionPending {
		return operationPending
	}
	return operationAllowed
}

func isSensitivePath(path string) bool {
	slash := filepath.ToSlash(path)
	base := strings.ToLower(filepath.Base(path))
	if strings.Contains("/"+slash+"/", "/.ssh/") || strings.Contains("/"+slash+"/", "/.aws/") {
		return true
	}
	if strings.HasPrefix(base, ".env") && base != ".env.example" && base != ".env.sample" && base != ".env.template" {
		return true
	}
	if base == "id_rsa" || base == "id_ed25519" || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".pem") {
		return true
	}
	return strings.Contains(base, "credentials") || strings.HasPrefix(base, "service-account")
}

func sameOperationCall(expected, actual OperationRequest) bool {
	return expected.UserID == actual.UserID && expected.ConversationID == actual.ConversationID &&
		expected.WorkspaceID == actual.WorkspaceID && expected.RunID == actual.RunID &&
		expected.TaskID == actual.TaskID && expected.AttemptID == actual.AttemptID &&
		expected.CallID == actual.CallID && expected.ToolName == actual.ToolName &&
		expected.Operation == actual.Operation && expected.Path == actual.Path &&
		expected.ExpectedVersion == actual.ExpectedVersion
}

func requestWithoutContent(req OperationRequest) OperationRequest {
	req.Content, req.OldContent = "", ""
	return req
}

func digestString(value string) string {
	if value == "" {
		return ""
	}
	return digestBytes([]byte(value))
}

func operationKey(operationID string) string { return "local-workspace-operation:" + operationID }
func operationDecisionKey(operationID string) string {
	return "local-workspace-operation-decision:" + operationID
}
func operationLockKey(operationID string) string {
	return "local-workspace-operation-lock:" + operationID
}

func saveOperationState(ctx context.Context, store state.Store, value operationState) error {
	content, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return store.Set(ctx, operationKey(value.OperationID), content, operationStateTTL)
}

func loadOperationState(ctx context.Context, store state.Store, operationID string) (operationState, error) {
	var value operationState
	content, err := store.Get(ctx, operationKey(operationID))
	if err != nil {
		return value, Error("workspace_not_found", 404, "resource not found")
	}
	if err := json.Unmarshal(content, &value); err != nil {
		return value, err
	}
	if value.ExpiresAt > 0 && value.ExpiresAt < time.Now().UnixMilli() {
		value.Status = operationExpired
		return value, Error("selection_expired", 409, "conflict")
	}
	return value, nil
}
