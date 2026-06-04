package source

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *SQLRepository) CreateParseTask(ctx context.Context, parseTask ParseTask) error {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return err
	}
	return mapSQLConstraint(db.Model(&ormParseTask{}).Create(parseTaskORMValues(parseTask)).Error)
}

func (r *SQLRepository) FindActiveTask(ctx context.Context, sourceID, bindingID, objectKey, targetVersionID, action string) (ParseTask, bool, error) {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return ParseTask{}, false, err
	}
	var model ormParseTask
	err = db.Where("source_id = ? AND binding_id = ? AND object_key = ? AND target_version_id = ? AND task_action = ?",
		sourceID, bindingID, objectKey, targetVersionID, action).
		Where("status IN ?", []string{ParseTaskStatusPending, ParseTaskStatusRunning, ParseTaskStatusSubmitted}).
		Limit(1).
		First(&model).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ParseTask{}, false, nil
		}
		return ParseTask{}, false, mapSQLConstraint(err)
	}
	return parseTaskFromORM(model), true, nil
}

func (r *SQLRepository) ListParseTasks(ctx context.Context, req ParseTaskListRequest) ([]ParseTaskWithRefs, int, error) {
	pageSize := normalizeSQLPageSize(req.PageSize)
	db, err := r.sourceORM(ctx)
	if err != nil {
		return nil, 0, err
	}
	base := applyParseTaskListORMFilter(db.Model(&ormParseTask{}), req)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, mapSQLConstraint(err)
	}
	var models []ormParseTask
	if err := applyParseTaskListORMFilter(db.Model(&ormParseTask{}), req).
		Order("created_at DESC, task_id").
		Limit(pageSize).
		Offset(parseTaskOffset(req)).
		Find(&models).Error; err != nil {
		return nil, 0, mapSQLConstraint(err)
	}
	items := []ParseTaskWithRefs{}
	for _, model := range models {
		task := parseTaskFromORM(model)
		item, err := r.refsForParseTask(ctx, task)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, int(total), nil
}

func (r *SQLRepository) GetParseTask(ctx context.Context, taskID string) (ParseTaskWithRefs, error) {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return ParseTaskWithRefs{}, err
	}
	var model ormParseTask
	if err := db.Where("task_id = ?", taskID).First(&model).Error; err != nil {
		return ParseTaskWithRefs{}, mapGORMError(err, ErrCodeTaskNotFound, "parse task not found")
	}
	return r.refsForParseTask(ctx, parseTaskFromORM(model))
}

func (r *SQLRepository) GetParseTaskStats(ctx context.Context, req ParseTaskStatsRequest) (ParseTaskStats, error) {
	stats := ParseTaskStats{ByStatus: map[string]int64{}, ByAction: map[string]int64{}}
	db, err := r.sourceORM(ctx)
	if err != nil {
		return stats, err
	}
	var rows []struct {
		Status             string
		TaskAction         string
		Count              int64
		RetryableFailedCnt int64
	}
	err = applyParseTaskStatsORMFilter(db.Model(&ormParseTask{}), req).
		Select("status, task_action, COUNT(*) AS count, COUNT(*) FILTER (WHERE status = 'FAILED' AND retry_count < 3) AS retryable_failed_cnt").
		Group("status, task_action").
		Scan(&rows).Error
	if err != nil {
		return stats, mapSQLConstraint(err)
	}
	for _, row := range rows {
		stats.Total += row.Count
		stats.ByStatus[row.Status] += row.Count
		stats.ByAction[row.TaskAction] += row.Count
		stats.RetryableFailedCount += row.RetryableFailedCnt
	}
	return stats, nil
}

func (r *SQLRepository) refsForParseTask(ctx context.Context, task ParseTask) (ParseTaskWithRefs, error) {
	item := ParseTaskWithRefs{Task: task}
	if source, err := r.GetSource(ctx, task.SourceID); err == nil {
		item.Source = &source
	}
	if binding, err := r.GetBinding(ctx, task.SourceID, task.BindingID); err == nil {
		item.Binding = &binding
	}
	if document, err := r.GetDocument(ctx, task.SourceID, task.BindingID, task.ObjectKey); err == nil {
		item.Document = &document
	}
	if state, err := r.GetDocumentState(ctx, task.SourceID, task.BindingID, task.ObjectKey); err == nil {
		item.State = &state
	}
	if object, err := r.GetObject(ctx, task.SourceID, task.BindingID, task.ObjectKey); err == nil {
		item.Object = &object
	}
	return item, nil
}

func (r *SQLRepository) ClaimDueTask(ctx context.Context, workerID string, now time.Time, ttl time.Duration) (ParseTask, bool, error) {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	var parseTask ParseTask
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var model ormParseTask
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where(`(
				(status IN ? AND next_run_at <= ?)
				OR (status = ? AND (
					(lease_until IS NOT NULL AND lease_until <= ?)
					OR lease_owner IS NULL OR lease_owner = '' OR lease_until IS NULL
				))
			)`, []string{ParseTaskStatusPending, ParseTaskStatusFailed}, now, ParseTaskStatusRunning, now).
			Where("NOT EXISTS (SELECT 1 FROM parse_task_dead_letters dl WHERE dl.task_id = parse_tasks.task_id)").
			Order("next_run_at, task_id").
			Limit(1).
			First(&model).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		taskRow := parseTaskFromORM(model)
		taskRow.Status = ParseTaskStatusRunning
		taskRow.LeaseOwner = workerID
		taskRow.LeaseUntil = timePtr(now.Add(ttl))
		taskRow.UpdatedAt = now
		if err := updateParseTaskORM(tx, taskRow); err != nil {
			return err
		}
		parseTask = taskRow
		return nil
	})
	if err != nil {
		return ParseTask{}, false, err
	}
	return parseTask, parseTask.TaskID != "", nil
}

func (r *SQLRepository) ClaimSubmittedTask(ctx context.Context, workerID string, now time.Time, ttl time.Duration) (ParseTask, bool, error) {
	var parseTask ParseTask
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var model ormParseTask
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND next_run_at <= ?", ParseTaskStatusSubmitted, now).
			Where("lease_owner IS NULL OR lease_owner = '' OR lease_until IS NULL OR lease_until <= ?", now).
			Order("next_run_at, task_id").
			Limit(1).
			First(&model).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		taskRow := parseTaskFromORM(model)
		taskRow.LeaseOwner = workerID
		taskRow.LeaseUntil = timePtr(now.Add(ttl))
		taskRow.UpdatedAt = now
		if err := updateParseTaskORM(tx, taskRow); err != nil {
			return err
		}
		parseTask = taskRow
		return nil
	})
	if err != nil {
		return ParseTask{}, false, err
	}
	return parseTask, parseTask.TaskID != "", nil
}

func (r *SQLRepository) HeartbeatTaskLease(ctx context.Context, taskID, workerID string, now time.Time, ttl time.Duration) (ParseTask, bool, error) {
	var parseTask ParseTask
	var extended bool
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var model ormParseTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id = ?", taskID).First(&model).Error; err != nil {
			return mapGORMError(err, ErrCodeTaskNotFound, "parse task not found")
		}
		taskRow := parseTaskFromORM(model)
		parseTask = taskRow
		if taskRow.LeaseOwner != workerID || taskRow.LeaseUntil == nil || !taskRow.LeaseUntil.After(now) {
			return nil
		}
		taskRow.LeaseUntil = timePtr(now.Add(ttl))
		taskRow.UpdatedAt = now
		if err := updateParseTaskORM(tx, taskRow); err != nil {
			return err
		}
		parseTask = taskRow
		extended = true
		return nil
	})
	return parseTask, extended, err
}

func (r *SQLRepository) ReleaseTaskLease(ctx context.Context, taskID, workerID string, nextRunAt time.Time) (ParseTask, bool, error) {
	var parseTask ParseTask
	var released bool
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var model ormParseTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id = ?", taskID).First(&model).Error; err != nil {
			return mapGORMError(err, ErrCodeTaskNotFound, "parse task not found")
		}
		taskRow := parseTaskFromORM(model)
		parseTask = taskRow
		if taskRow.LeaseOwner != workerID {
			return nil
		}
		if taskRow.Status == ParseTaskStatusRunning {
			taskRow.Status = ParseTaskStatusPending
		}
		taskRow.LeaseOwner = ""
		taskRow.LeaseUntil = nil
		taskRow.NextRunAt = nextRunAt
		taskRow.UpdatedAt = nextRunAt
		if err := updateParseTaskORM(tx, taskRow); err != nil {
			return err
		}
		parseTask = taskRow
		released = true
		return nil
	})
	return parseTask, released, err
}

func (r *SQLRepository) RetryOrDeadLetterTask(ctx context.Context, taskID, reason string, now time.Time, maxRetries int64, backoff time.Duration) (ParseTask, bool, error) {
	var parseTask ParseTask
	var deadLettered bool
	err := r.withORMTx(ctx, func(tx *gorm.DB) error {
		var model ormParseTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id = ?", taskID).First(&model).Error; err != nil {
			return mapGORMError(err, ErrCodeTaskNotFound, "parse task not found")
		}
		taskRow := parseTaskFromORM(model)
		taskRow.RetryCount++
		taskRow.LastError = JSON{"reason": reason}
		taskRow.LeaseOwner = ""
		taskRow.LeaseUntil = nil
		taskRow.UpdatedAt = now
		if taskRow.RetryCount >= maxRetries {
			taskRow.Status = ParseTaskStatusFailed
			if err := updateParseTaskORM(tx, taskRow); err != nil {
				return err
			}
			if err := upsertDeadLetterORM(tx, taskRow, reason, now); err != nil {
				return err
			}
			parseTask = taskRow
			deadLettered = true
			return nil
		}
		taskRow.Status = ParseTaskStatusPending
		taskRow.NextRunAt = now.Add(backoff)
		if err := updateParseTaskORM(tx, taskRow); err != nil {
			return err
		}
		parseTask = taskRow
		return nil
	})
	return parseTask, deadLettered, err
}

func (r *SQLRepository) SaveParseTask(ctx context.Context, parseTask ParseTask) error {
	return r.withORMTx(ctx, func(tx *gorm.DB) error {
		return updateParseTaskORM(tx, parseTask)
	})
}

func (r *SQLRepository) SupersedeTask(ctx context.Context, taskID string, reason string) error {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return err
	}
	err = db.Model(&ormParseTask{}).Where("task_id = ?", taskID).Updates(map[string]any{
		"status":     ParseTaskStatusSuperseded,
		"last_error": JSON{"reason": reason},
		"updated_at": time.Now().UTC(),
	}).Error
	return mapSQLConstraint(err)
}

func (r *SQLRepository) FailTask(ctx context.Context, taskID string, reason string) error {
	db, err := r.sourceORM(ctx)
	if err != nil {
		return err
	}
	err = db.Model(&ormParseTask{}).Where("task_id = ?", taskID).Updates(map[string]any{
		"status":     ParseTaskStatusFailed,
		"last_error": JSON{"reason": reason},
		"updated_at": time.Now().UTC(),
	}).Error
	return mapSQLConstraint(err)
}

func parseTaskOffset(req ParseTaskListRequest) int {
	page, pageSize := normalizeSQLPage(req.Page, req.PageSize)
	return (page - 1) * pageSize
}

func parseTaskORMValues(task ParseTask) map[string]any {
	return map[string]any{
		"task_id":                 task.TaskID,
		"tenant_id":               nullString(task.TenantID),
		"source_id":               task.SourceID,
		"binding_id":              task.BindingID,
		"binding_generation":      task.BindingGeneration,
		"object_key":              task.ObjectKey,
		"document_id":             task.DocumentID,
		"task_action":             task.TaskAction,
		"target_version_id":       task.TargetVersionID,
		"source_version":          task.SourceVersion,
		"core_parent_document_id": task.CoreParentDocumentID,
		"idempotency_key":         task.IdempotencyKey,
		"status":                  task.Status,
		"core_task_id":            nullString(task.CoreTaskID),
		"core_document_id":        nullString(task.CoreDocumentID),
		"lease_owner":             nullString(task.LeaseOwner),
		"lease_until":             task.LeaseUntil,
		"retry_count":             task.RetryCount,
		"next_run_at":             task.NextRunAt,
		"last_error":              task.LastError,
		"created_at":              task.CreatedAt,
		"updated_at":              task.UpdatedAt,
	}
}

func parseTaskFromORM(model ormParseTask) ParseTask {
	return ParseTask{
		TaskID:               model.TaskID,
		TenantID:             model.TenantID,
		SourceID:             model.SourceID,
		BindingID:            model.BindingID,
		BindingGeneration:    model.BindingGeneration,
		ObjectKey:            model.ObjectKey,
		DocumentID:           model.DocumentID,
		TaskAction:           model.TaskAction,
		TargetVersionID:      model.TargetVersionID,
		SourceVersion:        model.SourceVersion,
		CoreParentDocumentID: model.CoreParentDocumentID,
		IdempotencyKey:       model.IdempotencyKey,
		Status:               model.Status,
		CoreTaskID:           model.CoreTaskID,
		CoreDocumentID:       model.CoreDocumentID,
		LeaseOwner:           model.LeaseOwner,
		LeaseUntil:           model.LeaseUntil,
		RetryCount:           model.RetryCount,
		NextRunAt:            model.NextRunAt,
		LastError:            model.LastError,
		CreatedAt:            model.CreatedAt,
		UpdatedAt:            model.UpdatedAt,
	}
}

func applyParseTaskListORMFilter(db *gorm.DB, req ParseTaskListRequest) *gorm.DB {
	if req.SourceIDs != nil {
		if len(req.SourceIDs) == 0 {
			db = db.Where("1 = 0")
		} else {
			db = db.Where("source_id IN ?", req.SourceIDs)
		}
	}
	if req.SourceID != "" {
		db = db.Where("source_id = ?", req.SourceID)
	}
	if req.BindingID != "" {
		db = db.Where("binding_id = ?", req.BindingID)
	}
	if req.DocumentID != "" {
		db = db.Where("document_id = ?", req.DocumentID)
	}
	if len(req.Statuses) > 0 {
		db = db.Where("status IN ?", req.Statuses)
	}
	if len(req.TaskActions) > 0 {
		db = db.Where("task_action IN ?", req.TaskActions)
	}
	return db
}

func applyParseTaskStatsORMFilter(db *gorm.DB, req ParseTaskStatsRequest) *gorm.DB {
	if req.SourceIDs != nil {
		if len(req.SourceIDs) == 0 {
			db = db.Where("1 = 0")
		} else {
			db = db.Where("source_id IN ?", req.SourceIDs)
		}
	}
	if req.SourceID != "" {
		db = db.Where("source_id = ?", req.SourceID)
	}
	if req.BindingID != "" {
		db = db.Where("binding_id = ?", req.BindingID)
	}
	if req.DocumentID != "" {
		db = db.Where("document_id = ?", req.DocumentID)
	}
	return db
}

func updateParseTaskORM(db *gorm.DB, task ParseTask) error {
	err := db.Model(&ormParseTask{}).Where("task_id = ?", task.TaskID).Updates(map[string]any{
		"status":           task.Status,
		"core_task_id":     nullString(task.CoreTaskID),
		"core_document_id": nullString(task.CoreDocumentID),
		"lease_owner":      nullString(task.LeaseOwner),
		"lease_until":      task.LeaseUntil,
		"retry_count":      task.RetryCount,
		"next_run_at":      task.NextRunAt,
		"last_error":       task.LastError,
		"updated_at":       task.UpdatedAt,
	}).Error
	return mapSQLConstraint(err)
}

func upsertDeadLetterORM(db *gorm.DB, task ParseTask, reason string, now time.Time) error {
	values := map[string]any{
		"dead_letter_id":     "dead-letter-" + task.TaskID,
		"task_id":            task.TaskID,
		"tenant_id":          nullString(task.TenantID),
		"source_id":          task.SourceID,
		"binding_id":         task.BindingID,
		"binding_generation": task.BindingGeneration,
		"object_key":         task.ObjectKey,
		"document_id":        task.DocumentID,
		"task_action":        task.TaskAction,
		"target_version_id":  task.TargetVersionID,
		"retry_count":        task.RetryCount,
		"error_code":         reason,
		"last_error":         task.LastError,
		"failed_at":          now,
		"created_at":         now,
	}
	err := db.Model(&ormParseTaskDeadLetter{}).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "dead_letter_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"retry_count": task.RetryCount,
			"error_code":  reason,
			"last_error":  task.LastError,
			"failed_at":   now,
		}),
	}).Create(values).Error
	return mapSQLConstraint(err)
}
