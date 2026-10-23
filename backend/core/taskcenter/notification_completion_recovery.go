package taskcenter

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

// ReconcileOrdinaryNotifications finishes active tasks from durable terminal
// history/session state after an interrupted callback. It never executes work.
func ReconcileOrdinaryNotifications(ctx context.Context, db *gorm.DB, after string) (string, error) {
	var tasks []orm.TaskCenterTask
	if err := db.WithContext(ctx).Where("task_type IN ? AND status IN ? AND archived_at IS NULL AND id > ?",
		[]string{"background_chat", "workflow_run"}, []string{"running", "waiting"}, after).Order("id").Limit(100).Find(&tasks).Error; err != nil {
		return after, err
	}
	var recoveryErr error
	for _, task := range tasks {
		if task.WorkflowSessionID != nil && *task.WorkflowSessionID != "" {
			recoveryErr = errors.Join(recoveryErr, notificationTx(ctx, db, func(tx *gorm.DB) error {
				var session orm.WorkflowSession
				// Use the same session -> task lock order as accepted retries.
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", *task.WorkflowSessionID).First(&session).Error; err != nil {
					return err
				}
				if session.Status != "completed" && session.Status != "failed" {
					return nil
				}
				status := "succeeded"
				if session.Status == "failed" {
					status = "failed"
				}
				finishedAt, err := WorkflowNotificationCompletedAt(ctx, tx, session)
				if err != nil {
					return err
				}
				return updateTaskStatusAt(ctx, tx, task.ID, status, true, finishedAt)
			}))
			continue
		}
		if task.TaskType != "background_chat" {
			continue
		}
		status, finishedAt, err := ordinaryHistoryCompletion(ctx, db, task)
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
			continue
		}
		if status != "" {
			recoveryErr = errors.Join(recoveryErr, finalizeBackgroundTasks(ctx, db, task.ConversationID, task.ID, status, finishedAt))
		}
	}
	if len(tasks) == 100 {
		return tasks[len(tasks)-1].ID, recoveryErr
	}
	return "", recoveryErr
}

func ordinaryHistoryCompletion(ctx context.Context, db *gorm.DB, task orm.TaskCenterTask) (string, time.Time, error) {
	execution, scoped := readBackgroundExecution(task.ProgressJSON)
	if scoped {
		owner, err := backgroundOwner(ctx, db, task.ConversationID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", time.Time{}, nil
		}
		if err != nil {
			return "", time.Time{}, err
		}
		if owner != task.UserID || execution.Version != 1 {
			return "", time.Time{}, nil
		}
		if status, at := backgroundAggregate(execution); status != "" {
			return status, at, nil
		}
		for _, part := range execution.Parts {
			if err := checkpointBackgroundExecution(ctx, db, task.ConversationID, part.HistoryID, part.RunID); err != nil {
				return "", time.Time{}, err
			}
		}
		var current orm.TaskCenterTask
		if err := db.WithContext(ctx).Where("id = ?", task.ID).Take(&current).Error; err != nil {
			return "", time.Time{}, err
		}
		execution, _ = readBackgroundExecution(current.ProgressJSON)
		status, at := backgroundAggregate(execution)
		return status, at, nil
	}
	return legacyBackgroundHistoryCompletion(ctx, db, task)
}

// Old rows have no durable execution identity. This bounded, unique-group
// compatibility path is best effort; missing or regenerated origins cannot in
// general be reconstructed. Scoped admissions never use it.
func legacyBackgroundHistoryCompletion(ctx context.Context, db *gorm.DB, task orm.TaskCenterTask) (string, time.Time, error) {
	var next []orm.TaskCenterTask
	if err := db.WithContext(ctx).Where("conversation_id = ? AND task_type = 'background_chat' AND id <> ? AND created_at >= ?", task.ConversationID, task.ID, task.CreatedAt).
		Where("plugin_session_id IS NULL OR plugin_session_id = ''").Order("created_at, id").Limit(1).Find(&next).Error; err != nil {
		return "", time.Time{}, err
	}
	if len(next) > 0 && next[0].CreatedAt.Equal(task.CreatedAt) {
		return "", time.Time{}, nil
	}
	query := func(model any) *gorm.DB {
		q := db.WithContext(ctx).Model(model).Where("conversation_id = ? AND create_time >= ?", task.ConversationID, task.CreatedAt)
		if len(next) > 0 {
			q = q.Where("create_time < ?", next[0].CreatedAt)
		}
		return q
	}
	var single []orm.ChatHistory
	if err := query(&orm.ChatHistory{}).Find(&single).Error; err != nil {
		return "", time.Time{}, err
	}
	var dual []orm.MultiAnswersChatHistory
	if err := query(&orm.MultiAnswersChatHistory{}).Find(&dual).Error; err != nil {
		return "", time.Time{}, err
	}
	validAt := func(at time.Time) bool {
		return !at.Before(task.CreatedAt) && (len(next) == 0 || at.Before(next[0].CreatedAt))
	}
	if len(single) == 1 && len(dual) == 0 {
		h := single[0]
		at := NotificationHistoryCompletedAt(h.CreateTime, h.UpdateTime, h.Ext)
		if validAt(at) {
			return ordinaryTerminalStatus(h.RunStatus), at, nil
		}
	}
	if len(single) == 0 && len(dual) == 2 && dual[0].Seq == dual[1].Seq {
		execution := backgroundExecution{Version: 1}
		for _, h := range dual {
			at := NotificationHistoryCompletedAt(h.CreateTime, h.UpdateTime, h.Ext)
			if !validAt(at) {
				return "", time.Time{}, nil
			}
			execution.Parts = append(execution.Parts, BackgroundExecutionPart{HistoryID: h.ID, RunID: "legacy", Status: ordinaryTerminalStatus(h.RunStatus), CompletedAt: &at})
		}
		status, at := backgroundAggregate(execution)
		return status, at, nil
	}
	return "", time.Time{}, nil
}

func ordinaryTerminalStatus(status string) string {
	switch status {
	case "completed":
		return "succeeded"
	case "failed", "interrupted":
		return "failed"
	case "cancelled":
		return "canceled"
	default:
		return ""
	}
}
