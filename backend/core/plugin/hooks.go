package plugin

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	"lazymind/core/state"
	"lazymind/core/subagent"
)

// RegisterSubAgentHooks wires plugin lifecycle hooks into the subagent EventHooks.
// Must be called once at application startup (after store is initialized).
func RegisterSubAgentHooks() {
	subagent.EventHooks.RegisterArtifactHook(onArtifact)
	subagent.EventHooks.RegisterTerminalStatusHook(onTerminalStatus)
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
	onSSE := func(eventType string, payload map[string]any) {
		if subagent.EventHooks != nil {
			subagent.EventHooks.CallConversationEvent(ctx, stateStore, pctx.ConvID, "", eventType, payload)
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
		HistoryFilesPerTurn: params.HistoryFilesPerTurn,
	}
}
