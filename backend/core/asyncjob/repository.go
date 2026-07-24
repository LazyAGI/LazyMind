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

var ErrJobTypeRequired = errors.New("asyncjob: job_type is required")

var reusableStatuses = []string{
	string(StatusPending),
	string(StatusRunning),
	string(StatusSucceeded),
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
			existing, err := findReusableJob(ctx, tx, req.JobType, req.IdempotencyKey, true)
			if err != nil {
				return err
			}
			if existing != nil {
				created = existing
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
		existing, findErr := findReusableJob(ctx, db, req.JobType, req.IdempotencyKey, false)
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

func findReusableJob(ctx context.Context, db *gorm.DB, jobType, idempotencyKey string, lock bool) (*orm.AsyncJob, error) {
	q := db.WithContext(ctx).
		Where("job_type = ? AND idempotency_key = ? AND status IN ?", jobType, idempotencyKey, reusableStatuses).
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
