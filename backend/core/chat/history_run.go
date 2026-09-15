package chat

import (
	"context"
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
	return db.WithContext(ctx).Model(&orm.ChatHistory{}).Where("id = ?", historyID).Updates(map[string]any{
		"run_id": runID, "run_status": "generating", "run_terminal": nil, "update_time": time.Now(),
	}).Error
}

func updateOwnedChatHistory(ctx context.Context, db *gorm.DB, historyID, runID string, values map[string]any) (bool, error) {
	if db == nil || strings.TrimSpace(historyID) == "" || strings.TrimSpace(runID) == "" {
		return false, nil
	}
	result := db.WithContext(ctx).Model(&orm.ChatHistory{}).
		Where("id = ? AND run_id = ?", historyID, runID).Updates(values)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
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
