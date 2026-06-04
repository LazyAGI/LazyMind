package evalset

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/log"
)

var terminalImportJobStatuses = []string{
	string(asyncjob.StatusSucceeded),
	string(asyncjob.StatusFailed),
	string(asyncjob.StatusCanceled),
}

func StartImportPreviewCleanup(ctx context.Context, db *gorm.DB, interval time.Duration) {
	if db == nil {
		log.Logger.Warn().Msg("evalset import cleanup skipped: db is nil")
		return
	}
	if interval <= 0 {
		interval = defaultImportCleanupInterval
	}
	go importPreviewCleanupLoop(ctx, db, interval)
}

func importPreviewCleanupLoop(ctx context.Context, db *gorm.DB, interval time.Duration) {
	runImportPreviewCleanup(ctx, db, time.Now().UTC())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			runImportPreviewCleanup(ctx, db, now.UTC())
		}
	}
}

func runImportPreviewCleanup(ctx context.Context, db *gorm.DB, now time.Time) {
	if err := CleanupExpiredImportPreviews(ctx, db, now); err != nil {
		log.Logger.Warn().Err(err).Msg("evalset import cleanup: expire previews failed")
	}
	if err := CleanupConsumedImportPreviews(ctx, db, now); err != nil {
		log.Logger.Warn().Err(err).Msg("evalset import cleanup: remove consumed previews failed")
	}
	if err := CleanupTerminalImportJobs(ctx, db, now, LoadImportRuntimeConfigFromEnv().TaskRetention); err != nil {
		log.Logger.Warn().Err(err).Msg("evalset import cleanup: remove terminal jobs failed")
	}
}

func CleanupConsumedImportPreviews(ctx context.Context, db *gorm.DB, now time.Time) error {
	cutoff := now.UTC().Add(-24 * time.Hour)
	var previews []orm.EvalSetImportPreview
	if err := db.WithContext(ctx).
		Where("status = ? AND created_at < ?", importPreviewStatusConsumed, cutoff).
		Find(&previews).Error; err != nil {
		return err
	}

	for _, preview := range previews {
		active, err := hasActiveImportJobReference(ctx, db, preview.Token, preview.TempPath)
		if err != nil {
			return err
		}
		if active {
			continue
		}
		if strings.TrimSpace(preview.TempPath) != "" {
			if err := os.Remove(preview.TempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := os.Remove(invalidRowsCSVPathForImportToken(preview.Token)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := db.WithContext(ctx).
			Where("token = ? AND status = ?", preview.Token, importPreviewStatusConsumed).
			Delete(&orm.EvalSetImportPreview{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func CleanupTerminalImportJobs(ctx context.Context, db *gorm.DB, now time.Time, retention time.Duration) error {
	if retention <= 0 {
		retention = defaultImportTaskRetention
	}
	cutoff := now.UTC().Add(-retention)
	return db.WithContext(ctx).
		Where("job_type = ? AND status IN ? AND finished_at < ?", importJobType, terminalImportJobStatuses, cutoff).
		Delete(&orm.AsyncJob{}).Error
}

func hasActiveImportJobReference(ctx context.Context, db *gorm.DB, importToken, tempPath string) (bool, error) {
	importToken = strings.TrimSpace(importToken)
	tempPath = strings.TrimSpace(tempPath)

	var jobs []orm.AsyncJob
	if err := db.WithContext(ctx).
		Where("job_type = ? AND status IN ?", importJobType, []string{string(asyncjob.StatusPending), string(asyncjob.StatusRunning)}).
		Find(&jobs).Error; err != nil {
		return false, err
	}
	for _, job := range jobs {
		payload := EvalSetImportJobPayload{}
		if len(job.PayloadJSON) > 0 {
			if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
				continue
			}
		}
		if importToken != "" && strings.TrimSpace(payload.ImportToken) == importToken {
			return true, nil
		}
		if tempPath != "" && strings.TrimSpace(payload.TempPath) == tempPath {
			return true, nil
		}
	}
	return false, nil
}
