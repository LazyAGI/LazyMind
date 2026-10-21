package chat

import (
	"context"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
	"lazymind/core/subagent"
	"lazymind/core/tasktiming"
)

func beginChatTiming(ctx context.Context, db *gorm.DB, convID, historyID, runID string) error {
	if !tasktiming.Available(db) {
		return nil
	}
	err := conversationCheckpoint(ctx, db, convID, func(tx *gorm.DB) error {
		var conversation orm.Conversation
		if err := tx.Where("id = ? AND deleted_at IS NULL", convID).Take(&conversation).Error; err != nil {
			return err
		}
		return tasktiming.BeginChat(ctx, tx, runID, historyID, convID, conversation.CreateUserID, tasktiming.RequestedAt(ctx))
	})
	if err == nil {
		subagent.EventHooks.CallConversationEvent(ctx, store.State(), convID, historyID, "task_run_updated", map[string]any{})
	}
	return err
}

// Covers single/dual and legacy/external provider completion paths. Stream
// closure alone never ends a bound Workflow, which owns its own terminal state.
func settleChatTiming(parent context.Context, db *gorm.DB, convID, historyID, runID string, dual bool) {
	if !tasktiming.Available(db) {
		return
	}
	ctx, cancel := terminalWriteContext(parent)
	defer cancel()
	err := conversationCheckpoint(ctx, db, convID, func(tx *gorm.DB) error {
		row := struct{ RunStatus string }{}
		query := tx.Table("chat_histories")
		if dual {
			query = tx.Table("multi_answers_chat_histories")
		}
		result := query.Select("run_status").Where("id = ? AND conversation_id = ? AND run_id = ?", historyID, convID, runID).Take(&row)
		if result.Error != nil && result.Error != gorm.ErrRecordNotFound {
			return result.Error
		}
		status := row.RunStatus
		if status == "" || status == "generating" {
			status = "failed"
		}
		return tasktiming.FinishChat(ctx, tx, runID, status, time.Now().UTC())
	})
	if err != nil {
		log.Logger.Warn().Err(err).Str("run_id", runID).Msg("finish task timing failed")
		return
	}
	subagent.EventHooks.CallConversationEvent(ctx, store.State(), convID, historyID, "task_run_updated", map[string]any{})
}
