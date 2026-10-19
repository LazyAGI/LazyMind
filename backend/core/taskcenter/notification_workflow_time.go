package taskcenter

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// WorkflowNotificationCompletedAt reads the durable terminal transition for the
// current execution. Session metadata edits (dismiss/restore/current-step) must
// not move an old completion across a notification gate's delivery barrier.
func WorkflowNotificationCompletedAt(ctx context.Context, db *gorm.DB, session orm.WorkflowSession) (time.Time, error) {
	var latest orm.WorkflowSessionStep
	if err := db.WithContext(ctx).Select("created_at").Where("session_id = ? AND validity <> ?", session.ID, "stale").
		Order("created_at DESC").Limit(1).Find(&latest).Error; err != nil {
		return time.Time{}, err
	}
	var cursor int64
	for {
		var events []orm.WorkflowEvent
		query := db.WithContext(ctx).Where("session_id = ? AND event_type IN ? AND id > ?", session.ID, []string{"workflow.patch", "workflow.snapshot"}, cursor)
		if !latest.CreatedAt.IsZero() {
			query = query.Where("created_at >= ?", latest.CreatedAt)
		}
		if err := query.Order("id").Limit(100).Find(&events).Error; err != nil {
			return time.Time{}, err
		}
		for _, event := range events {
			var payload struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(event.PayloadJSON, &payload) == nil && payload.Status == session.Status {
				return event.CreatedAt, nil
			}
		}
		if len(events) < 100 {
			break
		}
		cursor = events[len(events)-1].ID
	}
	// Legacy rows without a durable transition cannot establish a newer finish
	// from mutable metadata. Prefer their immutable creation time conservatively.
	if !session.CreatedAt.IsZero() {
		return session.CreatedAt, nil
	}
	return session.UpdatedAt, nil
}
