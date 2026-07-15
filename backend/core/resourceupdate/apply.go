package resourceupdate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/preferencefile"
	"lazymind/core/resourcechange"
	"lazymind/core/skillv2/taskguard"
)

func (w *Worker) handleAutoApplyReview(ctx context.Context, task orm.ResourceUpdateTask) taskOutcome {
	if strings.TrimSpace(task.ReviewResultID) == "" && strings.TrimSpace(task.TriggerID) == "" {
		return permanentOutcome("missing_review_result_id", "review_result_id required")
	}
	if task.ResourceType == orm.ResourceUpdateResourceTypeSkill {
		decision, err := taskguard.EvaluateSkillOperation(ctx, w.db, w.stateStore, taskguard.SkillOperationRequest{
			UserID:        task.UserID,
			SkillID:       task.ResourceID,
			Operation:     taskguard.AutoUpdateSkill,
			TriggerSource: "scheduled",
		})
		if err != nil {
			if decision.Disposition == taskguard.DispositionDefer {
				return deferredOutcome(decision.ReasonCode, decision.Message, decision.RetryAfter)
			}
			return retryableOutcome(taskguard.ReasonTaskStatusUnavailable, err)
		}
		if !decision.Allowed {
			return deferredOutcome(decision.ReasonCode, decision.Message, decision.RetryAfter)
		}
	}
	now := w.clock().UTC()
	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		switch task.ResourceType {
		case orm.ResourceUpdateResourceTypeSkill:
			return autoApplySkillReviewResult(ctx, tx, task, now)
		case orm.ResourceUpdateResourceTypeMemory:
			return autoApplyMemoryReviewResult(ctx, tx, task, now)
		case orm.ResourceUpdateResourceTypeUserPreference:
			return autoApplyPreferenceReviewResult(ctx, tx, task, now)
		default:
			return fmt.Errorf("%w: unsupported resource_type %q", errReviewInvalid, task.ResourceType)
		}
	})
	if err == nil {
		resourceUpdateInfo(logEventAutoApplyApplied).
			Str("task_id", task.ID).
			Str("resource_type", task.ResourceType).
			Str("resource_id", task.ResourceID).
			Str("user_id", task.UserID).
			Str("review_result_id", taskReviewResultID(task)).
			Msg(logEventAutoApplyApplied)
		return taskOutcome{Status: orm.ResourceUpdateTaskStatusDone, ResultID: taskReviewResultID(task)}
	}
	if errors.Is(err, errReviewConflict) || errors.Is(err, errReviewNotFound) || errors.Is(err, errReviewInvalid) {
		resourceUpdateWarn(logEventAutoApplySkipped, err).
			Str("task_id", task.ID).
			Str("resource_type", task.ResourceType).
			Str("resource_id", task.ResourceID).
			Str("user_id", task.UserID).
			Str("review_result_id", taskReviewResultID(task)).
			Str("reason", reviewSkipReason(err)).
			Msg(logEventAutoApplySkipped)
		return taskOutcome{Status: orm.ResourceUpdateTaskStatusSkipped, ErrorCode: "auto_apply_skipped", ErrorMessage: err.Error()}
	}
	return retryableOutcome("auto_apply_failed", err)
}

func autoApplySkillReviewResult(ctx context.Context, tx *gorm.DB, task orm.ResourceUpdateTask, _ time.Time) error {
	result, err := lockSkillReviewResult(ctx, tx, taskReviewResultID(task))
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.ReviewStatus) != reviewStatusPending {
		return fmt.Errorf("%w: skill review result is %q", errReviewConflict, result.ReviewStatus)
	}
	if strings.TrimSpace(result.Type) != skillReviewTypePatch {
		return fmt.Errorf("%w: auto apply only supports skill patch results", errReviewInvalid)
	}
	v2Resource, err := mapSkillPatchResultToV2Resource(ctx, withUpdateLock(tx), result)
	if err == nil && strings.TrimSpace(task.ResourceID) == strings.TrimSpace(v2Resource.ID) {
		if strings.TrimSpace(task.ResourceType) != orm.ResourceUpdateResourceTypeSkill ||
			strings.TrimSpace(task.UserID) != strings.TrimSpace(result.UserID) {
			return fmt.Errorf("%w: skill auto apply task mapping mismatch", errReviewConflict)
		}
		if !v2Resource.AutoEvo {
			return fmt.Errorf("%w: skill auto_evo disabled", errReviewConflict)
		}
		return applySkillV2PatchResult(ctx, tx, result, v2Resource)
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return errReviewNotFound
}

func autoApplyMemoryReviewResult(ctx context.Context, tx *gorm.DB, task orm.ResourceUpdateTask, now time.Time) error {
	result, err := lockMemoryReviewResult(ctx, tx, taskReviewResultID(task))
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.ReviewStatus) != reviewStatusPending {
		return fmt.Errorf("%w: memory review result is %q", errReviewConflict, result.ReviewStatus)
	}
	if normalizeReviewTarget(result.Target) != orm.ResourceUpdateResourceTypeMemory || strings.TrimSpace(result.State) != memoryReviewStateSuccess {
		return fmt.Errorf("%w: memory review result target/state mismatch", errReviewInvalid)
	}
	resource, err := mapMemoryReviewResultToPersonalResource(withUpdateLock(tx).WithContext(ctx), orm.ResourceUpdateResourceTypeMemory, result)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errReviewNotFound
		}
		return err
	}
	if strings.TrimSpace(task.ResourceType) != orm.ResourceUpdateResourceTypeMemory ||
		strings.TrimSpace(task.UserID) != strings.TrimSpace(result.UserID) ||
		strings.TrimSpace(task.ResourceID) != strings.TrimSpace(resource.ID) {
		return fmt.Errorf("%w: memory auto apply task mapping mismatch", errReviewConflict)
	}
	if !resource.AutoEvo {
		return fmt.Errorf("%w: memory auto_evo disabled", errReviewConflict)
	}
	return applyPersonalResourceReviewResult(ctx, tx, orm.ResourceUpdateResourceTypeMemory, result, resource, now, true, resourcechange.Source{
		ChangeSource:  resourcechange.ChangeSourceAutoApply,
		SourceRefType: resourcechange.SourceRefTypeMemoryReview,
		SourceRefID:   result.ID,
		ChangedAt:     now,
	})
}

func autoApplyPreferenceReviewResult(ctx context.Context, tx *gorm.DB, task orm.ResourceUpdateTask, now time.Time) error {
	result, err := lockMemoryReviewResult(ctx, tx, taskReviewResultID(task))
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.ReviewStatus) != reviewStatusPending {
		return fmt.Errorf("%w: user_preference review result is %q", errReviewConflict, result.ReviewStatus)
	}
	if normalizeReviewTarget(result.Target) != orm.ResourceUpdateResourceTypeUserPreference || strings.TrimSpace(result.State) != memoryReviewStateSuccess {
		return fmt.Errorf("%w: user_preference review result target/state mismatch", errReviewInvalid)
	}
	resource, err := mapMemoryReviewResultToPersonalResource(withUpdateLock(tx).WithContext(ctx), orm.ResourceUpdateResourceTypeUserPreference, result)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errReviewNotFound
		}
		return err
	}
	if strings.TrimSpace(task.ResourceType) != orm.ResourceUpdateResourceTypeUserPreference ||
		strings.TrimSpace(task.UserID) != strings.TrimSpace(result.UserID) ||
		strings.TrimSpace(task.ResourceID) != strings.TrimSpace(resource.ID) {
		return fmt.Errorf("%w: user_preference auto apply task mapping mismatch", errReviewConflict)
	}
	if !resource.AutoEvo {
		return fmt.Errorf("%w: user_preference auto_evo disabled", errReviewConflict)
	}
	return applyPersonalResourceReviewResult(ctx, tx, orm.ResourceUpdateResourceTypeUserPreference, result, resource, now, true, resourcechange.Source{
		ChangeSource:  resourcechange.ChangeSourceAutoApply,
		SourceRefType: resourcechange.SourceRefTypeMemoryReview,
		SourceRefID:   result.ID,
		ChangedAt:     now,
	})
}

func lockSkillReviewResult(ctx context.Context, tx *gorm.DB, id string) (SkillReviewResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SkillReviewResult{}, errReviewNotFound
	}
	var result SkillReviewResult
	err := skillResultSelect(withUpdateLock(tx).WithContext(ctx)).Where("id = ?", id).Take(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SkillReviewResult{}, errReviewNotFound
	}
	return result, err
}

func lockMemoryReviewResult(ctx context.Context, tx *gorm.DB, id string) (MemoryReviewResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return MemoryReviewResult{}, errReviewNotFound
	}
	var result MemoryReviewResult
	err := memoryResultSelect(withUpdateLock(tx).WithContext(ctx)).Where("id = ?", id).Take(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MemoryReviewResult{}, errReviewNotFound
	}
	return result, err
}

func applyPersonalResourceReviewResult(ctx context.Context, tx *gorm.DB, target string, result MemoryReviewResult, resource orm.PersonalResource, now time.Time, requireAutoEvo bool, source resourcechange.Source) error {
	content := result.Content
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("%w: personal resource content required", errReviewInvalid)
	}
	if target == orm.ResourceUpdateResourceTypeUserPreference {
		if _, err := preferencefile.ParseFileContent(content); err != nil {
			return fmt.Errorf("%w: %v", errReviewInvalid, err)
		}
	}
	if strings.TrimSpace(resource.ResourceType) != strings.TrimSpace(target) {
		return fmt.Errorf("%w: personal resource target mismatch", errReviewConflict)
	}
	if requireAutoEvo && !resource.AutoEvo {
		return fmt.Errorf("%w: personal resource auto_evo disabled", errReviewConflict)
	}
	if resource.HeadRevisionID == nil || strings.TrimSpace(*resource.HeadRevisionID) == "" {
		return errReviewNotFound
	}
	_, head, err := personalResourceHeadContent(ctx, tx, resource)
	if err != nil {
		return err
	}
	path := personalResourcePath(target)
	hash := evolution.HashContent(content)
	blob := orm.PersonalResourceBlob{
		Hash:           hash,
		Size:           int64(len([]byte(content))),
		Mime:           "text/markdown; charset=utf-8",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        []byte(content),
		CreatedAt:      now,
	}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&blob).Error; err != nil {
		return err
	}
	revisionID := evolution.NewID()
	parentID := head.ID
	revision := orm.PersonalResourceRevision{
		ID:               revisionID,
		ResourceID:       resource.ID,
		ParentRevisionID: &parentID,
		RevisionNo:       head.RevisionNo + 1,
		Path:             path,
		BlobHash:         hash,
		ContentHash:      hash,
		Size:             blob.Size,
		Mime:             blob.Mime,
		FileType:         blob.FileType,
		Binary:           false,
		Message:          "auto apply memory review",
		ChangeSource:     source.ChangeSource,
		SourceRefType:    source.SourceRefType,
		SourceRefID:      source.SourceRefID,
		CreatedBy:        nullableString(result.UserID),
		CreatedAt:        now,
	}
	if target == orm.ResourceUpdateResourceTypeUserPreference {
		revision.Message = "auto apply user_preference review"
	}
	if err := tx.WithContext(ctx).Create(&revision).Error; err != nil {
		return err
	}
	update := map[string]any{
		"head_revision_id":      revisionID,
		"version":               gorm.Expr("version + 1"),
		"auto_evo_apply_status": evolution.AutoEvoApplyStatusIdle,
		"auto_evo_error":        "",
		"auto_evo_finished_at":  now,
		"updated_by":            strings.TrimSpace(result.UserID),
		"updated_at":            now,
	}
	affected := tx.WithContext(ctx).Model(&orm.PersonalResource{}).
		Where("id = ? AND version = ? AND head_revision_id = ?", resource.ID, resource.Version, *resource.HeadRevisionID).
		Updates(update)
	if affected.Error != nil {
		return affected.Error
	}
	if affected.RowsAffected == 0 {
		return fmt.Errorf("%w: personal resource version changed", errReviewConflict)
	}
	if err := resetPersonalResourceDraftToRevision(ctx, tx, resource.ID, revision, result.UserID, now); err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Model(&orm.PersonalResourceReviewSession{}).
		Where("resource_id = ? AND status = ?", resource.ID, "active").
		Updates(map[string]any{"status": "invalidated", "updated_at": now}).Error; err != nil {
		return err
	}
	return updateMemoryReviewStatus(ctx, tx, result.ID, reviewStatusAccepted)
}

func resetPersonalResourceDraftToRevision(ctx context.Context, tx *gorm.DB, resourceID string, revision orm.PersonalResourceRevision, userID string, now time.Time) error {
	var draft orm.PersonalResourceDraft
	err := tx.WithContext(ctx).Where("resource_id = ?", resourceID).Take(&draft).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	version := int64(1)
	if err == nil {
		version = draft.Version + 1
	}
	row := orm.PersonalResourceDraft{
		ResourceID:     resourceID,
		BaseRevisionID: &revision.ID,
		Path:           revision.Path,
		BlobHash:       revision.BlobHash,
		ContentHash:    revision.ContentHash,
		Size:           revision.Size,
		Mime:           revision.Mime,
		FileType:       revision.FileType,
		Binary:         revision.Binary,
		DraftStatus:    "",
		TaskID:         "",
		UpdatedBy:      nullableString(userID),
		Version:        version,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err != nil {
		return tx.WithContext(ctx).Create(&row).Error
	}
	return tx.WithContext(ctx).Model(&orm.PersonalResourceDraft{}).
		Where("resource_id = ?", resourceID).
		Updates(map[string]any{
			"base_revision_id": revision.ID,
			"path":             revision.Path,
			"blob_hash":        revision.BlobHash,
			"content_hash":     revision.ContentHash,
			"size":             revision.Size,
			"mime":             revision.Mime,
			"file_type":        revision.FileType,
			"binary":           revision.Binary,
			"draft_status":     "",
			"draft_updated_at": nil,
			"task_id":          "",
			"conversation_id":  nil,
			"updated_by":       nullableString(userID),
			"version":          version,
			"updated_at":       now,
		}).Error
}

func personalResourcePath(target string) string {
	if strings.TrimSpace(target) == orm.ResourceUpdateResourceTypeUserPreference {
		return "memory/user.md"
	}
	return "memory/memory.md"
}

func nullableString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
