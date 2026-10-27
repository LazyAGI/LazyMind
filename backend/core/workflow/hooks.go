package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/displaystate"
)

var OnAcademicRuntimeChanged func(context.Context, *gorm.DB, string)

var chatCancelHTTPClient = &http.Client{Timeout: 5 * time.Second}

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
		go func() {
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := NotifyChatCancel(cancelCtx, convID); err != nil {
				fmt.Printf("[plugin] NotifyChatCancel: %v\n", err)
			}
		}()
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
	if OnAcademicRuntimeChanged != nil {
		if id, ok := payload["session_id"].(string); ok {
			OnAcademicRuntimeChanged(context.Background(), store.DB(), id)
		}
	}
	if subagent.EventHooks == nil || conversationID == "" {
		return
	}
	subagent.EventHooks.CallConversationEvent(
		context.Background(), stateStore, conversationID, "", "workflow_artifact_updated", payload,
	)
}

// NotifyWorkflowRuntimeUpdated invalidates the conversation's Workflow view
// after a hosted attempt changes. WorkflowSessionStep remains authoritative.
func NotifyWorkflowRuntimeUpdated(
	ctx context.Context,
	db *gorm.DB,
	sessionID, attemptID, change string,
) {
	if db == nil {
		return
	}
	if OnAcademicRuntimeChanged != nil {
		OnAcademicRuntimeChanged(ctx, db, sessionID)
	}
	if subagent.EventHooks == nil {
		return
	}
	session, err := GetSession(ctx, db, sessionID)
	if err != nil || session.ConversationID == "" {
		return
	}
	subagent.EventHooks.CallConversationEvent(ctx, store.State(), session.ConversationID, "",
		"workflow_runtime_updated", map[string]any{
			"session_id": sessionID,
			"attempt_id": attemptID,
			"change":     change,
		})
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
		HostedTaskID:        params.HostedTaskID,
	}
}

// StopActiveWorkflowSession fences queued/running attempts. The session remains
// pausing until every executor has acknowledged exit.
func StopActiveWorkflowSession(ctx context.Context, db *gorm.DB, stateStore state.Store, convID string) error {
	var sessions []orm.WorkflowSession
	err := db.WithContext(ctx).Where("conversation_id = ? AND dismissed = false", convID).Find(&sessions).Error
	if err != nil {
		return err
	}
	for i := range sessions {
		if err := stopWorkflowSession(ctx, db, stateStore, &sessions[i], ""); err != nil {
			return err
		}
	}
	return nil
}

// StopWorkflowTurn never selects another run merely because it shares a chat.
func StopWorkflowTurn(ctx context.Context, db *gorm.DB, stateStore state.Store, owner, convID, historyID string) error {
	var sessions []orm.WorkflowSession
	if historyID == "" {
		return nil
	}
	if err := db.WithContext(ctx).Where("create_user_id = ? AND conversation_id = ? AND dismissed = false AND controller_host = ?", owner, convID, "lazymind").Find(&sessions).Error; err != nil {
		return err
	}
	for i := range sessions {
		if err := stopWorkflowSession(ctx, db, stateStore, &sessions[i], historyID); err != nil {
			return err
		}
	}
	return nil
}

// stopWorkflowSession cancels one specific session. Callers that already resolved a
// session must use this scoped form instead of looking it up again by conversation;
// a conversation can retain an older waiting session next to a newer active one.
func stopWorkflowSession(
	ctx context.Context,
	db *gorm.DB,
	stateStore state.Store,
	session *orm.WorkflowSession,
	turnHistoryID string,
) error {
	if session == nil {
		return nil
	}
	if controlstore.Controlled(*session) {
		if session.Status != SessionStatusActive && session.Status != "pausing" {
			return nil
		}
		_, err := (WorkflowControlService{DB: db}).Execute(ctx, session.CreateUserID, session.ID,
			WorkflowControlCommand{Kind: "stop", CommandID: "chat-stop:" + common.GenerateID()})
		return err
	}

	// A transition persists the task and step before the runner emits task_start.
	// Include pending attempts so a stop racing with that launch cannot leave a
	// parallel subtask behind. Waiting/failed/interrupted attempts have no live
	// runner and therefore do not need a cancellation signal.
	changed := false
	var cancelledTaskIDs []string
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current orm.WorkflowSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", session.ID).First(&current).Error; err != nil {
			return err
		}
		if turnHistoryID != "" {
			bound, err := displaystate.ChatHistoryID(current)
			if err != nil {
				return err
			}
			if bound != turnHistoryID {
				return nil
			}
		}
		if current.Status == "pausing" {
			var pending []orm.WorkflowSessionStep
			if err := tx.Where("session_id = ? AND terminal_code = ?", session.ID, "WORKFLOW_STOP_REQUESTED").Find(&pending).Error; err != nil {
				return err
			}
			for _, step := range pending {
				cancelledTaskIDs = append(cancelledTaskIDs, step.TaskID)
			}
			changed = true
			return nil
		}
		steps, err := ListSteps(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		live := false
		for _, step := range steps {
			if step.Validity == "stale" || (step.Status != StepStatusPending && step.Status != StepStatusRunning &&
				step.Status != "queued" && step.Status != "claimed") {
				continue
			}
			live = true
			// Mark the task first. If a terminal completion won the race, preserve it
			// and do not create a contradictory interrupted workflow-step projection.
			accepted, err := subagent.AcceptFinalStatus(
				ctx, tx, step.TaskID, subagent.StatusInterrupted, "stopped by user",
			)
			if err != nil {
				return err
			}
			if !accepted {
				continue
			}
			// Mirror into workflow_session_steps.
			if err := UpdateStepStatus(ctx, tx, step.TaskID, StepStatusInterrupted); err != nil {
				return err
			}
			// Reuse the Host stop reason so task-center projections can distinguish
			// an explicit stop from an ordinary approval checkpoint after reload.
			code := "WORKFLOW_STOPPED"
			if step.LeaseToken != "" || step.Status == StepStatusRunning {
				code = "WORKFLOW_STOP_REQUESTED"
			}
			if err := tx.Model(&orm.WorkflowSessionStep{}).
				Where("task_id = ? AND status = ?", step.TaskID, StepStatusInterrupted).
				UpdateColumns(map[string]any{"terminal_code": code, "lease_expires_at": nil}).Error; err != nil {
				return err
			}
			// Notify Python to cancel the ReAct loop for this task.
			cancelledTaskIDs = append(cancelledTaskIDs, step.TaskID)
		}
		if !live && current.Status != SessionStatusActive {
			return nil
		}

		// Preserve the reason even when stop wins before the first attempt exists.
		// Internal waiting remains resumable; the shared display is explicitly paused.
		now := time.Now().UTC()
		var pending int64
		if err := tx.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND terminal_code = ?", session.ID, "WORKFLOW_STOP_REQUESTED").Count(&pending).Error; err != nil {
			return err
		}
		status := SessionStatusWaiting
		if pending > 0 {
			status = "pausing"
		}
		result := tx.Model(&orm.WorkflowSession{}).
			Where("id = ? AND status = ?", session.ID, current.Status).
			Updates(map[string]any{"status": status, "last_stopped_at": now,
				"state_version": gorm.Expr("state_version + 1"), "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		var updated orm.WorkflowSession
		if err := tx.Where("id = ?", session.ID).First(&updated).Error; err != nil {
			return err
		}
		var interrupted []orm.WorkflowSessionStep
		if err := tx.Where("session_id = ? AND status = ? AND terminal_code = ? AND validity <> ?",
			session.ID, StepStatusInterrupted, "WORKFLOW_STOPPED", "stale").Find(&interrupted).Error; err != nil {
			return err
		}
		nodes := map[string]any{}
		for _, step := range interrupted {
			nodes[step.StepID] = map[string]any{"execution": StepStatusInterrupted}
		}
		payload, _ := json.Marshal(map[string]any{"status": status, "user_stopped": true, "nodes": nodes})
		if err := appendSessionStateEvent(tx, updated, "workflow.patch", payload); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil || !changed {
		return err
	}
	var cancelErrors []error
	for _, taskID := range cancelledTaskIDs {
		if err := notifyTaskCancel(taskID); err != nil {
			cancelErrors = append(cancelErrors, err)
		}
	}

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
	return errors.Join(cancelErrors...)
}

// notifyTaskCancel posts a cancel signal to the Python chat service so that
// the SubAgent ReAct loop terminates at the next iteration boundary.
// A failed notification leaves the durable pause pending and is returned to the caller.
func notifyTaskCancel(taskID string) error {
	body, _ := json.Marshal(map[string]string{
		"task_id": taskID,
	})
	url := common.JoinURL(common.ChatServiceEndpoint(), "/api/workflow/task-cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("task cancellation rejected: HTTP %d", resp.StatusCode)
	}
	return nil
}

// CancelRemovedTaskExecutions runs after committed removal. Restore is fenced
// until its durable cancellation job completes, so this cannot stop a new run.
func CancelRemovedTaskExecutions(ctx context.Context, db *gorm.DB, stateStore state.Store, owner, convID string, before time.Time, executionIDs []string) error {
	var sessions []orm.WorkflowSession
	if err := db.WithContext(ctx).Where("create_user_id=? AND conversation_id=? AND created_at<=? AND status IN ?", owner, convID, before, []string{SessionStatusActive, "pausing"}).Find(&sessions).Error; err != nil {
		return err
	}
	for index := range sessions {
		if err := stopWorkflowSession(ctx, db, stateStore, &sessions[index], ""); err != nil {
			return err
		}
		var current orm.WorkflowSession
		if err := db.WithContext(ctx).Where("id=?", sessions[index].ID).Take(&current).Error; err != nil {
			return err
		}
		if current.Status == SessionStatusActive || current.Status == "pausing" {
			return fmt.Errorf("workflow stop pending")
		}
	}
	for _, id := range executionIDs {
		prefix := convID + "_"
		started, err := strconv.ParseInt(strings.TrimPrefix(id, prefix), 10, 64)
		if !strings.HasPrefix(id, prefix) || err != nil || started > before.UnixMilli() {
			return fmt.Errorf("invalid removal execution identity")
		}
		if err := cancelRemovedChatExecution(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func cancelRemovedChatExecution(ctx context.Context, executionID string) error {
	body, _ := json.Marshal(map[string]string{"task_id": executionID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, common.JoinURL(common.ChatServiceEndpoint(), "/api/workflow/task-cancel"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := chatCancelHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("chat stop unavailable")
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&result); err != nil || !result.OK {
		return fmt.Errorf("chat stop unavailable")
	}
	return nil
}

// NotifyChatCancel posts a cancel signal to the Python chat service so that
// the active ChatAgent session for the given conversation terminates.
// Callers own retry/logging policy; the request itself is always bounded.
func NotifyChatCancel(ctx context.Context, convID string, historyID ...string) error {
	history := ""
	if len(historyID) > 0 {
		history = historyID[0]
	}
	body, _ := json.Marshal(map[string]string{
		"conversation_id": convID,
		"history_id":      history,
	})
	url := common.JoinURL(common.ChatServiceEndpoint(), "/api/workflow/task-cancel")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := chatCancelHTTPClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("chat service returned status %d", resp.StatusCode)
	}
	return nil
}
