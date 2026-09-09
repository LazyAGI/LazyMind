package conversationgroup

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

// RecoverLegacyRuns runs after migrations, before this process starts job workers.
// Deployment must stop the old Core/Chat processes before starting the new pair.
func RecoverLegacyRuns(ctx context.Context, db *gorm.DB) error {
	var rows []orm.ConversationOrganizerRun
	if err := db.WithContext(ctx).Where("protocol_version<2 AND status IN ?", []string{"pending", "running", "applying"}).Find(&rows).Error; err != nil {
		return err
	}
	for _, run := range rows {
		if !settleOrganizerStream(ctx, run.StreamJSON) {
			return errCancellationUnconfirmed
		}
		if err := UserTransaction(ctx, db, run.UserID, func(tx *gorm.DB) error {
			var current orm.ConversationOrganizerRun
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", run.ID).Take(&current).Error; err != nil {
				return err
			}
			if current.ProtocolVersion >= 2 || current.Status == "succeeded" || current.Status == "confirmed" || current.Status == "undone" || current.Status == "canceled" {
				return nil
			}
			if string(current.StreamJSON) != string(run.StreamJSON) {
				return errCancellationUnconfirmed
			}
			now := time.Now().UTC()
			if err := tx.Model(&orm.AsyncJob{}).Where("id=? AND status IN ?", run.JobID, []string{"pending", "running"}).Updates(map[string]any{"status": "canceled", "lock_until": nil, "updated_at": now, "error_code": "organizer_protocol_upgraded"}).Error; err != nil {
				return err
			}
			return tx.Model(&current).Updates(map[string]any{"status": "canceled", "stage": "canceled", "finished_at": now, "updated_at": now, "error_code": "organizer_protocol_upgraded", "error_message": "版本已更新，请重新整理"}).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

var errOrganizerProtocol = errors.New("conversation organizer run cannot be retried after protocol upgrade; start a new run")
