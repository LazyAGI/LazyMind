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
	// defaultFinalizeTimeout bounds the DB writes that record a job's outcome
	// (markSucceeded / markFailedAttempt / markHandlerNotFound). These run with
	// a context detached from the app ctx so that, when a SIGTERM cancels the
	// app ctx mid-job, the interrupted job can still be returned to a retryable
	// state and have its lease cleared — otherwise the row stays in `running`
	// with an unexpired lock_until until the next startup's RecoverStaleJobs.
	defaultFinalizeTimeout = 30 * time.Second
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
	return recoverStaleJobs(ctx, db, now, nil, nil)
}

func recoverStaleJobs(ctx context.Context, db *gorm.DB, now time.Time, jobTypes, excludeJobTypes []string) error {
	now = now.UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		staleJobs := func() *gorm.DB {
			query := tx.Model(&orm.AsyncJob{}).Where("status = ? AND lock_until < ?", StatusRunning, now)
			if len(jobTypes) > 0 {
				query = query.Where("job_type IN ?", jobTypes)
			}
			if len(excludeJobTypes) > 0 {
				query = query.Where("job_type NOT IN ?", excludeJobTypes)
			}
			return query
		}
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
		if err := staleJobs().
			Where("attempt_count < max_attempts").
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
		return staleJobs().
			Where("attempt_count >= max_attempts").
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

	if err := r.recoverStaleJobs(ctx, time.Now().UTC()); err != nil {
		log.Logger.Warn().Err(err).Msg("asyncjob: recover stale jobs failed")
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(r.opts.LockTTL / 2)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := r.recoverStaleJobs(ctx, now); err != nil {
					log.Logger.Warn().Err(err).Msg("asyncjob: recover stale jobs failed")
				}
			}
		}
	}()
	for i := 0; i < r.opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.workerLoop(ctx)
		}()
	}
	wg.Wait()
}

func (r *Runner) recoverStaleJobs(ctx context.Context, now time.Time) error {
	return recoverStaleJobs(ctx, r.db, now, r.opts.JobTypes, r.opts.ExcludeJobTypes)
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
		query := withClaimLock(tx)
		if len(r.opts.JobTypes) > 0 {
			query = query.Where("job_type IN ?", r.opts.JobTypes)
		}
		if len(r.opts.ExcludeJobTypes) > 0 {
			query = query.Where("job_type NOT IN ?", r.opts.ExcludeJobTypes)
		}
		if len(r.opts.YieldToJobTypes) > 0 {
			query = query.Where("NOT EXISTS (?)", tx.Model(&orm.AsyncJob{}).Select("1").Where("job_type IN ? AND status = ? AND next_run_at <= ?", r.opts.YieldToJobTypes, StatusPending, now))
		}
		if r.opts.SerializeResources {
			query = query.Where("NOT EXISTS (SELECT 1 FROM async_jobs active WHERE active.resource_type = async_jobs.resource_type AND active.resource_id = async_jobs.resource_id AND active.status = ?)", StatusRunning)
		}
		err := query.
			Where("status = ? AND next_run_at <= ?", StatusPending, now).
			Order("created_at ASC").
			First(&row).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		if r.opts.SerializeResources {
			if tx.Dialector.Name() == "postgres" {
				var acquired bool
				if err := tx.Raw("SELECT pg_try_advisory_xact_lock(hashtextextended(?, 0))", row.ResourceType+":"+row.ResourceID).Scan(&acquired).Error; err != nil {
					return err
				}
				if !acquired {
					return nil
				}
			}
			var active int64
			if err := tx.Model(&orm.AsyncJob{}).Where("resource_type = ? AND resource_id = ? AND status = ?", row.ResourceType, row.ResourceID, StatusRunning).Count(&active).Error; err != nil {
				return err
			}
			if active > 0 {
				return nil
			}
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
	reporter := &jobReporter{
		db:           r.db,
		jobID:        row.ID,
		workerID:     r.opts.WorkerID,
		attemptCount: row.AttemptCount,
		lockTTL:      r.opts.LockTTL,
	}
	var result Result
	var err error
	if ok {
		handlerCtx, cancelHandler := context.WithCancel(ctx)
		stopHeartbeat := make(chan struct{})
		heartbeatDone := make(chan struct{})
		go r.heartbeatLoop(handlerCtx, cancelHandler, reporter, stopHeartbeat, heartbeatDone)
		result, err = handler(handlerCtx, toJob(row), reporter)
		close(stopHeartbeat)
		<-heartbeatDone
		cancelHandler()
	}
	// Start the finalization deadline after the handler, including long model calls.
	finCtx, finCancel := context.WithTimeout(context.Background(), defaultFinalizeTimeout)
	defer finCancel()
	if !ok {
		return r.markHandlerNotFound(finCtx, row)
	}
	if err == nil {
		return r.markSucceeded(finCtx, row, result)
	}
	return r.markFailedAttempt(finCtx, row, result, err)
}

func (r *Runner) heartbeatLoop(ctx context.Context, cancelHandler context.CancelFunc, reporter *jobReporter, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	interval := r.opts.LockTTL / 3
	if interval <= 0 {
		interval = time.Nanosecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if err := reporter.Heartbeat(ctx); err != nil {
				if errors.Is(err, errJobLeaseLost) {
					cancelHandler()
					return
				}
				log.Logger.Warn().Err(err).Str("job_id", reporter.jobID).Msg("asyncjob: renew job lease failed")
			}
		}
	}
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

func (r *Runner) markHandlerNotFound(ctx context.Context, row orm.AsyncJob) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&orm.AsyncJob{}).
		Where("id = ? AND status = ? AND attempt_count = ? AND locked_by = ?", row.ID, StatusRunning, row.AttemptCount, row.LockedBy).
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

func (r *Runner) markSucceeded(ctx context.Context, row orm.AsyncJob, result Result) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Model(&orm.AsyncJob{}).
		Where("id = ? AND status = ? AND attempt_count = ? AND locked_by = ?", row.ID, StatusRunning, row.AttemptCount, row.LockedBy).
		Updates(map[string]any{"status": string(StatusSucceeded), "result_json": result.ResultJSON, "error_code": "", "error_message": "", "error_details_json": nil, "progress_current": gorm.Expr("progress_total"), "locked_by": "", "lock_until": nil, "finished_at": now, "updated_at": now}).Error
}

func (r *Runner) markFailedAttempt(ctx context.Context, row orm.AsyncJob, result Result, handlerErr error) error {
	now := time.Now().UTC()
	errorCode := stringsOrDefault(result.ErrorCode, ErrorCodeHandlerFailed)
	errorMessage := handlerErr.Error()

	if !result.Permanent && row.AttemptCount < row.MaxAttempts {
		return r.db.WithContext(ctx).Model(&orm.AsyncJob{}).
			Where("id = ? AND status = ? AND attempt_count = ? AND locked_by = ?", row.ID, StatusRunning, row.AttemptCount, row.LockedBy).
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
		Where("id = ? AND status = ? AND attempt_count = ? AND locked_by = ?", row.ID, StatusRunning, row.AttemptCount, row.LockedBy).
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
	db           *gorm.DB
	jobID        string
	workerID     string
	attemptCount int
	lockTTL      time.Duration
}

var errJobLeaseLost = errors.New("async job lease lost")

func (r *jobReporter) SetProgress(ctx context.Context, current, total int64) error {
	result := r.db.WithContext(ctx).Model(&orm.AsyncJob{}).
		Where("id = ? AND status = ? AND locked_by = ? AND attempt_count = ?", r.jobID, StatusRunning, r.workerID, r.attemptCount).
		Updates(map[string]any{
			"progress_current": current,
			"progress_total":   total,
			"updated_at":       time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errJobLeaseLost
	}
	return nil
}

func (r *jobReporter) Heartbeat(ctx context.Context) error {
	now := time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&orm.AsyncJob{}).
		Where("id = ? AND status = ? AND locked_by = ? AND attempt_count = ?", r.jobID, StatusRunning, r.workerID, r.attemptCount).
		Updates(map[string]any{
			"heartbeat_at": now,
			"lock_until":   now.Add(r.lockTTL),
			"updated_at":   now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errJobLeaseLost
	}
	return nil
}
