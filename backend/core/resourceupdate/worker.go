package resourceupdate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/state"
)

type Worker struct {
	db            *gorm.DB
	cfg           Config
	workerID      string
	clock         clockFunc
	loadLLMConfig func(context.Context, *gorm.DB, string) (map[string]any, error)
	callers       reviewCallers
	stateStore    state.Store
}

func NewWorker(db *gorm.DB, cfg Config, workerID string, stateStores ...state.Store) *Worker {
	cfg = normalizeConfig(cfg)
	if strings.TrimSpace(workerID) == "" {
		workerID = defaultWorkerID("resourceupdate-worker")
	}
	var stateStore state.Store
	if len(stateStores) > 0 {
		stateStore = stateStores[0]
	}
	return &Worker{
		db:            db,
		cfg:           cfg,
		workerID:      workerID,
		clock:         time.Now,
		loadLLMConfig: modelconfig.LoadLLMConfig,
		callers: reviewCallers{
			Skill:  algo.ReviewSkill,
			Memory: algo.ReviewMemory,
		},
		stateStore: stateStore,
	}
}

func (w *Worker) RunOnce(ctx context.Context) (WorkerRunResult, error) {
	var result WorkerRunResult
	if w == nil || w.db == nil {
		return result, errors.New("resource update worker db is nil")
	}
	now := w.clock().UTC()
	recovered, err := w.recoverExpiredRunning(ctx, now)
	if err != nil {
		return result, err
	}
	result.Recovered = recovered
	if recovered > 0 {
		resourceUpdateWarn(logEventWorkerRecovered, nil).
			Int("recovered", recovered).
			Str("worker_id", w.workerID).
			Msg(logEventWorkerRecovered)
	}

	tasks, err := w.claimPending(ctx, now)
	if err != nil {
		return result, err
	}
	result.Claimed = len(tasks)
	if len(tasks) > 0 {
		resourceUpdateInfo(logEventWorkerClaimed).
			Int("claimed", len(tasks)).
			Int("recovered", recovered).
			Str("worker_id", w.workerID).
			Msg(logEventWorkerClaimed)
	}
	for _, task := range tasks {
		outcome := w.dispatch(ctx, task)
		if err := w.finishTask(ctx, task, outcome); err != nil {
			return result, err
		}
		if outcome.Deferred {
			result.Retried++
			continue
		}
		switch outcome.Status {
		case orm.ResourceUpdateTaskStatusDone:
			logWorkerFinishedTask(task, outcome)
			result.Done++
		case orm.ResourceUpdateTaskStatusSkipped:
			logWorkerFinishedTask(task, outcome)
			result.Skipped++
		default:
			if outcome.Permanent || task.AttemptCount >= w.cfg.MaxAttempts {
				result.Failed++
			} else {
				result.Retried++
			}
		}
	}
	return result, nil
}

func logWorkerFinishedTask(task orm.ResourceUpdateTask, outcome taskOutcome) {
	resourceUpdateInfo(logEventWorkerFinished).
		Str("task_id", task.ID).
		Str("task_type", task.TaskType).
		Str("resource_type", task.ResourceType).
		Str("resource_id", task.ResourceID).
		Str("user_id", task.UserID).
		Str("trigger_type", task.TriggerType).
		Str("trigger_id", task.TriggerID).
		Str("status", outcome.Status).
		Str("result_id", outcome.ResultID).
		Str("error_code", outcome.ErrorCode).
		Str("error_message", outcome.ErrorMessage).
		Bool("permanent", outcome.Permanent).
		Int("attempt_count", task.AttemptCount).
		Msg(logEventWorkerFinished)
}

func (w *Worker) recoverExpiredRunning(ctx context.Context, now time.Time) (int, error) {
	tx := w.db.WithContext(ctx).
		Model(&orm.ResourceUpdateTask{}).
		Where("status = ? AND locked_until IS NOT NULL AND locked_until < ?", orm.ResourceUpdateTaskStatusRunning, now).
		Updates(map[string]any{
			"status":       orm.ResourceUpdateTaskStatusPending,
			"locked_by":    "",
			"locked_until": nil,
			"next_run_at":  now,
			"updated_at":   now,
		})
	return int(tx.RowsAffected), tx.Error
}

func (w *Worker) claimPending(ctx context.Context, now time.Time) ([]orm.ResourceUpdateTask, error) {
	var claimed []orm.ResourceUpdateTask
	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := withUpdateLock(tx.Model(&orm.ResourceUpdateTask{})).
			Where("status = ? AND next_run_at <= ?", orm.ResourceUpdateTaskStatusPending, now).
			Order("created_at ASC").
			Limit(w.cfg.WorkerBatchSize).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		lockUntil := now.Add(w.cfg.WorkerLockTTL)
		if err := tx.Model(&orm.ResourceUpdateTask{}).
			Where("id IN ? AND status = ?", ids, orm.ResourceUpdateTaskStatusPending).
			Updates(map[string]any{
				"status":        orm.ResourceUpdateTaskStatusRunning,
				"locked_by":     w.workerID,
				"locked_until":  lockUntil,
				"started_at":    now,
				"attempt_count": gorm.Expr("attempt_count + ?", 1),
				"error_code":    "",
				"error_message": "",
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
		return tx.Where("id IN ? AND status = ? AND locked_by = ?", ids, orm.ResourceUpdateTaskStatusRunning, w.workerID).
			Order("created_at ASC").
			Find(&claimed).Error
	})
	return claimed, err
}

func (w *Worker) dispatch(ctx context.Context, task orm.ResourceUpdateTask) taskOutcome {
	if task.TaskType == orm.ResourceUpdateTaskTypeAutoCommitSkillDraft {
		return w.handleAutoCommitSkillDraft(ctx, task)
	}
	if task.TaskType == orm.ResourceUpdateTaskTypeAutoApplyReview {
		return w.handleAutoApplyReview(ctx, task)
	}
	if task.TaskType != orm.ResourceUpdateTaskTypeGenerateReview {
		return taskOutcome{
			Status:       orm.ResourceUpdateTaskStatusFailed,
			ErrorCode:    "unsupported_task_type",
			ErrorMessage: task.TaskType,
			Permanent:    true,
		}
	}
	switch task.ResourceType {
	case orm.ResourceUpdateResourceTypeSkill:
		return w.handleSkillGenerate(ctx, task)
	case orm.ResourceUpdateResourceTypeMemory, orm.ResourceUpdateResourceTypeUserPreference:
		return w.handleMemoryGenerate(ctx, task)
	default:
		return taskOutcome{
			Status:       orm.ResourceUpdateTaskStatusFailed,
			ErrorCode:    "unsupported_resource_type",
			ErrorMessage: task.ResourceType,
			Permanent:    true,
		}
	}
}

func (w *Worker) finishTask(ctx context.Context, task orm.ResourceUpdateTask, outcome taskOutcome) error {
	now := w.clock().UTC()
	if outcome.Deferred {
		retryAfter := outcome.RetryAfter
		if retryAfter <= 0 {
			retryAfter = time.Minute
		}
		return w.db.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).
			Where("id = ? AND status = ? AND locked_by = ?", task.ID, orm.ResourceUpdateTaskStatusRunning, w.workerID).
			Updates(map[string]any{
				"status":        orm.ResourceUpdateTaskStatusPending,
				"error_code":    outcome.ErrorCode,
				"error_message": outcome.ErrorMessage,
				"attempt_count": gorm.Expr("CASE WHEN attempt_count > 0 THEN attempt_count - 1 ELSE 0 END"),
				"next_run_at":   now.Add(retryAfter),
				"locked_by":     "",
				"locked_until":  nil,
				"started_at":    nil,
				"updated_at":    now,
			}).Error
	}
	if outcome.Status == orm.ResourceUpdateTaskStatusDone || outcome.Status == orm.ResourceUpdateTaskStatusSkipped {
		updates := map[string]any{
			"status":        outcome.Status,
			"result_id":     outcome.ResultID,
			"error_code":    "",
			"error_message": "",
			"locked_by":     "",
			"locked_until":  nil,
			"finished_at":   now,
			"updated_at":    now,
		}
		return w.db.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).
			Where("id = ? AND status = ? AND locked_by = ?", task.ID, orm.ResourceUpdateTaskStatusRunning, w.workerID).
			Updates(updates).Error
	}

	if outcome.ErrorCode == "" {
		outcome.ErrorCode = "handler_error"
	}
	if strings.TrimSpace(outcome.ErrorMessage) == "" {
		outcome.ErrorMessage = outcome.ErrorCode
	}
	if outcome.Permanent || task.AttemptCount >= w.cfg.MaxAttempts {
		resourceUpdateWarn(logEventWorkerFinished, nil).
			Str("task_id", task.ID).
			Str("task_type", task.TaskType).
			Str("resource_type", task.ResourceType).
			Str("resource_id", task.ResourceID).
			Str("user_id", task.UserID).
			Str("trigger_id", task.TriggerID).
			Str("status", orm.ResourceUpdateTaskStatusFailed).
			Str("error_code", outcome.ErrorCode).
			Str("error_message", outcome.ErrorMessage).
			Int("attempt_count", task.AttemptCount).
			Int("max_attempts", w.cfg.MaxAttempts).
			Msg(logEventWorkerFinished)
		return w.db.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).
			Where("id = ? AND status = ? AND locked_by = ?", task.ID, orm.ResourceUpdateTaskStatusRunning, w.workerID).
			Updates(map[string]any{
				"status":        orm.ResourceUpdateTaskStatusFailed,
				"error_code":    outcome.ErrorCode,
				"error_message": outcome.ErrorMessage,
				"locked_by":     "",
				"locked_until":  nil,
				"finished_at":   now,
				"updated_at":    now,
			}).Error
	}
	nextRunAt := now.Add(w.retryBackoff(task.AttemptCount))
	resourceUpdateWarn(logEventWorkerFinished, nil).
		Str("task_id", task.ID).
		Str("task_type", task.TaskType).
		Str("resource_type", task.ResourceType).
		Str("resource_id", task.ResourceID).
		Str("user_id", task.UserID).
		Str("trigger_id", task.TriggerID).
		Str("status", orm.ResourceUpdateTaskStatusPending).
		Str("error_code", outcome.ErrorCode).
		Str("error_message", outcome.ErrorMessage).
		Int("attempt_count", task.AttemptCount).
		Time("next_run_at", nextRunAt).
		Msg(logEventWorkerFinished)
	return w.db.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).
		Where("id = ? AND status = ? AND locked_by = ?", task.ID, orm.ResourceUpdateTaskStatusRunning, w.workerID).
		Updates(map[string]any{
			"status":        orm.ResourceUpdateTaskStatusPending,
			"error_code":    outcome.ErrorCode,
			"error_message": outcome.ErrorMessage,
			"next_run_at":   nextRunAt,
			"locked_by":     "",
			"locked_until":  nil,
			"updated_at":    now,
		}).Error
}

func (w *Worker) retryBackoff(attemptCount int) time.Duration {
	if attemptCount < 1 {
		attemptCount = 1
	}
	backoff := w.cfg.RetryBackoffBase
	for i := 1; i < attemptCount; i++ {
		backoff *= 2
		if backoff >= w.cfg.RetryBackoffMax {
			return w.cfg.RetryBackoffMax
		}
	}
	if backoff > w.cfg.RetryBackoffMax {
		return w.cfg.RetryBackoffMax
	}
	return backoff
}

func retryableOutcome(code string, err error) taskOutcome {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return taskOutcome{
		Status:       orm.ResourceUpdateTaskStatusPending,
		ErrorCode:    code,
		ErrorMessage: message,
	}
}

func deferredOutcome(code, message string, retryAfter time.Duration) taskOutcome {
	return taskOutcome{
		Status:       orm.ResourceUpdateTaskStatusPending,
		ErrorCode:    code,
		ErrorMessage: message,
		Deferred:     true,
		RetryAfter:   retryAfter,
	}
}

func permanentOutcome(code, message string) taskOutcome {
	return taskOutcome{
		Status:       orm.ResourceUpdateTaskStatusFailed,
		ErrorCode:    code,
		ErrorMessage: message,
		Permanent:    true,
	}
}

func defaultWorkerID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
