package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"gorm.io/gorm"

	"lazymind/core/common"
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
			StopActivePluginSession(ctx, db, stateStore, convID)
		}
		go NotifyChatCancel(convID)
	}
}

// onArtifact is called by the subagent runner when any artifact is emitted.
func onArtifact(ctx context.Context, db *gorm.DB, taskID, artifactKey string) {
	pctx := loadPluginChatContextFromDB(ctx, db, taskID)
	if pctx == nil {
		return
	}
	OnArtifactEvent(ctx, db, taskID, artifactKey, pctx)
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
	pctx := loadPluginChatContextFromDB(ctx, db, taskID)
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

// loadPluginChatContextFromDB loads the plugin context for a task from the database.
func loadPluginChatContextFromDB(ctx context.Context, db *gorm.DB, taskID string) *PluginChatContext {
	task, err := subagent.GetTask(ctx, db, taskID)
	if err != nil || task == nil || task.AgentType != "plugin_step" {
		return nil
	}

	var params PluginStepParams
	if len(task.Params) > 0 {
		if err := json.Unmarshal(task.Params, &params); err != nil {
			fmt.Printf("[Plugin] failed to unmarshal params for task %s: %v\n", taskID, err)
			return nil
		}
	}
	if params.PluginID == "" || params.SessionID == "" {
		return nil
	}

	return &PluginChatContext{
		SessionID:           params.SessionID,
		PluginID:            params.PluginID,
		StepID:              params.StepID,
		ConvID:              task.ConversationID,
		UserID:              task.CreateUserID,
		PluginMode:          params.PluginMode,
		ChatSessionID:       params.ChatSessionID,
		HistoryFilesPerTurn: params.HistoryFilesPerTurn,
		HandOff:             params.HandOff,
	}
}

// StopActivePluginSession marks all running steps as interrupted and puts the session
// into waiting status. Python task cancellation and UI notification use the generic
// task lifecycle paths; no plugin-specific step completion queue is involved.
func StopActivePluginSession(ctx context.Context, db *gorm.DB, stateStore state.Store, convID string) {
	session, err := GetActiveSession(ctx, db, convID)
	if err != nil || session == nil {
		return
	}

	// Find running steps to interrupt.
	steps, err := ListSteps(ctx, db, session.ID)
	if err != nil {
		return
	}
	for _, step := range steps {
		if step.Status != StepStatusRunning {
			continue
		}
		// Mark the sub_agent_task as interrupted.
		_ = subagent.UpdateFinalStatus(ctx, db, step.TaskID, subagent.StatusInterrupted, "stopped by user")
		// Mirror into plugin_session_steps.
		_ = UpdateStepStatus(ctx, db, step.TaskID, StepStatusInterrupted)
		// Notify Python to cancel the ReAct loop for this task.
		go notifyTaskCancel(step.TaskID)
	}

	// Put session into waiting so the user can resume.
	_ = UpdateSessionStatus(ctx, db, session.ID, SessionStatusWaiting)

	// Push step_waiting SSE event to the conversation channel.
	if subagent.EventHooks != nil {
		subagent.EventHooks.CallConversationEvent(ctx, stateStore, convID, "", "step_waiting", map[string]any{
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
	url := common.JoinURL(common.ChatServiceEndpoint(), "/api/plugin/task-cancel")
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
	url := common.JoinURL(common.ChatServiceEndpoint(), "/api/plugin/task-cancel")
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		fmt.Printf("[plugin] NotifyChatCancel: %v\n", err)
		return
	}
	_ = resp.Body.Close()
}
