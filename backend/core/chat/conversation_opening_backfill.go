package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

var openingExplicitRetryErrorCodes = []string{
	"token_limit", "input_too_large", "output_too_large", "model_configuration", "authentication_failed", "invalid_request", "not_found",
	"invalid_output", "model_failed", "transport_error", "request_timeout", "rate_limited", "service_unavailable",
}

func enqueueOpeningScan(ctx context.Context, db *gorm.DB, batch orm.ConversationOpeningBackfill) error {
	var failed int64
	key := fmt.Sprintf("%s:%s:%d", batch.ID, batch.CursorID, batch.Scanned)
	if err := db.Model(&orm.AsyncJob{}).Where("job_type = ? AND idempotency_key = ? AND status = ?", openingBackfillJobType, key, "failed").Count(&failed).Error; err != nil {
		return err
	}
	if failed > 0 {
		return nil
	}
	_, err := asyncjob.Enqueue(ctx, db, asyncjob.EnqueueRequest{JobType: openingBackfillJobType, ResourceType: "opening_backfill", ResourceID: batch.ID,
		SkipSucceeded: true, IdempotencyKey: key, Payload: map[string]string{}, CreateUserID: batch.UserID, MaxAttempts: 3})
	return err
}

func (s *openingService) backfill(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	if job.ResourceType == "conversation" {
		var meta orm.ConversationOpening
		if err := s.db.WithContext(ctx).Where("conversation_id = ?", job.ResourceID).Take(&meta).Error; err != nil {
			return asyncjob.Result{}, err
		}
		var batch orm.ConversationOpeningBackfill
		if err := s.db.WithContext(ctx).Where("id = ?", meta.BackfillID).Take(&batch).Error; err != nil {
			return asyncjob.Result{}, err
		}
		if batch.Status == "paused" {
			return asyncjob.Result{}, nil
		}
		return s.generate(ctx, job, reporter)
	}
	var batch orm.ConversationOpeningBackfill
	if err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", job.ResourceID, job.CreateUserID).Take(&batch).Error; err != nil {
		return asyncjob.Result{}, err
	}
	if batch.Status == "paused" || batch.ScanComplete {
		return asyncjob.Result{}, nil
	}
	query := openingConversations(s.db.WithContext(ctx)).Where("create_user_id = ?", batch.UserID)
	if batch.CursorTime != nil {
		query = query.Where("updated_at < ? OR (updated_at = ? AND id < ?)", batch.CursorTime, batch.CursorTime, batch.CursorID)
	}
	var conversations []orm.Conversation
	if err := query.Order("updated_at DESC, id DESC").Limit(50).Find(&conversations).Error; err != nil {
		return asyncjob.Result{}, err
	}
	var skipped int64
	for _, conv := range conversations {
		var meta orm.ConversationOpening
		err := s.db.WithContext(ctx).Where("conversation_id = ?", conv.ID).Take(&meta).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if _, err = s.enqueue(ctx, conv.ID, batch.ID); err != nil {
				return asyncjob.Result{}, err
			}
			err = s.db.WithContext(ctx).Where("conversation_id = ?", conv.ID).Take(&meta).Error
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return asyncjob.Result{}, err
		}
		// A restarted page may have already enqueued this row before checkpointing.
		if meta.BackfillID != batch.ID {
			skipped++
		}
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{"scanned": gorm.Expr("scanned + ?", len(conversations)), "skipped": gorm.Expr("skipped + ?", skipped), "scan_complete": len(conversations) < 50, "updated_at": time.Now().UTC()}
		if len(conversations) > 0 {
			last := conversations[len(conversations)-1]
			updates["cursor_time"] = last.UpdatedAt
			updates["cursor_id"] = last.ID
		}
		result := tx.Model(&orm.ConversationOpeningBackfill{}).Where("id = ? AND scanned = ?", batch.ID, batch.Scanned).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := tx.Where("id = ?", batch.ID).Take(&batch).Error; err != nil {
			return err
		}
		if !batch.ScanComplete && batch.Status != "paused" {
			return enqueueOpeningScan(ctx, tx, batch)
		}
		return nil
	})
	return asyncjob.Result{}, err
}

// OpeningBackfill controls the user's one persistent batch; summaries never leave Core.
func OpeningBackfill(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	db := store.DB()
	var batch orm.ConversationOpeningBackfill
	if r.Method == http.MethodPost {
		var body struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			common.ReplyErr(w, "invalid body", http.StatusBadRequest)
			return
		}
		if body.Action != "start" && body.Action != "pause" && body.Action != "resume" && body.Action != "retry" {
			common.ReplyErr(w, "invalid action", http.StatusBadRequest)
			return
		}
		err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			batch = orm.ConversationOpeningBackfill{ID: "opening_" + common.GenerateID(), UserID: userID, Version: openingGeneratorVersion, Status: "running", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoNothing: true}).Create(&batch).Error; err != nil {
				return err
			}
			batch = orm.ConversationOpeningBackfill{}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).Take(&batch).Error; err != nil {
				return err
			}
			if body.Action == "pause" {
				if err := tx.Model(&batch).Update("status", "paused").Error; err != nil {
					return err
				}
				return tx.Model(&orm.AsyncJob{}).Where("job_type = ? AND create_user_id = ? AND status = ?", openingBackfillJobType, userID, "pending").Updates(map[string]any{"status": "canceled", "updated_at": time.Now().UTC()}).Error
			}
			if body.Action == "start" && batch.Status == "paused" {
				return nil
			}
			if body.Action == "resume" || body.Action == "retry" {
				if err := tx.Model(&batch).Update("status", "running").Error; err != nil {
					return err
				}
				// Paused jobs are canceled rather than left holding a worker or consuming retry attempts.
				if err := tx.Model(&orm.AsyncJob{}).Where("job_type = ? AND create_user_id = ? AND status IN ?", openingBackfillJobType, userID, []string{"canceled", "succeeded"}).
					Where("resource_id IN (SELECT conversation_id FROM conversation_opening_metadata WHERE backfill_id = ? AND status = 'pending')", batch.ID).
					Updates(map[string]any{"status": "pending", "attempt_count": 0, "next_run_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
			}
			if body.Action == "retry" {
				if err := tx.Model(&orm.AsyncJob{}).Where("job_type = ? AND resource_type = ? AND resource_id = ? AND status = ?", openingBackfillJobType, "opening_backfill", batch.ID, "failed").
					Updates(map[string]any{"status": "pending", "attempt_count": 0, "next_run_at": time.Now().UTC()}).Error; err != nil {
					return err
				}
				config, err := newOpeningService(tx).loadConfig(r.Context(), tx, userID)
				if err != nil {
					return err
				}
				var failed []orm.ConversationOpening
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND status = ? AND error_code IN ?", userID, "failed", openingExplicitRetryErrorCodes).Find(&failed).Error; err != nil {
					return err
				}
				for _, meta := range failed {
					var usage struct {
						ConfigurationHash string `json:"configuration_hash"`
					}
					_ = json.Unmarshal(meta.UsageJSON, &usage)
					if !openingShouldRetry(meta.ErrorCode, usage.ConfigurationHash, openingConfigHash(config)) {
						continue
					}
					meta.SeedRevision++
					meta.CallCount = 0
					meta.Status = "pending"
					meta.ErrorCode = ""
					meta.UpdatedAt = time.Now().UTC()
					kind := openingJobType
					if meta.BackfillID != "" {
						kind = openingBackfillJobType
					}
					if err := enqueueOpeningVersion(r.Context(), tx, &meta, kind); err != nil {
						return err
					}
				}
			}

			if batch.ScanComplete {
				return nil
			}
			return enqueueOpeningScan(r.Context(), tx, batch)
		})
		if err != nil {
			common.ReplyErr(w, "update backfill failed", http.StatusInternalServerError)
			return
		}
	}
	if err := db.WithContext(r.Context()).Where("user_id = ?", userID).Take(&batch).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "load backfill failed", http.StatusInternalServerError)
		return
	}
	if err := db.WithContext(r.Context()).Model(&orm.ConversationOpening{}).
		Where("user_id = ? AND status IN ?", userID, []string{"pending", "running"}).
		Where("job_id IN (SELECT id FROM async_jobs WHERE status = 'failed')").
		Updates(map[string]any{"status": "failed", "error_code": gorm.Expr("(SELECT error_code FROM async_jobs WHERE id = conversation_opening_metadata.job_id)")}).Error; err != nil {
		common.ReplyErr(w, "reconcile metadata state failed", 500)
		return
	}
	var counts []struct {
		Status string
		Count  int64
	}
	if err := db.WithContext(r.Context()).Model(&orm.ConversationOpening{}).Select("status, count(*) AS count").Where("backfill_id = ? AND user_id = ? AND backfill_id <> ''", batch.ID, userID).Group("status").Scan(&counts).Error; err != nil {
		common.ReplyErr(w, "load backfill failed", 500)
		return
	}
	states := map[string]int64{}
	if batch.ID == "" {
		batch.Status = "idle"
	}
	for _, row := range counts {
		states[row.Status] = row.Count
	}
	if batch.ScanComplete && states["pending"]+states["running"] == 0 && batch.Status != "paused" {
		batch.Status = "done"
	}
	if !batch.ScanComplete && batch.Status != "paused" {
		var failedScans int64
		if err := db.WithContext(r.Context()).Model(&orm.AsyncJob{}).Where("job_type = ? AND resource_type = ? AND resource_id = ? AND status = ?", openingBackfillJobType, "opening_backfill", batch.ID, "failed").Count(&failedScans).Error; err != nil {
			common.ReplyErr(w, "load scan state failed", 500)
			return
		}
		if failedScans > 0 {
			batch.Status = "failed"
		}
	}
	// Revision polling covers both realtime and backfilled titles, without exposing summaries.
	var revision struct {
		Revision int64
		Pending  int64
	}
	if err := db.WithContext(r.Context()).Model(&orm.ConversationOpening{}).Select("COALESCE(SUM(metadata_revision),0) AS revision, COALESCE(SUM(CASE WHEN status IN ('pending','running') AND job_id IN (SELECT id FROM async_jobs WHERE status IN ('pending','running')) THEN 1 ELSE 0 END),0) AS pending").Where("user_id = ?", userID).Scan(&revision).Error; err != nil {
		common.ReplyErr(w, "load metadata state failed", 500)
		return
	}
	var remaining int64
	if err := openingConversations(db.WithContext(r.Context())).Where("create_user_id = ?", userID).
		Where("NOT EXISTS (SELECT 1 FROM conversation_opening_metadata WHERE conversation_id = conversations.id)").Count(&remaining).Error; err != nil {
		common.ReplyErr(w, "load remaining conversations failed", 500)
		return
	}
	writeConversationJSON(w, http.StatusOK, map[string]any{"batch": batch, "completed": states["done"], "failed": states["failed"], "skipped": batch.Skipped + states["skipped"], "pending": revision.Pending, "revision": revision.Revision, "unprocessed": remaining + states["pending"] + states["running"]})
}

func openingSameConfigRetryable(code string) bool {
	switch code {
	case "invalid_output", "model_failed", "transport_error", "request_timeout", "rate_limited", "service_unavailable":
		return true
	}
	return false
}

func openingShouldRetry(code, previousConfigHash, currentConfigHash string) bool {
	return previousConfigHash != currentConfigHash || openingSameConfigRetryable(code)
}

func RenameConversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title    string `json:"display_name"`
		Revision int64  `json:"title_revision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Title) == "" || len([]rune(body.Title)) > 255 {
		common.ReplyErr(w, "invalid title", 400)
		return
	}
	id := conversationIDFromName(conversationNameFromPath(r))
	result := store.DB().WithContext(r.Context()).Model(&orm.Conversation{}).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL AND title_revision = ?", id, store.UserID(r), body.Revision).
		UpdateColumns(map[string]any{"display_name": strings.TrimSpace(body.Title), "title_source": "user", "title_revision": gorm.Expr("title_revision + 1")})
	if result.Error != nil {
		common.ReplyErr(w, "rename conversation failed", 500)
		return
	}
	if result.RowsAffected == 0 {
		common.ReplyErr(w, "conversation changed", http.StatusConflict)
		return
	}
	writeConversationJSON(w, 200, map[string]any{"display_name": strings.TrimSpace(body.Title), "title_revision": body.Revision + 1})
}
