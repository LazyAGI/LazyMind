package chat

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/taskcenter"
)

// claimChatHistoryRun transfers a reused history row to a new run before that
// run can emit progress. Terminal/progress writers must then use the matching
// run_id guard so a late previous run cannot overwrite the current owner.
func claimChatHistoryRun(ctx context.Context, db *gorm.DB, historyID, runID string) error {
	if db == nil || strings.TrimSpace(historyID) == "" || strings.TrimSpace(runID) == "" {
		return nil
	}
	var history orm.ChatHistory
	if err := db.WithContext(ctx).Select("conversation_id").Where("id = ?", historyID).Take(&history).Error; err != nil {
		return err
	}
	return conversationCheckpoint(ctx, db, history.ConversationID, func(tx *gorm.DB) error {
		var previous orm.ChatHistory
		if err := tx.Where("id = ?", historyID).Take(&previous).Error; err != nil {
			return err
		}
		if err := taskcenter.CaptureBackgroundExecution(ctx, tx, previous.ConversationID, previous.ID, previous.RunID); err != nil {
			return err
		}
		now := time.Now()
		return tx.Model(&orm.ChatHistory{}).Where("id = ?", historyID).Updates(map[string]any{
			"run_id": runID, "run_status": "generating", "run_terminal": nil,
			"create_time": now, "update_time": now,
		}).Error
	})
}

func updateOwnedChatHistory(ctx context.Context, db *gorm.DB, historyID, runID string, values map[string]any) (bool, error) {
	if db == nil || strings.TrimSpace(historyID) == "" || strings.TrimSpace(runID) == "" {
		return false, nil
	}
	var history orm.ChatHistory
	if err := db.WithContext(ctx).Select("conversation_id").Where("id = ?", historyID).Take(&history).Error; err != nil {
		return false, err
	}
	var affected int64
	err := conversationCheckpoint(ctx, db, history.ConversationID, func(tx *gorm.DB) error {
		var current orm.ChatHistory
		if err := tx.Where("id = ? AND run_id = ?", historyID, runID).Take(&current).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		preserveOwnedTerminalCompletion(values, current.Ext, current.TimeMixin, current.RunStatus, runID)
		result := tx.Model(&orm.ChatHistory{}).Where("id = ? AND run_id = ?", historyID, runID).Updates(values)
		affected = result.RowsAffected
		if result.Error != nil || affected == 0 {
			return result.Error
		}
		return taskcenter.CaptureBackgroundExecution(ctx, tx, history.ConversationID, historyID, runID)
	})
	if err != nil {
		return false, err
	}
	if affected == 0 {
		log.Logger.Info().Str("history_id", historyID).Str("run_id", runID).
			Msg("ignored stale chat history write")
		return false, nil
	}
	return true, nil
}

func updateOwnedMultiAnswerHistory(ctx context.Context, db *gorm.DB, historyID, runID string, values any) (bool, error) {
	if db == nil || strings.TrimSpace(historyID) == "" || strings.TrimSpace(runID) == "" {
		return false, nil
	}
	var history orm.MultiAnswersChatHistory
	if err := db.WithContext(ctx).Select("conversation_id").Where("id = ?", historyID).Take(&history).Error; err != nil {
		return false, err
	}
	var affected int64
	err := conversationCheckpoint(ctx, db, history.ConversationID, func(tx *gorm.DB) error {
		var current orm.MultiAnswersChatHistory
		if err := tx.Where("id = ? AND run_id = ?", historyID, runID).Take(&current).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		switch update := values.(type) {
		case map[string]any:
			preserveOwnedTerminalCompletion(update, current.Ext, current.TimeMixin, current.RunStatus, runID)
		case *orm.MultiAnswersChatHistory:
			if terminalHistoryStatus(update.RunStatus) {
				at := update.UpdateTime
				if terminalHistoryStatus(current.RunStatus) {
					at = taskcenter.NotificationHistoryCompletedAt(current.CreateTime, current.UpdateTime, current.Ext)
				}
				update.Ext = taskcenter.WithNotificationCompletionTimeForRun(update.Ext, at, runID)
			}
		}
		result := tx.Model(&orm.MultiAnswersChatHistory{}).Where("id = ? AND run_id = ?", historyID, runID).Updates(values)
		affected = result.RowsAffected
		if result.Error != nil || affected == 0 {
			return result.Error
		}
		return taskcenter.CaptureBackgroundExecution(ctx, tx, history.ConversationID, historyID, runID)
	})
	if err != nil {
		return false, err
	}
	if affected == 0 {
		log.Logger.Info().Str("history_id", historyID).Str("run_id", runID).
			Msg("ignored stale multi-answer history write")
		return false, nil
	}
	return true, nil
}

func terminalHistoryStatus(status string) bool {
	switch status {
	case "completed", "failed", "interrupted", "cancelled":
		return true
	default:
		return false
	}
}

// Called only after the history's run ownership is checked under the writer
// checkpoint. A replay of the same terminal keeps its first completion time.
func preserveOwnedTerminalCompletion(values map[string]any, ext json.RawMessage, times orm.TimeMixin, currentStatus, runID string) {
	status, _ := values["run_status"].(string)
	if !terminalHistoryStatus(status) {
		return
	}
	at, _ := values["update_time"].(time.Time)
	if terminalHistoryStatus(currentStatus) {
		at = taskcenter.NotificationHistoryCompletedAt(times.CreateTime, times.UpdateTime, ext)
	}
	if next, ok := values["ext"].(json.RawMessage); ok {
		ext = next
	}
	values["ext"] = taskcenter.WithNotificationCompletionTimeForRun(ext, at, runID)
}

// Fresh terminal creates share the same history/checkpoint transaction as owned
// terminal updates; notification projection happens only after this commits.
func createTerminalChatHistory(ctx context.Context, db *gorm.DB, history *orm.ChatHistory) error {
	return conversationCheckpoint(ctx, db, history.ConversationID, func(tx *gorm.DB) error {
		if err := tx.Create(history).Error; err != nil {
			return err
		}
		return taskcenter.CaptureBackgroundExecution(ctx, tx, history.ConversationID, history.ID, history.RunID)
	})
}
func createTerminalMultiAnswerHistory(ctx context.Context, db *gorm.DB, history *orm.MultiAnswersChatHistory) error {
	return conversationCheckpoint(ctx, db, history.ConversationID, func(tx *gorm.DB) error {
		if err := tx.Create(history).Error; err != nil {
			return err
		}
		return taskcenter.CaptureBackgroundExecution(ctx, tx, history.ConversationID, history.ID, history.RunID)
	})
}
