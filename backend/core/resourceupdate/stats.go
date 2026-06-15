package resourceupdate

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
)

func CountSkillReviewHistoryStats(ctx context.Context, db *gorm.DB, userID string, start, end time.Time) (HistoryStats, error) {
	var stats HistoryStats
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return stats, nil
	}
	err := db.WithContext(ctx).
		Table("chat_histories AS ch").
		Select(
			"COUNT(CASE WHEN TRIM(COALESCE(ch.raw_content, '')) <> '' OR TRIM(COALESCE(ch.content, '')) <> '' THEN 1 END) AS user_turn_count, "+
				"COALESCE(SUM(COALESCE(ch.tool_call_turns, 0)), 0) AS tool_call_count",
		).
		Joins("JOIN conversations AS c ON c.id = ch.conversation_id").
		Where("c.create_user_id = ? AND c.deleted_at IS NULL", userID).
		Where("ch.create_time >= ? AND ch.create_time < ?", start, end).
		Scan(&stats).Error
	return stats, err
}
