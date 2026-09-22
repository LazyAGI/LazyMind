package chat

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/state"
)

// RunningSettingsConversations uses the same authoritative state as Chat's running indicator.
// Unknown state is an error: it must never authorize a settings change as if idle.
func RunningSettingsConversations(ctx context.Context, db *gorm.DB, cache state.Store, user string) ([]orm.Conversation, error) {
	result := []orm.Conversation{}
	for offset := 0; ; offset += conversationStatusBatchLimit {
		var rows []orm.Conversation
		if err := db.WithContext(ctx).Select("id, display_name").Where("create_user_id = ? AND deleted_at IS NULL AND archived_at IS NULL AND is_ephemeral = ?", user, false).
			Order("id").Offset(offset).Limit(conversationStatusBatchLimit).Find(&rows).Error; err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		ids := make([]string, len(rows))
		byID := map[string]orm.Conversation{}
		for i, row := range rows {
			ids[i] = row.ID
			byID[row.ID] = row
		}
		statuses, err := batchConversationRunningStatus(ctx, db, cache, user, ids, time.Now())
		if err != nil {
			return nil, err
		}
		for _, status := range statuses {
			if status.Status == "unknown" {
				return nil, errors.New("task status unavailable")
			}
			if status.Status == "running" {
				result = append(result, byID[status.ConversationID])
			}
		}
	}
	return result, nil
}
