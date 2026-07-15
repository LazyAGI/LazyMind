package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/taskcenter"
)

var pluginOutboxDispatcherOnce sync.Once

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

// RecoverPendingPluginRuns closes the accepted-but-not-started crash window.
// Pending items are safe to claim once; dispatching items belonged to a worker
// in the previous process and are made explicitly interrupted instead of being
// silently retried or left running forever.
func RecoverPendingPluginRuns() {
	pluginOutboxDispatcherOnce.Do(func() {
		recoverPluginRunOutboxOnce()
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				dispatchPendingPluginRuns()
			}
		}()
	})
}

func recoverPluginRunOutboxOnce() {
	db := store.DB()
	if db == nil {
		return
	}
	ctx := context.Background()
	var abandoned []orm.PluginRunOutbox
	if err := db.WithContext(ctx).Where("status = ?", "dispatching").Find(&abandoned).Error; err == nil {
		for _, row := range abandoned {
			reason := "plugin worker interrupted by backend restart"
			_ = subagent.UpdateFinalStatus(ctx, db, row.TaskID, subagent.StatusInterrupted, reason)
			_ = UpdateStepStatus(ctx, db, row.TaskID, StepStatusInterrupted)
			if pctx := loadPluginChatContextFromDB(ctx, db, row.TaskID); pctx != nil {
				_ = UpdateSessionStatus(ctx, db, pctx.SessionID, SessionStatusWaiting)
			}
			_ = db.Model(&orm.PluginRunOutbox{}).Where("task_id = ?", row.TaskID).
				Updates(map[string]any{"status": "failed", "last_error": reason, "updated_at": time.Now().UTC()}).Error
		}
	}
	dispatchPendingPluginRuns()
}

func dispatchPendingPluginRuns() {
	db := store.DB()
	if db == nil {
		return
	}
	ctx := context.Background()
	var pending []orm.PluginRunOutbox
	if err := db.WithContext(ctx).Where("status = ?", "pending").Order("created_at ASC").Find(&pending).Error; err != nil {
		return
	}
	for _, row := range pending {
		dispatchPluginAttemptRunner(db, store.State(), row.TaskID)
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

// StopActivePluginSession marks all queued or running steps as interrupted and puts the session
// into waiting status. Python task cancellation and UI notification use the generic
// task lifecycle paths; no plugin-specific step completion queue is involved.
func StopActivePluginSession(ctx context.Context, db *gorm.DB, stateStore state.Store, convID string) {
	session, err := GetActiveSession(ctx, db, convID)
	if err != nil || session == nil {
		return
	}
	stopPluginSession(ctx, db, stateStore, session)
}

// stopPluginSession cancels one specific session. Callers that already resolved a
// session must use this scoped form instead of looking it up again by conversation;
// a conversation can retain an older waiting session next to a newer active one.
func stopPluginSession(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	session *orm.PluginSession,
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
		// and do not create a contradictory interrupted plugin-step projection.
		accepted, err := subagent.AcceptFinalStatus(
			ctx, db, step.TaskID, subagent.StatusInterrupted, "stopped by user",
		)
		if err != nil || !accepted {
			continue
		}
		// Mirror into plugin_session_steps.
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
