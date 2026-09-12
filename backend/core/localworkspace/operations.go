package localworkspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"

	"lazymind/core/state"
)

type OperationKind string

const (
	OperationRead      OperationKind = "read"
	OperationCreate    OperationKind = "create"
	OperationAppend    OperationKind = "append"
	OperationReplace   OperationKind = "replace"
	OperationDelete    OperationKind = "delete"
	OperationOverwrite OperationKind = "overwrite"
	OperationMkdir     OperationKind = "mkdir"
	OperationList      OperationKind = "ls"
	OperationGlob      OperationKind = "glob"
	OperationGrep      OperationKind = "grep"
	OperationInfo      OperationKind = "info"
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
	ExecutionMode        string        `json:"execution_mode,omitempty"`
	ArgumentsDigest      string        `json:"arguments_digest,omitempty"`
	ParentIdentity       string        `json:"parent_identity,omitempty"`
	TargetIdentity       string        `json:"target_identity,omitempty"`
	DependsOn            string        `json:"depends_on,omitempty"`
	UserID               string        `json:"user_id"`
	ConversationID       string        `json:"conversation_id"`
	WorkspaceID          string        `json:"workspace_id"`
	HistoryID            string        `json:"history_id,omitempty"`
	Generation           string        `json:"generation,omitempty"`
	LeaseToken           string        `json:"lease_token,omitempty"`
	RunID                string        `json:"run_id,omitempty"`
	TaskID               string        `json:"task_id,omitempty"`
	AttemptID            string        `json:"attempt_id,omitempty"`
	CallID               string        `json:"call_id"`
	ToolName             string        `json:"tool_name,omitempty"`
	Operation            OperationKind `json:"operation"`
	Path                 string        `json:"path"`
	Content              string        `json:"content,omitempty"`
	OldContent           string        `json:"old_content,omitempty"`
	ExpectedVersion      string        `json:"expected_version,omitempty"`
	ExpectedReplacements int           `json:"expected_replacements,omitempty"`
	Pattern              string        `json:"pattern,omitempty"`
	Glob                 string        `json:"glob,omitempty"`
	Limit                int           `json:"limit,omitempty"`
	Offset               int           `json:"offset,omitempty"`
	MaxLines             int           `json:"max_lines,omitempty"`
}

type OperationResult struct {
	ExecuteAllowed bool           `json:"execute_allowed,omitempty"`
	TargetIdentity string         `json:"target_identity,omitempty"`
	PermissionMode string         `json:"permission_mode,omitempty"`
	Receipt        bool           `json:"receipt,omitempty"`
	OperationID    string         `json:"operation_id,omitempty"`
	Path           string         `json:"path"`
	Content        string         `json:"content,omitempty"`
	Version        string         `json:"version,omitempty"`
	Decision       Decision       `json:"decision,omitempty"`
	Status         string         `json:"status,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	ExpiresAt      int64          `json:"expires_at,omitempty"`
	Data           map[string]any `json:"data,omitempty"`
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
	Slot              int              `json:"slot"`
	WorkspaceVersion  int64            `json:"workspace_version"`
	PermissionMode    string           `json:"permission_mode"`
	PermissionVersion int64            `json:"permission_version"`
}

const (
	operationStateTTL = 5 * time.Minute
	operationClaimTTL = 24 * time.Hour
	maxOperationBytes = 20 << 20
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
	identity, _ := json.Marshal([]string{req.UserID, req.ConversationID, req.RunID, req.HistoryID, req.TaskID, req.Generation, req.AttemptID, req.LeaseToken, req.CallID})
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
	// Preparing never reads file content, including hashes of files awaiting approval.
	decision := permissionDecision(snapshot.PermissionMode, req.Operation, req.Path)
	if req.ExecutionMode == localExecutionMode {
		if err := validateLocalTarget(req, req.TargetIdentity); err != nil {
			return OperationResult{}, err
		}
		if !pathWithin(snapshot.Root, req.Path) {
			decision = DecisionPending
		}
	} else {
		parent, _, info, err := openOperationPath(snapshot, req.Path)
		if err != nil {
			return OperationResult{}, err
		}
		defer parent.Close()
		if err := validateOperationTarget(req, info); err != nil {
			return OperationResult{}, err
		}
	}
	value := operationState{OperationID: operationID, Request: requestWithoutContent(req),
		ContentDigest: digestString(req.Content), OldContentDigest: digestString(req.OldContent),
		Decision: decision, Status: "preparing", Version: req.ExpectedVersion, Slot: -1,
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

func ExecuteOperation(ctx context.Context, db *gorm.DB, stateStore state.Store, operationID string, req OperationRequest) (OperationResult, error) {
	if req.ExecutionMode != "" {
		return OperationResult{}, Error("selection_forbidden", 403, "forbidden")
	}
	if stateStore == nil || db == nil {
		return OperationResult{}, errors.New("store not initialized")
	}
	value, err := loadOperationState(ctx, stateStore, operationID)
	if err != nil {
		return OperationResult{}, err
	}
	if !matchesOperation(value, req) {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	if _, err := validateOperationRun(ctx, db, stateStore, req); err != nil {
		return OperationResult{}, err
	}
	if value.Status == operationCompleted {
		return operationResult(value), nil
	}
	if value.Decision != DecisionAllowed {
		return operationResult(value), Error("selection_forbidden", 403, "forbidden")
	}
	// An executing operation has already crossed the mutation boundary. A
	// retry cannot safely infer whether the filesystem mutation happened, so it
	// must not replay it merely because the lock owner has the same CallID.
	if value.Status != operationAllowed {
		return operationResult(value), Error("binding_conflict", 409, "conflict")
	}
	claimed, err := stateStore.SetNX(ctx, operationLockKey(operationID), []byte(req.CallID), operationClaimTTL)
	if err != nil {
		return OperationResult{}, err
	}
	claimHeld := claimed
	defer func() {
		if claimHeld {
			releaseOperationClaim(ctx, stateStore, operationID, req.CallID)
		}
	}()
	value, err = loadOperationState(ctx, stateStore, operationID)
	if err != nil {
		return OperationResult{}, err
	}
	if value.Status == operationCompleted {
		return operationResult(value), nil
	}
	if !claimed || value.Status != operationAllowed || value.Decision != DecisionAllowed {
		return operationResult(value), Error("binding_conflict", 409, "conflict")
	}
	value.Status = operationExecuting
	if err := saveOperationState(ctx, stateStore, value); err != nil {
		return OperationResult{}, err
	}
	// Once executing is persisted, retain the claim. If that write failed, the
	// deferred compare-and-delete lets a retry reclaim only when the operation
	// is still in the allowed state; an ambiguous persisted executing state is
	// intentionally treated as non-replayable by the guard above.
	claimHeld = false
	var result OperationResult
	touched := false
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockOperationRun(tx, req); err != nil {
			return err
		}
		// The same rows are updated by revoke/reauthorize and permission changes.
		for _, lock := range []struct {
			model  any
			where  string
			args   []any
			column string
		}{
			{&orm.LocalWorkspace{}, "id = ?", []any{req.WorkspaceID}, "version"},
			{&orm.ConversationWorkspaceBinding{}, "conversation_id = ?", []any{req.ConversationID}, "permission_version"},
		} {
			if err := tx.Model(lock.model).Where(lock.where, lock.args...).UpdateColumn(lock.column, gorm.Expr(lock.column)).Error; err != nil {
				return err
			}
		}
		runSnapshot, err := validateOperationRun(ctx, tx, stateStore, req)
		if err != nil {
			return err
		}
		snapshot, err := resolveOperation(ctx, tx, req, runSnapshot)
		if err != nil {
			return err
		}
		if snapshot.WorkspaceVersion != value.WorkspaceVersion {
			return Error("selection_forbidden", 403, "forbidden")
		}
		parent, name, info, err := openOperationPath(snapshot, req.Path)
		if err != nil {
			return err
		}
		defer parent.Close()
		if err := validateOperationTarget(req, info); err != nil {
			return err
		}
		if time.Now().UnixMilli() >= value.ExpiresAt {
			return Error("selection_expired", 409, "conflict")
		}
		revalidate := func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if time.Now().UnixMilli() >= value.ExpiresAt {
				return Error("selection_expired", 409, "conflict")
			}
			_, err := validateOperationRun(ctx, tx, stateStore, req)
			return err
		}
		result, err = executeFileOperation(ctx, parent, name, info, req, snapshot.PermissionMode, &touched, revalidate)
		return err
	})
	if err != nil {
		value.Status = operationFailed
		if touched {
			value.Status = operationUncertain
		}
		var app *common.AppError
		if errors.As(err, &app) {
			value.Result.Reason = workspaceErrorReason(app)
		}
		if saveErr := saveOperationState(ctx, stateStore, value); saveErr != nil {
			return OperationResult{}, saveErr
		}
		return operationResult(value), err
	}
	result.OperationID, result.Path, result.Status = operationID, req.Path, operationCompleted
	value.Status, value.Result, value.Version = operationCompleted, result, result.Version
	value.Result.Content, value.Result.Data = "", nil
	if err := saveOperationState(ctx, stateStore, value); err != nil {
		value.Status, value.Result.Reason = operationUncertain, "operation_uncertain"
		_ = saveOperationState(ctx, stateStore, value)
		return OperationResult{}, err
	}
	return result, nil
}

func releaseOperationClaim(ctx context.Context, store state.Store, operationID, owner string) {
	atomic, ok := store.(state.CompareAndDeleteStore)
	if !ok {
		return
	}
	_, _ = atomic.CompareAndDelete(ctx, operationLockKey(operationID), []byte(owner))
}

func resolveOperation(ctx context.Context, db *gorm.DB, req OperationRequest, runSnapshot *ContextSnapshot) (*ContextSnapshot, error) {
	live, err := ResolveForConversation(ctx, db, req.UserID, req.ConversationID)
	if err != nil {
		return nil, err
	}
	if live == nil || live.WorkspaceID != req.WorkspaceID {
		return nil, Error("workspace_not_found", 404, "resource not found")
	}
	if runSnapshot == nil {
		runSnapshot = live
	}
	if runSnapshot.WorkspaceID != req.WorkspaceID || live.WorkspaceVersion != runSnapshot.WorkspaceVersion {
		return nil, Error("selection_forbidden", 403, "forbidden")
	}
	runSnapshot.Root, runSnapshot.DirectoryIdentity, runSnapshot.Sources = live.Root, live.DirectoryIdentity, live.Sources
	return runSnapshot, nil
}

func readOperation(op OperationKind) bool {
	return op == OperationRead || op == OperationList || op == OperationGlob || op == OperationGrep || op == OperationInfo
}

func validateOperationRequest(req OperationRequest) error {
	local := req.ExecutionMode == localExecutionMode
	validPath := fs.ValidPath(req.Path) && !strings.ContainsAny(req.Path, "\\:\x00")
	if local {
		if !Enabled() {
			return ModeError()
		}
		validPath = filepath.IsAbs(req.Path) && filepath.Clean(req.Path) == req.Path && !strings.ContainsRune(req.Path, 0)
		if len(req.Path) > 4096 || !validDigest(req.ArgumentsDigest) || len(req.ParentIdentity) > 160 ||
			len(req.TargetIdentity) > 160 || req.ParentIdentity == "" || req.TargetIdentity == "" ||
			(req.DependsOn != "" && !validDigest(req.DependsOn)) {
			return Error("invalid_selection", 400, "invalid request")
		}
	} else if req.ExecutionMode != "" || req.ArgumentsDigest != "" || req.ParentIdentity != "" || req.TargetIdentity != "" || req.DependsOn != "" {
		return Error("invalid_selection", 400, "invalid request")
	}
	if req.UserID == "" || req.ConversationID == "" || req.WorkspaceID == "" || req.CallID == "" || len(req.CallID) > 512 ||
		req.Path == "" || !validPath ||
		len(req.Content) > maxOperationBytes || len(req.OldContent) > maxOperationBytes || !utf8.ValidString(req.Content) || strings.ContainsRune(req.Content, 0) {
		return Error("invalid_selection", 400, "invalid request")
	}
	for _, part := range strings.Split(filepath.ToSlash(req.Path), "/") {
		if strings.EqualFold(part, ".git") || (part != "." && strings.HasSuffix(part, ".")) || strings.HasSuffix(part, " ") {
			return Error("path_invalid", 400, "invalid request")
		}
	}
	switch req.Operation {
	case OperationRead, OperationCreate, OperationAppend, OperationReplace, OperationDelete, OperationOverwrite, OperationMkdir, OperationList, OperationGlob, OperationGrep, OperationInfo:
	default:
		return Error("invalid_selection", 400, "invalid request")
	}
	if !readOperation(req.Operation) && isSensitivePath(req.Path) {
		return Error("selection_forbidden", 403, "forbidden")
	}
	if req.Operation == OperationReplace && (req.OldContent == "" || req.ExpectedReplacements < 0 || req.ExpectedReplacements > 100) {
		return Error("invalid_selection", 400, "invalid request")
	}
	if req.Offset < 0 || req.MaxLines < 0 || req.Limit < 0 || len(req.Pattern) > 4096 || len(req.Glob) > 4096 {
		return Error("invalid_selection", 400, "invalid request")
	}
	return nil
}

func validateOperationTarget(req OperationRequest, info os.FileInfo) error {
	create := req.Operation == OperationCreate || req.Operation == OperationMkdir
	if create && info != nil {
		return Error("binding_conflict", 409, "conflict")
	}
	if !create && info == nil {
		return Error("workspace_not_found", 404, "resource not found")
	}
	directory := req.Operation == OperationList || req.Operation == OperationGlob || req.Operation == OperationGrep
	if info != nil && ((directory && !info.IsDir()) || (!directory && req.Operation != OperationInfo && !info.Mode().IsRegular())) {
		return Error("path_invalid", 400, "invalid request")
	}
	return nil
}

// Each component is opened once and checked against the identity observed before open.
// This does not provide an atomic compare-and-swap against arbitrary external editors.
func openOperationPath(snapshot *ContextSnapshot, relative string) (*os.Root, string, os.FileInfo, error) {
	root, err := os.OpenRoot(snapshot.Root)
	if err != nil {
		return nil, "", nil, err
	}
	fail := func(err error) (*os.Root, string, os.FileInfo, error) { root.Close(); return nil, "", nil, err }
	file, err := root.Open(".")
	if err != nil {
		return fail(err)
	}
	identity, err := openedDirectoryIdentity(file)
	file.Close()
	if err != nil || identity != snapshot.DirectoryIdentity {
		return fail(Error("path_unavailable", 409, "conflict"))
	}
	parts := strings.Split(relative, "/")
	for _, part := range parts[:len(parts)-1] {
		before, err := root.Lstat(part)
		if err != nil {
			return fail(err)
		}
		if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return fail(Error("path_invalid", 400, "invalid request"))
		}
		child, err := root.OpenRoot(part)
		if err != nil {
			return fail(err)
		}
		after, err := child.Stat(".")
		if err != nil || !os.SameFile(before, after) {
			child.Close()
			return fail(Error("path_invalid", 400, "invalid request"))
		}
		root.Close()
		root = child
	}
	name := parts[len(parts)-1]
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return root, name, nil, nil
	}
	if err != nil {
		return fail(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return fail(Error("path_invalid", 400, "invalid request"))
	}
	return root, name, info, nil
}

func readOperationFile(root *os.Root, name string, info os.FileInfo) ([]byte, error) {
	if info == nil || !info.Mode().IsRegular() || info.Size() > maxOperationBytes {
		return nil, Error("path_invalid", 400, "invalid request")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || (!os.SameFile(info, actual) || fileHasAliases(file, actual)) {
		return nil, Error("binding_conflict", 409, "conflict")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxOperationBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxOperationBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return nil, Error("unsupported_file", 400, "invalid request")
	}
	after, err := file.Stat()
	if err != nil || after.Size() != actual.Size() || after.ModTime() != actual.ModTime() {
		return nil, Error("binding_conflict", 409, "conflict")
	}
	return data, nil
}

func executeFileOperation(ctx context.Context, root *os.Root, name string, info os.FileInfo, req OperationRequest, mode string, touched *bool, revalidate func() error) (OperationResult, error) {
	if req.Operation == OperationList || req.Operation == OperationGlob || req.Operation == OperationGrep || req.Operation == OperationInfo {
		return discoverOperation(ctx, root, name, info, req, mode)
	}
	if req.Operation == OperationMkdir {
		if err := revalidate(); err != nil {
			return OperationResult{}, err
		}
		err := root.Mkdir(name, 0o700)
		if err == nil {
			*touched = true
		}
		return OperationResult{}, err
	}
	var old []byte
	var err error
	if info != nil {
		old, err = readOperationFile(root, name, info)
		if err != nil {
			return OperationResult{}, err
		}
	}
	version := digestBytes(old)
	if req.Operation == OperationRead {
		text := string(old)
		if req.Offset > 0 || req.MaxLines > 0 {
			lines := strings.SplitAfter(text, "\n")
			start := min(req.Offset, len(lines))
			end := len(lines)
			if req.MaxLines > 0 {
				end = min(end, start+req.MaxLines)
			}
			text = strings.Join(lines[start:end], "")
		}
		return OperationResult{Content: text, Version: version}, nil
	}
	if req.Operation != OperationCreate && (req.ExpectedVersion == "" || req.ExpectedVersion != version) {
		return OperationResult{}, Error("binding_conflict", 409, "conflict")
	}
	if req.Operation == OperationDelete {
		if err := revalidate(); err != nil {
			return OperationResult{}, err
		}
		current, err := root.Lstat(name)
		if err != nil || !os.SameFile(info, current) {
			return OperationResult{}, Error("binding_conflict", 409, "conflict")
		}
		check, err := readOperationFile(root, name, current)
		if err != nil {
			return OperationResult{}, err
		}
		if digestBytes(check) != version {
			return OperationResult{}, Error("binding_conflict", 409, "conflict")
		}
		err = root.Remove(name)
		if err == nil {
			*touched = true
		}
		return OperationResult{Version: version}, err
	}
	content := req.Content
	switch req.Operation {
	case OperationAppend:
		content = string(old) + req.Content
	case OperationReplace:
		count := req.ExpectedReplacements
		if count == 0 {
			count = 1
		}
		normalize := func(text string) string {
			return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
		}
		expression := strings.Join(strings.Split(regexp.QuoteMeta(normalize(req.OldContent)), "\n"), "(?:\r\n|\n|\r)")
		literal := regexp.MustCompile(expression)
		if len(literal.FindAllIndex(old, count+1)) != count {
			return OperationResult{}, Error("binding_conflict", 409, "conflict")
		}
		crlf := strings.Count(string(old), "\r\n")
		lf := strings.Count(string(old), "\n") - crlf
		cr := strings.Count(string(old), "\r") - crlf
		newline := "\n"
		if crlf > 0 && crlf >= lf && crlf >= cr {
			newline = "\r\n"
		} else if cr > lf {
			newline = "\r"
		}
		replacement := strings.ReplaceAll(normalize(req.Content), "\n", newline)
		content = literal.ReplaceAllStringFunc(string(old), func(string) string { return replacement })
	}
	if len(content) > maxOperationBytes {
		return OperationResult{}, Error("invalid_selection", 400, "invalid request")
	}
	id, err := newID()
	if err != nil {
		return OperationResult{}, err
	}
	temp := ".lazymind-" + id
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return OperationResult{}, err
	}
	defer root.Remove(temp)
	if info != nil {
		err = file.Chmod(info.Mode().Perm())
	}
	if err == nil {
		_, err = io.WriteString(file, content)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return OperationResult{}, err
	}
	if info != nil {
		current, err := root.Lstat(name)
		if err != nil || !os.SameFile(info, current) {
			return OperationResult{}, Error("binding_conflict", 409, "conflict")
		}
		check, err := readOperationFile(root, name, current)
		if err != nil {
			return OperationResult{}, err
		}
		if digestBytes(check) != version {
			return OperationResult{}, Error("binding_conflict", 409, "conflict")
		}
	}
	if err := revalidate(); err != nil {
		return OperationResult{}, err
	}
	if req.Operation == OperationCreate {
		err = root.Link(temp, name)
	} else {
		err = root.Rename(temp, name)
	}
	if err != nil {
		return OperationResult{}, err
	}
	*touched = true
	return OperationResult{Content: content, Version: digestBytes([]byte(content))}, nil
}

func digestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func permissionDecision(mode string, operation OperationKind, path string) Decision {
	if mode == PermissionAllowAll || (readOperation(operation) && !isSensitivePath(path)) {
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
	slash := strings.ToLower(filepath.ToSlash(path))
	base := strings.ToLower(filepath.Base(path))
	if strings.Contains("/"+slash+"/", "/.ssh/") || strings.Contains("/"+slash+"/", "/.aws/") {
		return true
	}
	if (base == ".env" || strings.HasPrefix(base, ".env.")) && base != ".env.example" && base != ".env.sample" && base != ".env.template" {
		return true
	}
	if base == "id_rsa" || base == "id_ed25519" || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".pem") {
		return true
	}
	return strings.Contains(base, "credentials") || strings.HasPrefix(base, "service-account")
}

func matchesOperation(value operationState, req OperationRequest) bool {
	return requestWithoutContent(value.Request) == requestWithoutContent(req) &&
		value.ContentDigest == digestString(req.Content) && value.OldContentDigest == digestString(req.OldContent)
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

func operationSlotKey(conversation string, slot int) string {
	return fmt.Sprintf("local-workspace-operation-slot:%s:%d", conversation, slot)
}

func operationResult(value operationState) OperationResult {
	result := value.Result
	if value.Request.ExecutionMode == localExecutionMode {
		result.PermissionMode = value.PermissionMode
	}
	result.Receipt = value.Status == operationCompleted
	result.OperationID, result.Path, result.Version = value.OperationID, value.Request.Path, value.Version
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

func discoverOperation(ctx context.Context, root *os.Root, name string, info os.FileInfo, req OperationRequest, mode string) (OperationResult, error) {
	sourceID := "local-workspace:" + req.WorkspaceID
	entry := func(relative string, info os.FileInfo) map[string]any {
		kind := "file"
		if info.IsDir() {
			kind = "directory"
		}
		return map[string]any{"name": info.Name(), "path": relative, "type": kind, "source_id": sourceID, "size": info.Size(), "mtime": info.ModTime().UTC().Format(time.RFC3339)}
	}
	if req.Operation == OperationInfo {
		return OperationResult{Data: entry(req.Path, info)}, nil
	}
	directory, err := root.OpenRoot(name)
	if err != nil {
		return OperationResult{}, err
	}
	defer directory.Close()
	actual, err := directory.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		return OperationResult{}, Error("path_invalid", 400, "invalid request")
	}
	limit := req.Limit
	if limit == 0 {
		limit = 200
	}
	limit = min(limit, 200)
	var regex *regexp.Regexp
	if req.Operation == OperationGrep {
		regex, err = regexp.Compile(req.Pattern)
		if err != nil {
			return OperationResult{}, Error("invalid_selection", 400, "invalid request")
		}
	}
	matches := []any{}
	skipped := []map[string]string{}
	scanned, totalBytes := 0, 0
	var walk func(*os.Root, string, int) error
	walk = func(dir *os.Root, relative string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := dir.Open(".")
		if err != nil {
			return err
		}
		entries, err := f.ReadDir(1001)
		f.Close()
		if err != nil && err != io.EOF {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, item := range entries {
			scanned++
			if scanned > 1000 || len(matches) >= limit {
				return nil
			}
			relativePath := path.Join(relative, item.Name())
			if strings.EqualFold(item.Name(), ".git") || strings.HasPrefix(item.Name(), ".lazymind-") {
				continue
			}
			stat, err := dir.Lstat(item.Name())
			if err != nil {
				return err
			}
			if stat.Mode()&os.ModeSymlink != 0 || (!stat.IsDir() && !stat.Mode().IsRegular()) {
				skipped = append(skipped, map[string]string{"path": relativePath, "reason": "path_invalid"})
				continue
			}
			if req.Operation == OperationList {
				matches = append(matches, entry(relativePath, stat))
				continue
			}
			if stat.IsDir() {
				if depth >= 32 {
					skipped = append(skipped, map[string]string{"path": relativePath, "reason": "path_invalid"})
					continue
				}
				child, err := dir.OpenRoot(item.Name())
				if err != nil {
					return err
				}
				opened, err := child.Stat(".")
				if err == nil && !os.SameFile(stat, opened) {
					err = Error("path_invalid", 400, "invalid request")
				}
				if err == nil {
					err = walk(child, relativePath, depth+1)
				}
				child.Close()
				if err != nil {
					return err
				}
				continue
			}
			pattern := req.Glob
			if req.Operation == OperationGlob {
				pattern = req.Pattern
			}
			if pattern != "" && pattern != "*" {
				candidate := strings.TrimPrefix(relativePath, req.Path+"/")
				matched, matchErr := matchOperationGlob(pattern, candidate)
				if matchErr != nil {
					return Error("invalid_selection", 400, "invalid request")
				}
				if !matched {
					continue
				}
			}
			if req.Operation == OperationGlob {
				matches = append(matches, relativePath)
				continue
			}
			if permissionDecision(mode, OperationRead, relativePath) != DecisionAllowed {
				skipped = append(skipped, map[string]string{"path": relativePath, "reason": "approval_required"})
				continue
			}
			content, err := readOperationFile(dir, item.Name(), stat)
			if err != nil {
				skipped = append(skipped, map[string]string{"path": relativePath, "reason": "unsupported_file"})
				continue
			}
			totalBytes += len(content)
			if totalBytes > maxOperationBytes {
				skipped = append(skipped, map[string]string{"path": relativePath, "reason": "search_limit"})
				return nil
			}
			for line, text := range strings.Split(string(content), "\n") {
				if regex.MatchString(text) {
					matches = append(matches, map[string]any{"file": relativePath, "path": relativePath, "line": line + 1, "content": string([]rune(text)[:min(len([]rune(text)), 500)]), "source_id": sourceID})
				}
				if len(matches) >= limit {
					break
				}
			}
		}
		return nil
	}
	if err := walk(directory, req.Path, 0); err != nil {
		return OperationResult{}, err
	}
	key, count := "matches", "match_count"
	if req.Operation == OperationList {
		key, count = "entries", "entry_count"
	}
	return OperationResult{Data: map[string]any{key: matches, count: len(matches), "source_id": sourceID, "path": req.Path, "pattern": req.Pattern, "max_entries": limit, "truncated": len(matches) >= limit || scanned > 1000 || totalBytes > maxOperationBytes, "skipped": skipped}}, nil
}

// A complete ** segment consumes zero or more directories. Other segments use
// the standard glob syntax, with bounded dynamic programming instead of backtracking.
func matchOperationGlob(pattern, relative string) (bool, error) {
	if !strings.Contains(pattern, "/") && pattern != "**" {
		return path.Match(pattern, path.Base(relative))
	}
	patternParts, parts := strings.Split(pattern, "/"), strings.Split(relative, "/")
	if len(patternParts) > 64 {
		return false, os.ErrInvalid
	}
	reachable := make([]bool, len(parts)+1)
	reachable[0] = true
	for _, segment := range patternParts {
		next := make([]bool, len(parts)+1)
		for index := 0; index <= len(parts); index++ {
			if segment == "**" {
				next[index] = reachable[index] || (index > 0 && next[index-1])
				continue
			}
			if index == len(parts) {
				continue
			}
			match, err := path.Match(segment, parts[index])
			if err != nil {
				return false, err
			}
			next[index+1] = reachable[index] && match
		}
		reachable = next
	}
	return reachable[len(parts)], nil
}
