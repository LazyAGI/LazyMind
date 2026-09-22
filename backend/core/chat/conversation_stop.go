package chat

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/state"
	"lazymind/core/subagent"
	"lazymind/core/workflow"
)

func StopConversationExecution(ctx context.Context, db *gorm.DB, stateStore state.Store,
	userID, conversationID, historyID, reason string) error {
	if db == nil {
		return fmt.Errorf("store not initialized")
	}
	var conversation orm.Conversation
	if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL",
		conversationID, userID).First(&conversation).Error; err != nil {
		return errConversationUnavailable
	}
	if strings.TrimSpace(reason) == "" {
		reason = "stopped by user"
	}
	var stopSignalErr error
	if stateStore != nil {
		ids, err := getGeneratingHistoryIDs(ctx, stateStore, conversationID)
		if err != nil {
			stopSignalErr = err
		}
		if len(ids) == 0 && historyID != "" {
			ids = append(ids, historyID)
		}
		external := map[string]struct{}{}
		if len(ids) > 0 {
			external, err = activeExternalChatHistoryIDs(ctx, db, userID, conversationID, ids)
			if err != nil {
				stopSignalErr = err
				ids = nil
			}
		}
		for _, id := range ids {
			if _, ok := external[id]; ok {
				continue
			}
			if status, err := getChatStatus(ctx, stateStore, conversationID, id); err == nil && status.Status == "generating" && strings.TrimSpace(status.RunID) != "" {
				if _, err := claimUserCancelDecision(ctx, stateStore, conversationID, id, status.RunID); err != nil && stopSignalErr == nil {
					stopSignalErr = err
				}
			}
			if err := setChatCancelSignal(ctx, stateStore, conversationID, id); err != nil && stopSignalErr == nil {
				stopSignalErr = err
			}
		}
	}
	if err := newExternalChatApplication(db).requestStop(ctx, userID, conversationID, historyID); err != nil {
		return err
	}
	workflow.StopActiveWorkflowSession(ctx, db, stateStore, conversationID)
	taskIDs, err := subagent.InterruptConversation(ctx, db, conversationID, reason)
	if err != nil {
		return err
	}
	for _, taskID := range taskIDs {
		event := subagent.TaskEvent{Type: "error", TaskID: taskID, Status: subagent.StatusInterrupted, Message: reason}
		_ = subagent.WriteStatus(ctx, stateStore, taskID, map[string]any{"status": subagent.StatusInterrupted, "summary": reason})
		_ = subagent.AppendStreamEvent(ctx, stateStore, taskID, event)
		subagent.PublishConversationTaskEvent(ctx, db, stateStore, event)
	}
	subagent.CancelRuns(taskIDs)
	notifyCtx, cancel := terminalWriteContext(ctx)
	go func() {
		defer cancel()
		if err := workflow.NotifyChatCancel(notifyCtx, conversationID); err != nil {
			log.Logger.Warn().Err(err).Str("conversation_id", conversationID).Msg("failed to notify Python chat cancellation")
		}
	}()
	return stopSignalErr
}
