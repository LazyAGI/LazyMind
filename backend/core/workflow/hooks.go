package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/taskcenter"
)

// RegisterSubAgentHooks wires plugin lifecycle hooks into the subagent EventHooks.
// Must be called once at application startup (after store is initialized).
func RegisterSubAgentHooks() {
	subagent.EventHooks.RegisterArtifactHook(onArtifact)
	subagent.EventHooks.RegisterTerminalStatusHook(onTerminalStatus)

	// Wire the task-cancel hook so that CancelTaskByID actually stops Python execution.
	taskcenter.OnCancelHook = func(ctx context.Context, convID string) {
		db := store.DB()
		stateStore := store.State()
		if db != nil {
			StopActiveWorkflowSession(ctx, db, stateStore, convID)
		}
		go NotifyChatCancel(convID)
	}
}

// onArtifact is called by the subagent runner when any artifact is emitted.
func onArtifact(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, artifactKey string) {
	pctx := loadWorkflowChatContextFromDB(ctx, db, taskID)
	if pctx == nil {
		return
	}
	rev := OnArtifactEvent(ctx, db, taskID, artifactKey, pctx)
	if rev == nil {
		return
	}
	// Slot revision is now durable. Notify the conversation stream so the
	// event-driven WorkflowPanel can refresh immediately without polling or waiting
	// for the whole step to finish.
	emitWorkflowArtifactUpdated(stateStore, pctx.ConvID, map[string]any{
		"session_id": pctx.SessionID,
		"step_id":    pctx.StepID,
		"slot_id":    rev.SlotID,
		"slot":       rev.Slot,
		"revision":   rev.Revision,
	})
}

// NotifyWorkflowArtifactUpdated publishes a durable slot change made outside the
// SubAgent artifact hook (for example a human edit, version selection, or rollback).
// Event delivery is best-effort; the committed revision remains authoritative.
func NotifyWorkflowArtifactUpdated(
	ctx context.Context,
	db *gorm.DB,
	sessionID, stepID, slotID, slot string,
	revision int,
	listIndex *int,
	changeSource string,
) {
	if db == nil {
		return
	}
	session, err := GetSession(ctx, db, sessionID)
	if err != nil {
		return
	}
	payload := map[string]any{
		"session_id":    sessionID,
		"step_id":       stepID,
		"slot_id":       slotID,
		"slot":          slot,
		"revision":      revision,
		"change_source": changeSource,
	}
	if listIndex != nil {
		payload["list_index"] = *listIndex
	}
	emitWorkflowArtifactUpdated(store.State(), session.ConversationID, payload)
}

func emitWorkflowArtifactUpdated(stateStore state.Store, conversationID string, payload map[string]any) {
	if subagent.EventHooks == nil || conversationID == "" {
		return
	}
	subagent.EventHooks.CallConversationEvent(
		context.Background(), stateStore, conversationID, "", "workflow_artifact_updated", payload,
	)
}

// onTerminalStatus is called by the subagent runner when a task reaches terminal status.
func onTerminalStatus(ctx context.Context, db *gorm.DB, stateStore state.Store, taskID, status, message string) {
	if status == subagent.StatusRunning {
		_ = UpdateStepStatus(ctx, db, taskID, status)
		return
	}
	if status != subagent.StatusSucceeded && status != subagent.StatusFailed && status != subagent.StatusInterrupted {
		return
	}
	pctx := loadWorkflowChatContextFromDB(ctx, db, taskID)
	if pctx == nil {
		return
	}
	// Build an onSSE that pushes events to the conversation-level events channel.
	// Use a detached context: ctx originates from the SubAgent Run loop and is
	// cancelled as soon as Run returns, before advanceAutoMode emits driver events.
	onSSE := func(eventType string, payload map[string]any) {
		if subagent.EventHooks != nil {
			subagent.EventHooks.CallConversationEvent(context.Background(), stateStore, pctx.ConvID, "", eventType, payload)
		}
	}
	OnSubAgentDone(ctx, db, stateStore, taskID, status, message, onSSE, pctx)
}

// loadWorkflowChatContextFromDB loads the plugin context for a task from the database.
func loadWorkflowChatContextFromDB(ctx context.Context, db *gorm.DB, taskID string) *WorkflowChatContext {
	task, err := subagent.GetTask(ctx, db, taskID)
	if err != nil || task == nil || task.AgentType != "workflow_step" {
		return nil
	}

	var params WorkflowStepParams
	if len(task.Params) > 0 {
		if err := json.Unmarshal(task.Params, &params); err != nil {
			fmt.Printf("[Workflow] failed to unmarshal params for task %s: %v\n", taskID, err)
			return nil
		}
	}
	if params.WorkflowID == "" || params.SessionID == "" {
		return nil
	}

	return &WorkflowChatContext{
		SessionID:           params.SessionID,
		WorkflowID:          params.WorkflowID,
		StepID:              params.StepID,
		ConvID:              task.ConversationID,
		UserID:              task.CreateUserID,
		WorkflowMode:        params.WorkflowMode,
		ChatSessionID:       params.ChatSessionID,
		TriggerHistoryID:    task.TriggerHistoryID,
		HistoryFilesPerTurn: params.HistoryFilesPerTurn,
		HandOff:             params.HandOff,
	}
}

// StopActiveWorkflowSession marks all queued or running steps as interrupted and puts the session
// into waiting status. Python task cancellation and UI notification use the generic
// task lifecycle paths; no workflow-specific step completion queue is involved.
func StopActiveWorkflowSession(ctx context.Context, db *gorm.DB, stateStore state.Store, convID string) {
	session, err := GetActiveSession(ctx, db, convID)
	if err != nil || session == nil {
		return
	}
	stopWorkflowSession(ctx, db, stateStore, session)
}

// stopWorkflowSession cancels one specific session. Callers that already resolved a
// session must use this scoped form instead of looking it up again by conversation;
// a conversation can retain an older waiting session next to a newer active one.
func stopWorkflowSession(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	session *orm.WorkflowSession,
) {
	if session == nil || session.Status != SessionStatusActive {
		return
	}

	// A transition persists the task and step before the runner emits task_start.
	// Include pending attempts so a stop racing with that launch cannot leave a
	// parallel subtask behind. Waiting/failed/interrupted attempts have no live
	// runner and therefore do not need a cancellation signal.
	steps, err := ListSteps(ctx, db, session.ID)
	if err != nil {
		return
	}
	for _, step := range steps {
		if step.Validity == "stale" || (step.Status != StepStatusPending && step.Status != StepStatusRunning) {
			continue
		}
		// Mark the task first. If a terminal completion won the race, preserve it
		// and do not create a contradictory interrupted workflow-step projection.
		accepted, err := subagent.AcceptFinalStatus(
			ctx, db, step.TaskID, subagent.StatusInterrupted, "stopped by user",
		)
		if err != nil || !accepted {
			continue
		}
		// Mirror into workflow_session_steps.
		_ = UpdateStepStatus(ctx, db, step.TaskID, StepStatusInterrupted)
		// Notify Python to cancel the ReAct loop for this task.
		go notifyTaskCancel(step.TaskID)
	}

	// Put session into waiting so the user can resume.
	_ = UpdateSessionStatus(ctx, db, session.ID, SessionStatusWaiting)

	// Push step_waiting SSE event to the conversation channel.
	if subagent.EventHooks != nil {
		subagent.EventHooks.CallConversationEvent(ctx, stateStore, session.ConversationID, "", "step_waiting", map[string]any{
			"session_id":   session.ID,
			"step_id":      session.CurrentStepID,
			"interrupted":  true,
			"user_stopped": true,
			"reason":       "user_stopped",
		})
	}
}

// notifyTaskCancel posts a cancel signal to the Python chat service so that
// the SubAgent ReAct loop terminates at the next iteration boundary.
// Called in a goroutine; errors are logged and suppressed.
func notifyTaskCancel(taskID string) {
	body, _ := json.Marshal(map[string]string{
		"task_id": taskID,
	})
	url := common.JoinURL(common.ChatServiceEndpoint(), "/api/workflow/task-cancel")
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		fmt.Printf("[plugin] notifyTaskCancel: %v\n", err)
		return
	}
	_ = resp.Body.Close()
}

// NotifyChatCancel posts a cancel signal to the Python chat service so that
// the active ChatAgent session for the given conversation terminates.
// Called by StopChatGeneration in a goroutine; errors are logged and suppressed.
func NotifyChatCancel(convID string) {
	body, _ := json.Marshal(map[string]string{
		"conversation_id": convID,
	})
	url := common.JoinURL(common.ChatServiceEndpoint(), "/api/workflow/task-cancel")
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		fmt.Printf("[plugin] NotifyChatCancel: %v\n", err)
		return
	}
	_ = resp.Body.Close()
}
