package resourceupdate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/maintenance"
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
			Skill:               algo.ReviewSkill,
			Memory:              algo.ReviewMemory,
			PreferenceOrganizer: algo.OrganizePreference,
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

	for i := 0; i < w.cfg.WorkerBatchSize; i++ {
		tasks, err := w.claimPending(ctx, w.clock().UTC())
		if err != nil {
			return result, err
		}
		if len(tasks) == 0 {
			break
		}
		task := tasks[0]
		result.Claimed++
		outcome := w.withTaskLeaseHeartbeat(ctx, task, func(callCtx context.Context) taskOutcome { return w.dispatch(callCtx, task) })
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
	var candidates []orm.ResourceUpdateTask
	if err := w.db.WithContext(ctx).Select("id", "user_id").Where("status = ? AND locked_until <= ?", orm.ResourceUpdateTaskStatusRunning, now).Limit(w.cfg.WorkerBatchSize).Find(&candidates).Error; err != nil {
		return 0, err
	}
	recovered := 0
	for _, task := range candidates {
		err := maintenance.UserTransaction(ctx, w.db, task.UserID, func(tx *gorm.DB) error {
			update := tx.Model(&orm.ResourceUpdateTask{}).Where("id = ? AND status = ? AND locked_until <= ?", task.ID, orm.ResourceUpdateTaskStatusRunning, now).Updates(map[string]any{"status": orm.ResourceUpdateTaskStatusPending, "run_id": "", "locked_by": "", "locked_until": nil, "next_run_at": now, "updated_at": now})
			recovered += int(update.RowsAffected)
			return update.Error
		})
		if err != nil {
			return recovered, err
		}
	}
	return recovered, nil
}

// A pending lane winner reserves its lane even during backoff. Query only a
// bounded set of idle lane winners, then revalidate under the user lock.
func eligiblePending(tx *gorm.DB, now time.Time) *gorm.DB {
	return tx.Model(&orm.ResourceUpdateTask{}).Where("status = ? AND next_run_at <= ?", orm.ResourceUpdateTaskStatusPending, now).
		Where(`(lane_key = '' OR (NOT EXISTS (
 SELECT 1 FROM resource_update_tasks busy WHERE busy.lane_key = resource_update_tasks.lane_key AND busy.status = 'running'
 ) AND NOT EXISTS (
 SELECT 1 FROM resource_update_tasks ahead WHERE ahead.lane_key = resource_update_tasks.lane_key AND ahead.status = 'pending' AND ahead.id <> resource_update_tasks.id AND
 (ahead.lane_priority > resource_update_tasks.lane_priority OR (ahead.lane_priority = resource_update_tasks.lane_priority AND
 (ahead.lane_order_at < resource_update_tasks.lane_order_at OR (ahead.lane_order_at = resource_update_tasks.lane_order_at AND
 (ahead.created_at < resource_update_tasks.created_at OR (ahead.created_at = resource_update_tasks.created_at AND ahead.id < resource_update_tasks.id))))))
 )))`)
}

func (w *Worker) claimPending(ctx context.Context, now time.Time) ([]orm.ResourceUpdateTask, error) {
	var candidates []orm.ResourceUpdateTask
	if err := eligiblePending(w.db.WithContext(ctx), now).Select("id", "user_id").Order("CASE WHEN lane_key = '' THEN created_at ELSE lane_order_at END ASC, created_at ASC, id ASC").Limit(w.cfg.WorkerBatchSize).Find(&candidates).Error; err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		var claimed []orm.ResourceUpdateTask
		err := maintenance.UserTransaction(ctx, w.db, candidate.UserID, func(tx *gorm.DB) error {
			claimAt := w.clock().UTC()
			var task orm.ResourceUpdateTask
			err := withUpdateSkipLocked(eligiblePending(tx, claimAt)).Where("id = ?", candidate.ID).Take(&task).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			task.RunID = common.GenerateID()
			until := claimAt.Add(w.cfg.WorkerLockTTL)
			update := tx.Model(&orm.ResourceUpdateTask{}).Where("id = ? AND status = ?", task.ID, orm.ResourceUpdateTaskStatusPending).Updates(map[string]any{
				"status": orm.ResourceUpdateTaskStatusRunning, "run_id": task.RunID, "locked_by": w.workerID, "locked_until": until, "started_at": claimAt, "finished_at": nil,
				"attempt_count": gorm.Expr("attempt_count + 1"), "error_code": "", "error_message": "", "updated_at": claimAt,
			})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return nil
			}
			task.Status, task.LockedBy, task.LockedUntil, task.StartedAt = orm.ResourceUpdateTaskStatusRunning, w.workerID, &until, &claimAt
			task.AttemptCount++
			claimed = append(claimed, task)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(claimed) > 0 {
			return claimed, nil
		}
	}
	return nil, nil
}

// updateOwned never lets an old execution renew or acknowledge a newer run.
func (w *Worker) updateOwned(ctx context.Context, task orm.ResourceUpdateTask, updates map[string]any) error {
	return maintenance.UserTransaction(ctx, w.db, task.UserID, func(tx *gorm.DB) error {
		update := tx.Model(&orm.ResourceUpdateTask{}).Where("id = ? AND run_id = ? AND run_id <> '' AND status = ? AND locked_by = ? AND locked_until > ?", task.ID, task.RunID, orm.ResourceUpdateTaskStatusRunning, w.workerID, w.clock().UTC()).Updates(updates)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return maintenance.ErrLeaseLost
		}
		return nil
	})
}

func (w *Worker) dispatch(ctx context.Context, task orm.ResourceUpdateTask) taskOutcome {
	if task.TaskType == orm.ResourceUpdateTaskTypeAutoCommitSkillDraft {
		return w.handleAutoCommitSkillDraft(ctx, task)
	}
	if task.TaskType == orm.ResourceUpdateTaskTypeOrganizePreference &&
		task.ResourceType == orm.ResourceUpdateResourceTypeUserPreference {
		return w.handlePreferenceOrganizer(ctx, task)
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
	case orm.ResourceUpdateResourceTypeMemory:
		return w.handleMemoryReview(ctx, task)
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
		return w.updateOwned(ctx, task, map[string]any{
			"status":        orm.ResourceUpdateTaskStatusPending,
			"error_code":    outcome.ErrorCode,
			"error_message": outcome.ErrorMessage,
			"attempt_count": gorm.Expr("CASE WHEN attempt_count > 0 THEN attempt_count - 1 ELSE 0 END"),
			"next_run_at":   now.Add(retryAfter),
			"locked_by":     "",
			"locked_until":  nil,
			"started_at":    nil,
			"updated_at":    now,
		})
	}
	if outcome.Status == orm.ResourceUpdateTaskStatusDone || outcome.Status == orm.ResourceUpdateTaskStatusSkipped {
		updates := map[string]any{
			"status":        outcome.Status,
			"result_id":     outcome.ResultID,
			"result_json":   outcome.ResultJSON,
			"error_code":    "",
			"error_message": "",
			"locked_by":     "",
			"locked_until":  nil,
			"finished_at":   now,
			"updated_at":    now,
		}
		return w.updateOwned(ctx, task, updates)
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
		return w.updateOwned(ctx, task, map[string]any{
			"status":        orm.ResourceUpdateTaskStatusFailed,
			"result_json":   outcome.ResultJSON,
			"error_code":    outcome.ErrorCode,
			"error_message": outcome.ErrorMessage,
			"locked_by":     "",
			"locked_until":  nil,
			"finished_at":   now,
			"updated_at":    now,
		})
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
	return w.updateOwned(ctx, task, map[string]any{
		"status":        orm.ResourceUpdateTaskStatusPending,
		"result_json":   outcome.ResultJSON,
		"error_code":    outcome.ErrorCode,
		"error_message": outcome.ErrorMessage,
		"next_run_at":   nextRunAt,
		"locked_by":     "",
		"locked_until":  nil,
		"updated_at":    now,
	})
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

// withTaskLeaseHeartbeat keeps one claimed task, and therefore its lane, owned
// while a long synchronous downstream call is in progress.
func (w *Worker) withTaskLeaseHeartbeat(
	ctx context.Context,
	task orm.ResourceUpdateTask,
	call func(context.Context) taskOutcome,
) taskOutcome {
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	interval := w.cfg.WorkerLockTTL / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	done := make(chan struct{})
	leaseErrors := make(chan error, 1)
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-callCtx.Done():
				return
			case <-ticker.C:
				now := w.clock().UTC()
				err := w.updateOwned(callCtx, task, map[string]any{"locked_until": now.Add(w.cfg.WorkerLockTTL), "updated_at": now})
				if err != nil {
					leaseErrors <- err
					cancel()
					return
				}
			}
		}
	}()
	outcome := call(callCtx)
	cancel()
	<-done
	select {
	case err := <-leaseErrors:
		return retryableOutcome("task_lease_lost", err)
	default:
	}
	return outcome
}
