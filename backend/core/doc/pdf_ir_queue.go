package doc

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/readingpolicy"
	"strings"
	"time"
)

// A user action may promote a quota-deferred automatic job, but never steals an
// actual running/queued parse. The same identity keeps concurrent clicks deduped.
func enqueuePDFIR(ctx context.Context, db *gorm.DB, req asyncjob.EnqueueRequest, automatic bool) (*orm.AsyncJob, error) {
	if automatic {
		return enqueueAutomaticPDFIR(ctx, db, req)
	}
	job, err := asyncjob.Enqueue(ctx, db, req)
	if err != nil || job.Status != "pending" || job.ErrorCode != readingpolicy.QuotaErrorCode {
		return job, err
	}
	payload, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, err
	}
	err = db.WithContext(ctx).Model(&orm.AsyncJob{}).Where("id=? AND status=? AND error_code=?", job.ID, "pending", readingpolicy.QuotaErrorCode).Updates(map[string]any{
		"payload_json": json.RawMessage(payload), "create_user_id": req.CreateUserID, "next_run_at": time.Now().UTC(), "error_code": "", "error_message": "", "updated_at": time.Now().UTC(),
	}).Error
	if err != nil {
		return nil, err
	}
	var current orm.AsyncJob
	err = db.WithContext(ctx).Where("id=?", job.ID).Take(&current).Error
	return &current, err
}

// Lock existing keys before deciding whether to reuse them. For absent keys,
// conflict-ignore insertion prevents generic Enqueue from reviving a failed
// row inserted by another caller. SQLite busy retries rerun the transaction.
func enqueueAutomaticPDFIR(ctx context.Context, db *gorm.DB, req asyncjob.EnqueueRequest) (*orm.AsyncJob, error) {
	req.JobType = strings.TrimSpace(req.JobType)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	req.MaxAttempts = 1
	if req.JobType == "" {
		return nil, asyncjob.ErrJobTypeRequired
	}
	if req.IdempotencyKey == "" {
		return asyncjob.Enqueue(ctx, db, req)
	}
	payload, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if req.RunAt.IsZero() {
		req.RunAt = now
	}
	var job *orm.AsyncJob
	err = common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		candidate := orm.AsyncJob{ID: "job_" + common.GenerateID(), JobType: req.JobType, IdempotencyKey: req.IdempotencyKey, Status: "pending", ResourceType: req.ResourceType, ResourceID: req.ResourceID, PayloadJSON: json.RawMessage(payload), MaxAttempts: 1, NextRunAt: req.RunAt.UTC(), CreateUserID: req.CreateUserID, CreateUserName: req.CreateUserName, CreatedAt: now, UpdatedAt: now}

		q := tx.Where("job_type=? AND idempotency_key=?", req.JobType, req.IdempotencyKey)
		if tx.Dialector.Name() != "sqlite" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		var current orm.AsyncJob
		if err := q.Session(&gorm.Session{}).Take(&current).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// An absent key cannot be row-locked. A concurrent insert wins the unique
			// index; never pass that race through generic Enqueue's stale-row reset.
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
				return err
			}
			if err := q.Session(&gorm.Session{}).Take(&current).Error; err != nil {
				return err
			}
		}
		job = &current
		// Missing/obsolete succeeded artifacts may still need regeneration. Keep
		// the existing SkipSucceeded behavior, with the row locked until enqueue.
		if current.Status == "succeeded" && req.SkipSucceeded {
			var err error
			job, err = asyncjob.EnqueueInTransaction(ctx, tx, req)
			return err
		}
		return nil
	})
	return job, err
}
