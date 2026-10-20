package chat

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/log"
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
		if incoming, exists := values["ext"]; exists {
			// Decision handlers lock the history row too. Read after obtaining that
			// lock so terminal/progress writes cannot resurrect an answered card.
			if err := tx.Model(&orm.ChatHistory{}).Where("id = ? AND run_id = ?", historyID, runID).UpdateColumn("update_time", gorm.Expr("update_time")).Error; err != nil {
				return err
			}
			var current orm.ChatHistory
			if err := tx.Where("id = ?", historyID).Take(&current).Error; err != nil {
				return err
			}
			raw, err := json.Marshal(incoming)
			if err != nil {
				return err
			}
			var stored, next map[string]any
			if json.Unmarshal(current.Ext, &stored) == nil && json.Unmarshal(raw, &next) == nil && next != nil {
				pending, _ := stored["ask_pending"].(map[string]any)
				incomingPending, _ := next["ask_pending"].(map[string]any)
				if pending["knowledge_management"] != nil && (incomingPending == nil || incomingPending["knowledge_management"] != nil) {
					for _, key := range []string{"ask_pending", "ask_answered", "ask_saved_answers"} {
						delete(next, key)
						if value, ok := stored[key]; ok {
							next[key] = value
						}
					}
					merged, err := json.Marshal(next)
					if err != nil {
						return err
					}
					values["ext"] = json.RawMessage(merged)
				}
			}
		}
		result := tx.Model(&orm.ChatHistory{}).Where("id = ? AND run_id = ?", historyID, runID).Updates(values)
		affected = result.RowsAffected
		return result.Error
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
	result := db.WithContext(ctx).Model(&orm.MultiAnswersChatHistory{}).
		Where("id = ? AND run_id = ?", historyID, runID).Updates(values)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		log.Logger.Info().Str("history_id", historyID).Str("run_id", runID).
			Msg("ignored stale multi-answer history write")
		return false, nil
	}
	return true, nil
}
