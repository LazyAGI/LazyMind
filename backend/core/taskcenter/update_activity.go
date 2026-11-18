package taskcenter

import (
	"context"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/displaystate"
	"time"
)

// Activity is a read-only execution projection, separate from retryable history.
func ActiveForDesktopUpdate(ctx context.Context, db *gorm.DB) (int64, error) {
	var tasks []orm.TaskCenterTask
	if err := db.WithContext(ctx).Where("status IN ? AND (scheduled_fire_at IS NULL OR scheduled_fire_at <= ?)", []string{"pending", "running", "waiting"}, time.Now()).Find(&tasks).Error; err != nil {
		return 0, err
	}
	var active int64
	for _, task := range tasks {
		if task.TaskType != "workflow_run" {
			active++
			continue
		}
		if task.WorkflowSessionID == nil {
			active++
			continue
		}
		var session orm.WorkflowSession
		if err := db.WithContext(ctx).Where("id = ?", *task.WorkflowSessionID).First(&session).Error; err != nil {
			return 0, err
		}
		view, err := displaystate.Read(db.WithContext(ctx), session)
		if err != nil {
			return 0, err
		}
		if view.Status != "paused" || view.Reason != "user_stopped" {
			active++
		}
	}
	return active, nil
}
