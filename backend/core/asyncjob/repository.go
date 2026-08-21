package asyncjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

var ErrJobTypeRequired = errors.New("async job_type is required")

var reusableStatuses = []string{
	string(StatusPending),
	string(StatusRunning),
	string(StatusSucceeded),
}

// activeReusableStatuses is reusableStatuses minus succeeded: pending and
// running jobs are always deduplicated, but succeeded jobs are only reused
// unless the caller opts out (SkipSucceeded). One-click batches that must
// re-run on every invocation pass SkipSucceeded=true so a new job is created
// instead of replaying the old result.
var activeReusableStatuses = []string{
	string(StatusPending),
	string(StatusRunning),
}

// staleStatuses are terminal job states that occupy the unique
// (job_type, idempotency_key) index but can be reset and reused when the same
// job is enqueued again (e.g. retry an install after it failed).
var staleStatuses = []string{
	string(StatusFailed),
	string(StatusCanceled),
}

func Enqueue(ctx context.Context, db *gorm.DB, req EnqueueRequest) (*orm.AsyncJob, error) {
	req.JobType = strings.TrimSpace(req.JobType)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.JobType == "" {
		return nil, ErrJobTypeRequired
	}
	if req.MaxAttempts <= 0 {
		req.MaxAttempts = 1
	}

	now := time.Now().UTC()
	if req.RunAt.IsZero() {
		req.RunAt = now
	} else {
		req.RunAt = req.RunAt.UTC()
	}

	payload, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal async job payload: %w", err)
	}

	var created *orm.AsyncJob
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if req.IdempotencyKey != "" {
			existing, err := findReusableJob(ctx, tx, req.JobType, req.IdempotencyKey, true, req.SkipSucceeded)
			if err != nil {
				return err
			}
			if existing != nil {
				created = existing
				return nil
			}

			// SkipSucceeded retires a previously succeeded job so the history row
			// is kept but its idempotency key is released (the table has a unique
			// index on job_type+idempotency_key) and a fresh job can be created.
			if req.SkipSucceeded {
				retired, err := findSucceededJob(ctx, tx, req.JobType, req.IdempotencyKey, true)
				if err != nil {
					return err
				}
				if retired != nil {
					newKey := req.IdempotencyKey + ":done:" + retired.ID
					if err := tx.Model(&orm.AsyncJob{}).
						Where("id = ?", retired.ID).
						Update("idempotency_key", newKey).Error; err != nil {
						return err
					}
				}
			}

			// No active/reusable job: a stale failed/canceled row still holds
			// the unique index, so reset it in place instead of inserting a new
			// row (which would violate the unique constraint).
			stale, err := findStaleJob(ctx, tx, req.JobType, req.IdempotencyKey, true)
			if err != nil {
				return err
			}
			if stale != nil {
				if err := resetJobForReuse(tx, stale, req, payload, now); err != nil {
					return err
				}
				created = stale
				return nil
			}
		}

		row := &orm.AsyncJob{
			ID:             "job_" + common.GenerateID(),
			JobType:        req.JobType,
			Status:         string(StatusPending),
			ResourceType:   req.ResourceType,
			ResourceID:     req.ResourceID,
			IdempotencyKey: req.IdempotencyKey,
			PayloadJSON:    json.RawMessage(payload),
			MaxAttempts:    req.MaxAttempts,
			NextRunAt:      req.RunAt,
			CreateUserID:   req.CreateUserID,
			CreateUserName: req.CreateUserName,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		created = row
		return nil
	})
	if err != nil && req.IdempotencyKey != "" && isUniqueConflict(err) {
		existing, findErr := findReusableJob(ctx, db, req.JobType, req.IdempotencyKey, false, req.SkipSucceeded)
		if findErr == nil && existing != nil {
			return existing, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return created, nil
}

func Get(ctx context.Context, db *gorm.DB, id string) (*orm.AsyncJob, error) {
	var row orm.AsyncJob
	if err := db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func findReusableJob(ctx context.Context, db *gorm.DB, jobType, idempotencyKey string, lock, skipSucceeded bool) (*orm.AsyncJob, error) {
	statuses := reusableStatuses
	if skipSucceeded {
		statuses = activeReusableStatuses
	}
	q := db.WithContext(ctx).
		Where("job_type = ? AND idempotency_key = ? AND status IN ?", jobType, idempotencyKey, statuses).
		Order("created_at ASC")
	if lock {
		q = withUpdateLock(q)
	}

	var row orm.AsyncJob
	if err := q.First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// findSucceededJob looks up the previously succeeded job holding the given
// idempotency key so SkipSucceeded callers can retire it and free the key.
func findSucceededJob(ctx context.Context, db *gorm.DB, jobType, idempotencyKey string, lock bool) (*orm.AsyncJob, error) {
	q := db.WithContext(ctx).
		Where("job_type = ? AND idempotency_key = ? AND status = ?", jobType, idempotencyKey, string(StatusSucceeded)).
		Order("created_at ASC")
	if lock {
		q = withUpdateLock(q)
	}

	var row orm.AsyncJob
	if err := q.First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func findStaleJob(ctx context.Context, db *gorm.DB, jobType, idempotencyKey string, lock bool) (*orm.AsyncJob, error) {
	q := db.WithContext(ctx).
		Where("job_type = ? AND idempotency_key = ? AND status IN ?", jobType, idempotencyKey, staleStatuses).
		Order("created_at ASC")
	if lock {
		q = withUpdateLock(q)
	}

	var row orm.AsyncJob
	if err := q.First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// resetJobForReuse clears a terminal job row so the runner can pick it up again
// as a fresh pending job. The job id and created_at are preserved so callers
// polling the same job id keep working across retries.
func resetJobForReuse(db *gorm.DB, row *orm.AsyncJob, req EnqueueRequest, payload json.RawMessage, now time.Time) error {
	return db.Model(row).Updates(map[string]any{
		"status":             string(StatusPending),
		"resource_type":      req.ResourceType,
		"resource_id":        req.ResourceID,
		"payload_json":       json.RawMessage(payload),
		"max_attempts":       req.MaxAttempts,
		"next_run_at":        req.RunAt,
		"attempt_count":      0,
		"error_code":         "",
		"error_message":      "",
		"error_details_json": nil,
		"result_json":        nil,
		"progress_current":   0,
		"progress_total":     0,
		"locked_by":          "",
		"lock_until":         nil,
		"started_at":         nil,
		"finished_at":        nil,
		"heartbeat_at":       nil,
		"create_user_id":     req.CreateUserID,
		"create_user_name":   req.CreateUserName,
		"updated_at":         now,
	}).Error
}

func withUpdateLock(db *gorm.DB) *gorm.DB {
	switch db.Dialector.Name() {
	case "postgres", "mysql":
		return db.Clauses(clause.Locking{Strength: "UPDATE"})
	default:
		return db
	}
}

func withClaimLock(db *gorm.DB) *gorm.DB {
	switch db.Dialector.Name() {
	case "postgres", "mysql":
		return db.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
	default:
		return db
	}
}

func isUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "unique violation") ||
		strings.Contains(msg, "sqlstate 23505")
}
