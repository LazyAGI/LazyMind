package resourceupdate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/resourcechange"
)

func (w *Worker) handleAutoApplyReview(ctx context.Context, task orm.ResourceUpdateTask) taskOutcome {
	if strings.TrimSpace(task.ReviewResultID) == "" && strings.TrimSpace(task.TriggerID) == "" {
		return permanentOutcome("missing_review_result_id", "review_result_id required")
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

func autoApplySkillReviewResult(ctx context.Context, tx *gorm.DB, task orm.ResourceUpdateTask, now time.Time) error {
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
	resource, err := mapSkillPatchResultToResource(withUpdateLock(tx).WithContext(ctx), result)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errReviewNotFound
		}
		return err
	}
	if strings.TrimSpace(task.ResourceType) != orm.ResourceUpdateResourceTypeSkill ||
		strings.TrimSpace(task.UserID) != strings.TrimSpace(result.UserID) ||
		strings.TrimSpace(task.ResourceID) != strings.TrimSpace(resource.ID) {
		return fmt.Errorf("%w: skill auto apply task mapping mismatch", errReviewConflict)
	}
	if !resource.AutoEvo {
		return fmt.Errorf("%w: skill auto_evo disabled", errReviewConflict)
	}
	return applySkillPatchResult(ctx, tx, result, resource, now, resourcechange.Source{
		ChangeSource:  resourcechange.ChangeSourceAutoApply,
		SourceRefType: resourcechange.SourceRefTypeSkillReviewResult,
		SourceRefID:   result.ID,
		ChangedAt:     now,
	})
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
	resource, err := mapMemoryReviewResultToMemory(withUpdateLock(tx).WithContext(ctx), result)
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
	return applyMemoryReviewResult(ctx, tx, result, resource, now, true, resourcechange.Source{
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
	resource, err := mapMemoryReviewResultToPreference(withUpdateLock(tx).WithContext(ctx), result)
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
	return applyPreferenceReviewResult(ctx, tx, result, resource, now, true, resourcechange.Source{
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

func applySkillPatchResult(ctx context.Context, tx *gorm.DB, result SkillReviewResult, resource orm.SkillResource, now time.Time, source resourcechange.Source) error {
	content := result.SkillContent
	meta, err := validateSkillReviewContent(result.SkillName, content)
	if err != nil {
		return err
	}
	update := map[string]any{
		"description":           meta.Description,
		"content":               content,
		"content_size":          skillContentSize(content),
		"mime_type":             mimeTypeForExt(resource.FileExt),
		"content_hash":          evolution.HashContent(content),
		"version":               resource.Version + 1,
		"draft_content":         "",
		"draft_source_version":  0,
		"draft_status":          "",
		"draft_updated_at":      nil,
		"update_status":         evolution.UpdateStatusUpToDate,
		"auto_evo_apply_status": evolution.AutoEvoApplyStatusIdle,
		"auto_evo_error":        "",
		"updated_at":            now,
		"ext":                   clearLegacyDraftSuggestionRefs(resource.Ext),
	}
	affected, err := resourcechange.UpdateModel(ctx, tx, &orm.SkillResource{}, func(query *gorm.DB) *gorm.DB {
		return query.Where("id = ? AND version = ?", resource.ID, resource.Version)
	}, update, resourcechange.ContentChange{
		ResourceType:  orm.ResourceUpdateResourceTypeSkill,
		ResourceID:    resource.ID,
		UserID:        resource.OwnerUserID,
		FromVersion:   resource.Version,
		ToVersion:     resource.Version + 1,
		BeforeContent: resource.Content,
		AfterContent:  content,
		Source:        source,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: skill version changed", errReviewConflict)
	}
	if childErr := tx.WithContext(ctx).Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND node_type = ? AND category = ? AND parent_skill_name = ?",
			resource.OwnerUserID, evolution.SkillNodeTypeChild, resource.Category, resource.SkillName).
		Updates(map[string]any{"update_status": evolution.UpdateStatusUpToDate, "updated_at": now}).Error; childErr != nil {
		return childErr
	}
	return updateSkillReviewStatus(ctx, tx, result.ID, reviewStatusAccepted)
}

func createSkillFromNewResult(ctx context.Context, tx *gorm.DB, result SkillReviewResult, userName string, now time.Time, source resourcechange.Source) (orm.SkillResource, error) {
	content := result.SkillContent
	meta, err := validateSkillReviewContent(result.SkillName, content)
	if err != nil {
		return orm.SkillResource{}, err
	}
	category := strings.TrimSpace(meta.Category)
	if category == "" {
		category = "system"
	}
	if err := validatePathSegment(category); err != nil {
		return orm.SkillResource{}, err
	}
	if err := validatePathSegment(meta.Name); err != nil {
		return orm.SkillResource{}, err
	}
	var count int64
	if err := tx.WithContext(ctx).Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND node_type = ? AND skill_name = ?", result.UserID, evolution.SkillNodeTypeParent, meta.Name).
		Count(&count).Error; err != nil {
		return orm.SkillResource{}, err
	}
	if count > 0 {
		return orm.SkillResource{}, gorm.ErrDuplicatedKey
	}
	relPath := evolution.ParentSkillRelativePath(category, meta.Name)
	if err := tx.WithContext(ctx).Model(&orm.SkillResource{}).
		Where("owner_user_id = ? AND relative_path = ?", result.UserID, relPath).
		Count(&count).Error; err != nil {
		return orm.SkillResource{}, err
	}
	if count > 0 {
		return orm.SkillResource{}, gorm.ErrDuplicatedKey
	}
	row := orm.SkillResource{
		ID:             newSkillResourceID(),
		OwnerUserID:    result.UserID,
		OwnerUserName:  strings.TrimSpace(userName),
		Category:       category,
		SkillName:      meta.Name,
		NodeType:       evolution.SkillNodeTypeParent,
		Description:    meta.Description,
		FileExt:        "md",
		RelativePath:   relPath,
		Content:        content,
		ContentSize:    skillContentSize(content),
		MimeType:       mimeTypeForExt("md"),
		ContentHash:    evolution.HashContent(content),
		Version:        1,
		AutoEvo:        false,
		IsEnabled:      true,
		UpdateStatus:   evolution.UpdateStatusUpToDate,
		CreateUserID:   result.UserID,
		CreateUserName: strings.TrimSpace(userName),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := resourcechange.CreateModel(ctx, tx, &row, resourcechange.ContentChange{
		ResourceType:  orm.ResourceUpdateResourceTypeSkill,
		ResourceID:    row.ID,
		UserID:        row.OwnerUserID,
		FromVersion:   0,
		ToVersion:     row.Version,
		BeforeContent: "",
		AfterContent:  row.Content,
		Source:        source,
	}); err != nil {
		return orm.SkillResource{}, err
	}
	return row, nil
}

func applyMemoryReviewResult(ctx context.Context, tx *gorm.DB, result MemoryReviewResult, resource orm.SystemMemory, now time.Time, requireAutoEvo bool, source resourcechange.Source) error {
	content := result.Content
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("%w: memory content required", errReviewInvalid)
	}
	if requireAutoEvo && !resource.AutoEvo {
		return fmt.Errorf("%w: memory auto_evo disabled", errReviewConflict)
	}
	update := map[string]any{
		"content":               content,
		"content_hash":          evolution.HashSystemMemory(orm.SystemMemory{Content: content}),
		"version":               resource.Version + 1,
		"draft_content":         "",
		"draft_source_version":  0,
		"draft_status":          "",
		"draft_updated_at":      nil,
		"auto_evo_apply_status": evolution.AutoEvoApplyStatusIdle,
		"auto_evo_error":        "",
		"updated_by":            result.UserID,
		"updated_at":            now,
		"ext":                   clearLegacyDraftSuggestionRefs(resource.Ext),
	}
	affected, err := resourcechange.UpdateModel(ctx, tx, &orm.SystemMemory{}, func(query *gorm.DB) *gorm.DB {
		return query.Where("id = ? AND version = ?", resource.ID, resource.Version)
	}, update, resourcechange.ContentChange{
		ResourceType:  orm.ResourceUpdateResourceTypeMemory,
		ResourceID:    resource.ID,
		UserID:        resource.UserID,
		FromVersion:   resource.Version,
		ToVersion:     resource.Version + 1,
		BeforeContent: resource.Content,
		AfterContent:  content,
		Source:        source,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: memory version changed", errReviewConflict)
	}
	return updateMemoryReviewStatus(ctx, tx, result.ID, reviewStatusAccepted)
}

func applyPreferenceReviewResult(ctx context.Context, tx *gorm.DB, result MemoryReviewResult, resource orm.SystemUserPreference, now time.Time, requireAutoEvo bool, source resourcechange.Source) error {
	parsed, err := evolution.ParseSystemUserPreferenceContent(result.Content)
	if err != nil {
		return fmt.Errorf("%w: %v", errReviewInvalid, err)
	}
	if requireAutoEvo && !resource.AutoEvo {
		return fmt.Errorf("%w: user_preference auto_evo disabled", errReviewConflict)
	}
	hashRow := resource
	hashRow.Content = parsed.Content
	hashRow.AgentPersona = parsed.AgentPersona
	hashRow.PreferredName = parsed.PreferredName
	hashRow.ResponseStyle = parsed.ResponseStyle
	update := map[string]any{
		"content":               parsed.Content,
		"agent_persona":         parsed.AgentPersona,
		"preferred_name":        parsed.PreferredName,
		"response_style":        parsed.ResponseStyle,
		"content_hash":          evolution.HashSystemUserPreference(hashRow),
		"version":               resource.Version + 1,
		"draft_content":         "",
		"draft_source_version":  0,
		"draft_status":          "",
		"draft_updated_at":      nil,
		"auto_evo_apply_status": evolution.AutoEvoApplyStatusIdle,
		"auto_evo_error":        "",
		"updated_by":            result.UserID,
		"updated_at":            now,
		"ext":                   clearLegacyDraftSuggestionRefs(resource.Ext),
	}
	affected, err := resourcechange.UpdateModel(ctx, tx, &orm.SystemUserPreference{}, func(query *gorm.DB) *gorm.DB {
		return query.Where("id = ? AND version = ?", resource.ID, resource.Version)
	}, update, resourcechange.ContentChange{
		ResourceType:  orm.ResourceUpdateResourceTypeUserPreference,
		ResourceID:    resource.ID,
		UserID:        resource.UserID,
		FromVersion:   resource.Version,
		ToVersion:     resource.Version + 1,
		BeforeContent: evolution.FormatSystemUserPreferenceForChat(resource),
		AfterContent:  evolution.FormatSystemUserPreferenceForChat(hashRow),
		Source:        source,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: user_preference version changed", errReviewConflict)
	}
	return updateMemoryReviewStatus(ctx, tx, result.ID, reviewStatusAccepted)
}
