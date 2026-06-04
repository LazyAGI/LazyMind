package asyncjob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/log"
)

const (
	defaultConcurrency  = 2
	defaultPollInterval = 2 * time.Second
	defaultLockTTL      = 10 * time.Minute
)

type Runner struct {
	db   *gorm.DB
	opts Options
	done chan struct{}
}

func Start(ctx context.Context, db *gorm.DB, opts Options) *Runner {
	r := newRunner(db, opts)
	go r.run(ctx)
	return r
}

func RecoverStaleJobs(ctx context.Context, db *gorm.DB, now time.Time) error {
	now = now.UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		commonValues := map[string]any{
			"locked_by":  "",
			"lock_until": nil,
			"updated_at": now,
		}

		pendingValues := map[string]any{
			"status":      string(StatusPending),
			"next_run_at": now,
		}
		for key, value := range commonValues {
			pendingValues[key] = value
		}
		if err := tx.Model(&orm.AsyncJob{}).
			Where("status = ? AND lock_until < ? AND attempt_count < max_attempts", StatusRunning, now).
			Updates(pendingValues).Error; err != nil {
			return err
		}

		failedValues := map[string]any{
			"status":        string(StatusFailed),
			"error_code":    ErrorCodeLockExpired,
			"error_message": "job lock expired",
			"finished_at":   now,
		}
		for key, value := range commonValues {
			failedValues[key] = value
		}
		return tx.Model(&orm.AsyncJob{}).
			Where("status = ? AND lock_until < ? AND attempt_count >= max_attempts", StatusRunning, now).
			Updates(failedValues).Error
	})
}

func newRunner(db *gorm.DB, opts Options) *Runner {
	opts = normalizeOptions(opts)
	return &Runner{
		db:   db,
		opts: opts,
		done: make(chan struct{}),
	}
}

func normalizeOptions(opts Options) Options {
	if opts.WorkerID == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "worker"
		}
		opts.WorkerID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.LockTTL <= 0 {
		opts.LockTTL = defaultLockTTL
	}
	return opts
}

func (r *Runner) run(ctx context.Context) {
	defer close(r.done)

	if err := RecoverStaleJobs(ctx, r.db, time.Now().UTC()); err != nil {
		log.Logger.Warn().Err(err).Msg("asyncjob: recover stale jobs failed")
	}

	var wg sync.WaitGroup
	for i := 0; i < r.opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.workerLoop(ctx)
		}()
	}
	wg.Wait()
}

func (r *Runner) Done() <-chan struct{} {
	return r.done
}

func (r *Runner) workerLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		ran, err := r.runOnce(ctx)
		if err != nil {
			log.Logger.Warn().Err(err).Msg("asyncjob: run job failed")
		}

		delay := time.Duration(0)
		if !ran {
			delay = r.opts.PollInterval
		}
		timer.Reset(delay)
	}
}

func (r *Runner) runOnce(ctx context.Context) (bool, error) {
	job, err := r.claimOne(ctx, time.Now().UTC())
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}
	return true, r.runJob(ctx, *job)
}

func (r *Runner) claimOne(ctx context.Context, now time.Time) (*orm.AsyncJob, error) {
	now = now.UTC()
	lockUntil := now.Add(r.opts.LockTTL)
	var claimed *orm.AsyncJob

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row orm.AsyncJob
		err := withClaimLock(tx).
			Where("status = ? AND next_run_at <= ?", StatusPending, now).
			Order("created_at ASC").
			First(&row).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		values := map[string]any{
			"status":        string(StatusRunning),
			"attempt_count": gorm.Expr("attempt_count + ?", 1),
			"locked_by":     r.opts.WorkerID,
			"lock_until":    lockUntil,
			"started_at":    gorm.Expr("COALESCE(started_at, ?)", now),
			"heartbeat_at":  now,
			"updated_at":    now,
		}
		result := tx.Model(&orm.AsyncJob{}).
			Where("id = ? AND status = ?", row.ID, StatusPending).
			Updates(values)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}

		row.Status = string(StatusRunning)
		row.AttemptCount++
		row.LockedBy = r.opts.WorkerID
		row.LockUntil = &lockUntil
		if row.StartedAt == nil {
			row.StartedAt = &now
		}
		row.HeartbeatAt = &now
		row.UpdatedAt = now
		claimed = &row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *Runner) runJob(ctx context.Context, row orm.AsyncJob) error {
	handler, ok := lookupHandler(row.JobType)
	if !ok {
		return r.markHandlerNotFound(ctx, row.ID)
	}

	reporter := &jobReporter{
		db:       r.db,
		jobID:    row.ID,
		workerID: r.opts.WorkerID,
		lockTTL:  r.opts.LockTTL,
	}
	result, err := handler(ctx, toJob(row), reporter)
	if err == nil {
		return r.markSucceeded(ctx, row.ID, result)
	}
	return r.markFailedAttempt(ctx, row, result, err)
}

func toJob(row orm.AsyncJob) Job {
	return Job{
		ID:             row.ID,
		JobType:        row.JobType,
		ResourceType:   row.ResourceType,
		ResourceID:     row.ResourceID,
		PayloadJSON:    row.PayloadJSON,
		AttemptCount:   row.AttemptCount,
		CreateUserID:   row.CreateUserID,
		CreateUserName: row.CreateUserName,
	}
}

func (r *Runner) markHandlerNotFound(ctx context.Context, jobID string) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&orm.AsyncJob{}).
		Where("id = ?", jobID).
		Updates(map[string]any{
			"status":        string(StatusFailed),
			"error_code":    ErrorCodeHandlerNotFound,
			"error_message": "async job handler not found",
			"locked_by":     "",
			"lock_until":    nil,
			"finished_at":   now,
			"updated_at":    now,
		}).Error
}

func (r *Runner) markSucceeded(ctx context.Context, jobID string, result Result) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing orm.AsyncJob
		if err := withUpdateLock(tx).
			Where("id = ?", jobID).
			First(&existing).Error; err != nil {
			return err
		}
		if existing.Status == string(StatusSucceeded) {
			return nil
		}
		return tx.Model(&orm.AsyncJob{}).
			Where("id = ?", jobID).
			Updates(map[string]any{
				"status":             string(StatusSucceeded),
				"result_json":        result.ResultJSON,
				"error_code":         "",
				"error_message":      "",
				"error_details_json": nil,
				"progress_current":   gorm.Expr("progress_total"),
				"locked_by":          "",
				"lock_until":         nil,
				"finished_at":        now,
				"updated_at":         now,
			}).Error
	})
}

func (r *Runner) markFailedAttempt(ctx context.Context, row orm.AsyncJob, result Result, handlerErr error) error {
	now := time.Now().UTC()
	errorCode := stringsOrDefault(result.ErrorCode, ErrorCodeHandlerFailed)
	errorMessage := handlerErr.Error()

	if row.AttemptCount < row.MaxAttempts {
		return r.db.WithContext(ctx).Model(&orm.AsyncJob{}).
			Where("id = ?", row.ID).
			Updates(map[string]any{
				"status":             string(StatusPending),
				"next_run_at":        now.Add(backoffForAttempt(row.AttemptCount)),
				"error_code":         errorCode,
				"error_message":      errorMessage,
				"error_details_json": result.ErrorDetailsJSON,
				"locked_by":          "",
				"lock_until":         nil,
				"updated_at":         now,
			}).Error
	}

	return r.db.WithContext(ctx).Model(&orm.AsyncJob{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{
			"status":             string(StatusFailed),
			"error_code":         errorCode,
			"error_message":      errorMessage,
			"error_details_json": result.ErrorDetailsJSON,
			"locked_by":          "",
			"lock_until":         nil,
			"finished_at":        now,
			"updated_at":         now,
		}).Error
}

func backoffForAttempt(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 10 * time.Second
	case attempt == 2:
		return 30 * time.Second
	default:
		return 60 * time.Second
	}
}

func stringsOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

type jobReporter struct {
	db       *gorm.DB
	jobID    string
	workerID string
	lockTTL  time.Duration
}

func (r *jobReporter) SetProgress(ctx context.Context, current, total int64) error {
	return r.db.WithContext(ctx).Model(&orm.AsyncJob{}).
		Where("id = ? AND status = ? AND locked_by = ?", r.jobID, StatusRunning, r.workerID).
		Updates(map[string]any{
			"progress_current": current,
			"progress_total":   total,
			"updated_at":       time.Now().UTC(),
		}).Error
}

func (r *jobReporter) Heartbeat(ctx context.Context) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&orm.AsyncJob{}).
		Where("id = ? AND status = ? AND locked_by = ?", r.jobID, StatusRunning, r.workerID).
		Updates(map[string]any{
			"heartbeat_at": now,
			"lock_until":   now.Add(r.lockTTL),
			"updated_at":   now,
		}).Error
}
