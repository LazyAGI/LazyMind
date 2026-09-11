package localworkspace

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/graphengine"
)

type StopConversationFunc func(context.Context, string, string) error

var stopConversation struct {
	sync.RWMutex
	fn StopConversationFunc
}

func SetStopConversationFunc(fn StopConversationFunc) {
	stopConversation.Lock()
	stopConversation.fn = fn
	stopConversation.Unlock()
}

func requestConversationStop(ctx context.Context, userID, conversationID string) error {
	stopConversation.RLock()
	fn := stopConversation.fn
	stopConversation.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, userID, conversationID)
}

// ValidateOperationRunFunc is implemented by chat/subagent/workflow owners. It
// deliberately lives behind a callback so localworkspace does not import the
// packages that own run lifecycle state (and create an import cycle).
type ValidateOperationRunFunc func(context.Context, *gorm.DB, state.Store, OperationRequest) (*ContextSnapshot, error)

var operationRunValidator struct {
	sync.RWMutex
	fn ValidateOperationRunFunc
}

// SetValidateOperationRunFunc installs the authoritative Core run validator.
// The callback must validate owner, conversation, run/task/attempt identity,
// generation and lease, as applicable, against live state.
func SetValidateOperationRunFunc(fn ValidateOperationRunFunc) {
	operationRunValidator.Lock()
	operationRunValidator.fn = fn
	operationRunValidator.Unlock()
}

// validateOperationRun rejects unregistered execution, including missing wiring.
func validateOperationRun(ctx context.Context, db *gorm.DB, stateStore state.Store, req OperationRequest) (*ContextSnapshot, error) {
	invalid := Error("binding_conflict", 409, "conflict")
	if db == nil || stateStore == nil || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.ConversationID) == "" {
		return nil, invalid
	}
	var count int64
	if err := db.WithContext(ctx).Model(&orm.Conversation{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", req.ConversationID, req.UserID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, invalid
	}
	operationRunValidator.RLock()
	fn := operationRunValidator.fn
	operationRunValidator.RUnlock()
	if fn == nil {
		return nil, invalid
	}
	if req.AttemptID != "" {
		generation, err := strconv.ParseInt(req.Generation, 10, 64)
		if err != nil || generation <= 0 || req.TaskID == "" || req.LeaseToken == "" || req.RunID != "" || req.HistoryID != "" {
			return nil, invalid
		}
		if err := attempt.New(db, attempt.Config{}).ValidateLease(ctx, req.AttemptID, req.LeaseToken); err != nil {
			return nil, invalid
		}
		query := db.WithContext(ctx).Model(&orm.WorkflowSessionStep{}).
			Joins("JOIN plugin_sessions ws ON ws.id = plugin_session_steps.session_id").
			Joins("JOIN sub_agent_tasks task ON task.id = plugin_session_steps.task_id").
			Where("plugin_session_steps.id = ? AND plugin_session_steps.task_id = ? AND plugin_session_steps.fencing_generation = ? AND plugin_session_steps.validity = 'effective'", req.AttemptID, req.TaskID, generation).
			Where("ws.conversation_id = ? AND ws.create_user_id = ? AND ws.dismissed = ? AND ws.status IN ?", req.ConversationID, req.UserID, false, []string{"active", "waiting"}).
			Where("task.conversation_id = ? AND task.create_user_id = ? AND task.agent_type = 'workflow_step' AND task.status IN ?", req.ConversationID, req.UserID, []string{"pending", "running"})
		if err := query.Count(&count).Error; err != nil {
			return nil, err
		}
		if count != 1 {
			return nil, invalid
		}
		// The pinned graph, rather than model-supplied tool names, grants the toolkit.
		var declaration struct {
			CompiledGraph json.RawMessage
			StepID        string
		}
		err = db.WithContext(ctx).Table("plugin_session_steps step").
			Select("revision.compiled_graph, step.step_id").
			Joins("JOIN plugin_sessions session ON session.id = step.session_id").
			Joins("JOIN plugin_revisions revision ON revision.id = session.plugin_revision_id").
			Where("step.id = ?", req.AttemptID).Scan(&declaration).Error
		if err != nil {
			return nil, err
		}
		var graph graphengine.CompiledStateGraph
		if json.Unmarshal(declaration.CompiledGraph, &graph) != nil || !slices.Contains(graph.Nodes[declaration.StepID].LegacyTools, "local_fs") {
			return nil, invalid
		}
		return fn(ctx, db, stateStore, req)
	}
	if req.LeaseToken != "" {
		return nil, invalid
	}
	return fn(ctx, db, stateStore, req)
}

// LockOperationRun is called inside the file-commit transaction, before live
// validation. Chat transitions lock the same conversation row; task updates and
// Workflow lease/terminal updates lock their own existing rows automatically.
func LockOperationRun(tx *gorm.DB, req OperationRequest) error {
	var step orm.WorkflowSessionStep
	if req.AttemptID != "" {
		if err := tx.Select("session_id").Where("id = ?", req.AttemptID).First(&step).Error; err != nil {
			return err
		}
	}
	for _, row := range []struct {
		model any
		id    string
	}{
		{&orm.Conversation{}, req.ConversationID},
		{&orm.WorkflowSession{}, step.SessionID},
		{&orm.SubAgentTask{}, req.TaskID},
		{&orm.WorkflowSessionStep{}, req.AttemptID},
	} {
		if row.id != "" {
			if err := tx.Model(row.model).Where("id = ?", row.id).
				UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
