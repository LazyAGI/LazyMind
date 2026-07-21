package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

type Scanner struct {
	db       *gorm.DB
	cfg      Config
	clock    clockFunc
	workerID string
}

type ScannerRunResult struct {
	SkillResultsExpired        int
	SkillTasksCreated          int
	SkillDraftTasksCreated     int
	MemoryTasksCreated         int
	UserPreferenceTasksCreated int
}

type autoApplyTrigger struct {
	TriggerType string
	Generation  int64
}

func NewScanner(db *gorm.DB, cfg Config, workerID string) *Scanner {
	cfg = normalizeConfig(cfg)
	if strings.TrimSpace(workerID) == "" {
		workerID = defaultWorkerID("resourceupdate-scanner")
	}
	return &Scanner{
		db:       db,
		cfg:      cfg,
		clock:    time.Now,
		workerID: workerID,
	}
}

func (s *Scanner) RunOnce(ctx context.Context) (ScannerRunResult, error) {
	var result ScannerRunResult
	if s == nil || s.db == nil {
		return result, errors.New("resource update scanner db is nil")
	}
	now := s.clock().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created, err := scanAutoEvoSkillDrafts(ctx, tx, now)
		if err != nil {
			return err
		}
		result.SkillDraftTasksCreated = created
		created, err = scanMemoryReviewResults(ctx, tx, orm.ResourceUpdateResourceTypeMemory, now)
		if err != nil {
			return err
		}
		result.MemoryTasksCreated = created
		created, err = scanMemoryReviewResults(ctx, tx, orm.ResourceUpdateResourceTypeUserPreference, now)
		if err != nil {
			return err
		}
		result.UserPreferenceTasksCreated = created
		return nil
	})
	if err == nil && (result.SkillResultsExpired > 0 || result.SkillTasksCreated > 0 || result.SkillDraftTasksCreated > 0 || result.MemoryTasksCreated > 0 || result.UserPreferenceTasksCreated > 0) {
		resourceUpdateInfo(logEventResultScanDone).
			Int("skill_results_expired", result.SkillResultsExpired).
			Int("skill_tasks_created", result.SkillTasksCreated).
			Int("skill_draft_tasks_created", result.SkillDraftTasksCreated).
			Int("memory_tasks_created", result.MemoryTasksCreated).
			Int("user_preference_tasks_created", result.UserPreferenceTasksCreated).
			Msg(logEventResultScanDone)
	}
	return result, err
}

func ScanPendingResultsForResource(ctx context.Context, db *gorm.DB, resourceType, userID, resourceID string) error {
	if db == nil {
		return errors.New("resource update scanner db is nil")
	}
	now := time.Now().UTC()
	resourceType = strings.TrimSpace(resourceType)
	userID = strings.TrimSpace(userID)
	resourceID = strings.TrimSpace(resourceID)
	if userID == "" || resourceID == "" {
		return nil
	}
	resourceUpdateInfo(logEventAutoEvoScanStart).
		Str("resource_type", resourceType).
		Str("resource_id", resourceID).
		Str("user_id", userID).
		Msg(logEventAutoEvoScanStart)
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		switch resourceType {
		case orm.ResourceUpdateResourceTypeSkill:
			return nil
		case orm.ResourceUpdateResourceTypeMemory:
			return scanMemoryReviewResultsForResource(ctx, tx, orm.ResourceUpdateResourceTypeMemory, userID, resourceID, now)
		case orm.ResourceUpdateResourceTypeUserPreference:
			return scanMemoryReviewResultsForResource(ctx, tx, orm.ResourceUpdateResourceTypeUserPreference, userID, resourceID, now)
		default:
			return nil
		}
	})
	if err == nil {
		resourceUpdateInfo(logEventAutoEvoScanDone).
			Str("resource_type", resourceType).
			Str("resource_id", resourceID).
			Str("user_id", userID).
			Msg(logEventAutoEvoScanDone)
	}
	return err
}

func scanSkillReviewResults(ctx context.Context, tx *gorm.DB, now time.Time) (int, int, error) {
	var rows []SkillReviewResult
	if err := skillResultSelect(withUpdateLock(tx).WithContext(ctx)).
		Where("review_status = ?", reviewStatusPending).
		Order("userid ASC, skill_name ASC, time DESC, id DESC").
		Find(&rows).Error; err != nil {
		return 0, 0, err
	}
	return scanSkillReviewResultRows(ctx, tx, rows, now, reviewResultTrigger())
}

func scanSkillReviewResultsForResource(ctx context.Context, tx *gorm.DB, userID, resourceID string, now time.Time) error {
	if v2Resource, err := skillV2ResourceByID(ctx, tx, userID, resourceID); err == nil {
		if !v2Resource.AutoEvo {
			resourceUpdateInfo(logEventResultScanSkipped).
				Str("resource_type", orm.ResourceUpdateResourceTypeSkill).
				Str("resource_id", resourceID).
				Str("user_id", userID).
				Str("reason", "auto_evo_disabled").
				Msg(logEventResultScanSkipped)
			return nil
		}
		var rows []SkillReviewResult
		if err := skillResultSelect(withUpdateLock(tx).WithContext(ctx)).
			Where("review_status = ? AND type = ? AND userid = ? AND skill_name = ?",
				reviewStatusPending, skillReviewTypePatch, userID, v2Resource.SkillName).
			Order("time DESC, id DESC").
			Find(&rows).Error; err != nil {
			return err
		}
		_, _, err := scanSkillReviewResultRows(ctx, tx, rows, now, autoEvoTrigger(v2Resource.AutoEvoGeneration))
		return err
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	resourceUpdateInfo(logEventResultScanSkipped).
		Str("resource_type", orm.ResourceUpdateResourceTypeSkill).
		Str("resource_id", resourceID).
		Str("user_id", userID).
		Str("reason", "resource_not_found").
		Msg(logEventResultScanSkipped)
	return nil
}

func reviewResultTrigger() autoApplyTrigger {
	return autoApplyTrigger{TriggerType: orm.ResourceUpdateTriggerTypeReviewResult}
}

func autoEvoTrigger(generation int64) autoApplyTrigger {
	return autoApplyTrigger{TriggerType: orm.ResourceUpdateTriggerTypeAutoEvoEnabled, Generation: generation}
}

func scanSkillReviewResultRows(ctx context.Context, tx *gorm.DB, rows []SkillReviewResult, now time.Time, trigger autoApplyTrigger) (int, int, error) {
	seenPatch := map[string]string{}
	expireIDs := make([]string, 0)
	created := 0
	for _, row := range rows {
		row.UserID = strings.TrimSpace(row.UserID)
		row.SkillName = strings.TrimSpace(row.SkillName)
		row.Type = strings.TrimSpace(row.Type)
		if row.UserID == "" || row.SkillName == "" {
			resourceUpdateWarn(logEventResultScanSkipped, nil).
				Str("resource_type", orm.ResourceUpdateResourceTypeSkill).
				Str("review_result_id", row.ID).
				Str("user_id", row.UserID).
				Str("skill_name", row.SkillName).
				Str("reason", "missing_user_or_skill_name").
				Msg(logEventResultScanSkipped)
			continue
		}
		if row.Type == skillReviewTypeNew {
			if err := applyNewSkillReviewResult(ctx, tx, row, now); err != nil {
				if errors.Is(err, gorm.ErrDuplicatedKey) || errors.Is(err, errReviewInvalid) {
					expireIDs = append(expireIDs, row.ID)
					resourceUpdateWarn(logEventResultScanSkipped, err).
						Str("resource_type", orm.ResourceUpdateResourceTypeSkill).
						Str("review_result_id", row.ID).
						Str("user_id", row.UserID).
						Str("skill_name", row.SkillName).
						Str("reason", "new_skill_auto_create_invalid").
						Msg(logEventResultScanSkipped)
					continue
				}
				return 0, 0, err
			}
			continue
		}
		if row.Type != skillReviewTypePatch {
			continue
		}
		key := row.UserID + "\x00" + row.SkillName
		if seenPatch[key] != "" {
			expireIDs = append(expireIDs, row.ID)
			resourceUpdateInfo(logEventResultExpired).
				Str("resource_type", orm.ResourceUpdateResourceTypeSkill).
				Str("review_result_id", row.ID).
				Str("kept_review_result_id", seenPatch[key]).
				Str("user_id", row.UserID).
				Str("skill_name", row.SkillName).
				Str("reason", "older_pending_skill_patch").
				Msg(logEventResultExpired)
			continue
		}
		seenPatch[key] = row.ID
		resourceID := ""
		autoEvo := false
		generation := int64(0)
		v2Resource, err := mapSkillPatchResultToV2Resource(ctx, tx, row)
		if err == nil {
			resourceID = v2Resource.ID
			autoEvo = v2Resource.AutoEvo
			generation = v2Resource.AutoEvoGeneration
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, 0, err
		} else {
			expireIDs = append(expireIDs, row.ID)
			resourceUpdateInfo(logEventResultExpired).
				Str("resource_type", orm.ResourceUpdateResourceTypeSkill).
				Str("review_result_id", row.ID).
				Str("user_id", row.UserID).
				Str("skill_name", row.SkillName).
				Str("reason", "skill_patch_resource_not_found").
				Msg(logEventResultExpired)
			continue
		}
		if !autoEvo {
			resourceUpdateInfo(logEventResultScanSkipped).
				Str("resource_type", orm.ResourceUpdateResourceTypeSkill).
				Str("resource_id", resourceID).
				Str("review_result_id", row.ID).
				Str("user_id", row.UserID).
				Str("skill_name", row.SkillName).
				Str("reason", "auto_evo_disabled").
				Msg(logEventResultScanSkipped)
			continue
		}
		hasOwnedDraft, err := hasTaskOwnedSkillDraft(ctx, tx, resourceID)
		if err != nil {
			return 0, 0, err
		}
		if hasOwnedDraft {
			continue
		}
		if trigger.TriggerType == orm.ResourceUpdateTriggerTypeAutoEvoEnabled {
			trigger.Generation = generation
		}
		made, err := ensureAutoApplyTask(ctx, tx, orm.ResourceUpdateResourceTypeSkill, row.UserID, resourceID, row.ID, now, trigger)
		if err != nil {
			return 0, 0, err
		}
		if made {
			created++
		}
	}
	if len(expireIDs) == 0 {
		return 0, created, nil
	}
	result := tx.WithContext(ctx).
		Table("skill_review_results").
		Where("id IN ? AND review_status = ?", expireIDs, reviewStatusPending).
		Updates(map[string]any{"review_status": reviewStatusExpired})
	if result.Error != nil {
		return 0, 0, result.Error
	}
	return int(result.RowsAffected), created, nil
}

func scanAutoEvoSkillDrafts(ctx context.Context, tx *gorm.DB, now time.Time) (int, error) {
	var rows []struct {
		SkillID      string `gorm:"column:skill_id"`
		UserID       string `gorm:"column:user_id"`
		TaskID       string `gorm:"column:task_id"`
		DraftVersion int64  `gorm:"column:draft_version"`
	}
	if err := tx.WithContext(ctx).
		Table("skills AS s").
		Select("s.id AS skill_id, s.owner_user_id AS user_id, d.task_id, d.version AS draft_version").
		Joins("JOIN skill_drafts AS d ON d.skill_id = s.id").
		Where("s.auto_evo = ? AND s.deleted_at IS NULL AND d.task_id <> '' AND EXISTS (SELECT 1 FROM skill_draft_entries e WHERE e.skill_id = s.id)", true).
		Order("s.owner_user_id ASC, s.id ASC").
		Find(&rows).Error; err != nil {
		return 0, err
	}
	created := 0
	for _, row := range rows {
		var count int64
		if err := tx.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).
			Where("task_type = ? AND resource_type = ? AND resource_id = ? AND status IN ?",
				orm.ResourceUpdateTaskTypeAutoCommitSkillDraft,
				orm.ResourceUpdateResourceTypeSkill,
				row.SkillID,
				activeAutoApplyStatuses()).
			Count(&count).Error; err != nil {
			return 0, err
		}
		if count > 0 {
			continue
		}
		requestBody, err := json.Marshal(skillDraftAutoCommitRequestJSON{TaskID: row.TaskID, DraftVersion: row.DraftVersion})
		if err != nil {
			return 0, err
		}
		task := orm.ResourceUpdateTask{
			ID:           common.GenerateID(),
			TaskType:     orm.ResourceUpdateTaskTypeAutoCommitSkillDraft,
			ResourceType: orm.ResourceUpdateResourceTypeSkill,
			UserID:       strings.TrimSpace(row.UserID),
			ResourceID:   strings.TrimSpace(row.SkillID),
			TriggerType:  orm.ResourceUpdateTriggerTypeAutoEvoEnabled,
			TriggerID:    fmt.Sprintf("skill_draft:%s:%s:%d", row.SkillID, row.TaskID, row.DraftVersion),
			Status:       orm.ResourceUpdateTaskStatusPending,
			RequestJSON:  requestBody,
			NextRunAt:    now,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := tx.WithContext(ctx).Create(&task).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				continue
			}
			return 0, err
		}
		created++
	}
	return created, nil
}

func hasTaskOwnedSkillDraft(ctx context.Context, tx *gorm.DB, skillID string) (bool, error) {
	var count int64
	err := tx.WithContext(ctx).
		Table("skill_drafts AS d").
		Where("d.skill_id = ? AND d.task_id <> '' AND EXISTS (SELECT 1 FROM skill_draft_entries e WHERE e.skill_id = d.skill_id)", skillID).
		Count(&count).Error
	return count > 0, err
}

func applyNewSkillReviewResult(ctx context.Context, tx *gorm.DB, row SkillReviewResult, now time.Time) error {
	if _, err := createSkillV2FromNewResult(ctx, tx, row, ""); err != nil {
		return err
	}
	return updateSkillReviewStatus(ctx, tx, row.ID, reviewStatusAccepted)
}

func scanMemoryReviewResults(ctx context.Context, tx *gorm.DB, target string, now time.Time) (int, error) {
	var rows []MemoryReviewResult
	if err := memoryResultSelect(withUpdateLock(tx).WithContext(ctx)).
		Where("target = ? AND user_id <> '' AND state = ? AND review_status = ?", target, memoryReviewStateSuccess, reviewStatusPending).
		Order("time ASC, id ASC").
		Find(&rows).Error; err != nil {
		return 0, err
	}
	created := 0
	for _, row := range rows {
		resourceID, autoEvo, err := mapMemoryReviewResultResource(ctx, tx, target, row)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				resourceUpdateInfo(logEventResultScanSkipped).
					Str("resource_type", target).
					Str("review_result_id", row.ID).
					Str("user_id", strings.TrimSpace(row.UserID)).
					Str("session_id", strings.TrimSpace(row.SessionID)).
					Str("reason", "resource_not_found").
					Msg(logEventResultScanSkipped)
				continue
			}
			return 0, err
		}
		if !autoEvo {
			resourceUpdateInfo(logEventResultScanSkipped).
				Str("resource_type", target).
				Str("resource_id", resourceID).
				Str("review_result_id", row.ID).
				Str("user_id", strings.TrimSpace(row.UserID)).
				Str("session_id", strings.TrimSpace(row.SessionID)).
				Str("reason", "auto_evo_disabled").
				Msg(logEventResultScanSkipped)
			continue
		}
		made, err := ensureAutoApplyTask(ctx, tx, target, strings.TrimSpace(row.UserID), resourceID, row.ID, now, reviewResultTrigger())
		if err != nil {
			return 0, err
		}
		if made {
			created++
		}
	}
	return created, nil
}

func scanMemoryReviewResultsForResource(ctx context.Context, tx *gorm.DB, target, userID, resourceID string, now time.Time) error {
	var rows []MemoryReviewResult
	if err := memoryResultSelect(withUpdateLock(tx).WithContext(ctx)).
		Where("target = ? AND user_id = ? AND state = ? AND review_status = ?", target, userID, memoryReviewStateSuccess, reviewStatusPending).
		Order("time ASC, id ASC").
		Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		mappedID, autoEvo, err := mapMemoryReviewResultResource(ctx, tx, target, row)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				resourceUpdateInfo(logEventResultScanSkipped).
					Str("resource_type", target).
					Str("resource_id", resourceID).
					Str("review_result_id", row.ID).
					Str("user_id", strings.TrimSpace(row.UserID)).
					Str("session_id", strings.TrimSpace(row.SessionID)).
					Str("reason", "resource_not_found").
					Msg(logEventResultScanSkipped)
				continue
			}
			return err
		}
		if mappedID != resourceID {
			resourceUpdateInfo(logEventResultScanSkipped).
				Str("resource_type", target).
				Str("resource_id", resourceID).
				Str("mapped_resource_id", mappedID).
				Str("review_result_id", row.ID).
				Str("user_id", strings.TrimSpace(row.UserID)).
				Str("session_id", strings.TrimSpace(row.SessionID)).
				Str("reason", "resource_mismatch").
				Msg(logEventResultScanSkipped)
			continue
		}
		if !autoEvo {
			resourceUpdateInfo(logEventResultScanSkipped).
				Str("resource_type", target).
				Str("resource_id", resourceID).
				Str("review_result_id", row.ID).
				Str("user_id", strings.TrimSpace(row.UserID)).
				Str("session_id", strings.TrimSpace(row.SessionID)).
				Str("reason", "auto_evo_disabled").
				Msg(logEventResultScanSkipped)
			continue
		}
		generation, err := currentAutoEvoGeneration(tx, target, resourceID)
		if err != nil {
			return err
		}
		if _, err := ensureAutoApplyTask(ctx, tx, target, userID, resourceID, row.ID, now, autoEvoTrigger(generation)); err != nil {
			return err
		}
	}
	return nil
}

func mapMemoryReviewResultResource(ctx context.Context, tx *gorm.DB, target string, row MemoryReviewResult) (string, bool, error) {
	switch target {
	case orm.ResourceUpdateResourceTypeMemory:
		resource, err := mapMemoryReviewResultToPersonalResource(tx.WithContext(ctx), target, row)
		return resource.ID, resource.AutoEvo, err
	case orm.ResourceUpdateResourceTypeUserPreference:
		resource, err := mapMemoryReviewResultToPersonalResource(tx.WithContext(ctx), target, row)
		return resource.ID, resource.AutoEvo, err
	default:
		return "", false, fmt.Errorf("unsupported review target %q", target)
	}
}

func ensureAutoApplyTask(ctx context.Context, tx *gorm.DB, resourceType, userID, resourceID, reviewResultID string, now time.Time, trigger autoApplyTrigger) (bool, error) {
	resourceType = strings.TrimSpace(resourceType)
	userID = strings.TrimSpace(userID)
	resourceID = strings.TrimSpace(resourceID)
	reviewResultID = strings.TrimSpace(reviewResultID)
	if resourceType == "" || userID == "" || resourceID == "" || reviewResultID == "" {
		resourceUpdateWarn(logEventResultScanSkipped, nil).
			Str("resource_type", resourceType).
			Str("resource_id", resourceID).
			Str("user_id", userID).
			Str("review_result_id", reviewResultID).
			Str("reason", "missing_auto_apply_task_key").
			Msg(logEventResultScanSkipped)
		return false, nil
	}
	triggerType, triggerID := autoApplyTriggerID(resourceType, resourceID, reviewResultID, trigger)
	var activeTask orm.ResourceUpdateTask
	err := tx.WithContext(ctx).
		Where("task_type = ? AND resource_type = ? AND review_result_id = ? AND status IN ?",
			orm.ResourceUpdateTaskTypeAutoApplyReview, resourceType, reviewResultID, activeAutoApplyStatuses()).
		Order("created_at ASC").
		Take(&activeTask).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if err == nil {
		resourceUpdateInfo(logEventAutoApplyTaskBlocked).
			Str("task_id", activeTask.ID).
			Str("resource_type", resourceType).
			Str("resource_id", resourceID).
			Str("user_id", userID).
			Str("task_status", activeTask.Status).
			Str("review_result_id", reviewResultID).
			Time("next_run_at", activeTask.NextRunAt).
			Time("locked_until", timeOrZero(activeTask.LockedUntil)).
			Str("reason", "active_auto_apply_exists").
			Msg(logEventAutoApplyTaskBlocked)
		return false, nil
	}
	var failedTask orm.ResourceUpdateTask
	err = tx.WithContext(ctx).
		Where("task_type = ? AND resource_type = ? AND review_result_id = ? AND status = ?",
			orm.ResourceUpdateTaskTypeAutoApplyReview, resourceType, reviewResultID, orm.ResourceUpdateTaskStatusFailed).
		Order("updated_at DESC").
		Take(&failedTask).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if err == nil {
		resourceUpdateWarn(logEventAutoApplyTaskBlocked, nil).
			Str("task_id", failedTask.ID).
			Str("resource_type", resourceType).
			Str("resource_id", resourceID).
			Str("user_id", userID).
			Str("task_status", failedTask.Status).
			Str("review_result_id", reviewResultID).
			Str("error_code", failedTask.ErrorCode).
			Int("attempt_count", failedTask.AttemptCount).
			Str("reason", "failed_auto_apply_exists").
			Msg(logEventAutoApplyTaskBlocked)
		return false, nil
	}
	var existing orm.ResourceUpdateTask
	err = tx.WithContext(ctx).
		Where("task_type = ? AND resource_type = ? AND trigger_type = ? AND trigger_id = ?",
			orm.ResourceUpdateTaskTypeAutoApplyReview, resourceType, triggerType, triggerID).
		Take(&existing).Error
	if err == nil {
		if existing.Status == orm.ResourceUpdateTaskStatusDone || existing.Status == orm.ResourceUpdateTaskStatusFailed {
			resourceUpdateInfo(logEventAutoApplyTaskBlocked).
				Str("task_id", existing.ID).
				Str("resource_type", resourceType).
				Str("resource_id", resourceID).
				Str("user_id", userID).
				Str("trigger_type", triggerType).
				Str("trigger_id", triggerID).
				Str("review_result_id", reviewResultID).
				Str("task_status", existing.Status).
				Str("reason", "existing_trigger_terminal").
				Msg(logEventAutoApplyTaskBlocked)
			return false, nil
		}
		updates := map[string]any{
			"user_id":          userID,
			"resource_id":      resourceID,
			"review_result_id": reviewResultID,
			"status":           orm.ResourceUpdateTaskStatusPending,
			"error_code":       "",
			"error_message":    "",
			"next_run_at":      now,
			"locked_by":        "",
			"locked_until":     nil,
			"started_at":       nil,
			"finished_at":      nil,
			"updated_at":       now,
		}
		if err := tx.WithContext(ctx).Model(&orm.ResourceUpdateTask{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
			return false, err
		}
		resourceUpdateInfo(logEventAutoApplyTaskCreated).
			Str("task_id", existing.ID).
			Str("resource_type", resourceType).
			Str("resource_id", resourceID).
			Str("user_id", userID).
			Str("trigger_type", triggerType).
			Str("trigger_id", triggerID).
			Str("review_result_id", reviewResultID).
			Str("reason", "reactivated_existing_task").
			Time("next_run_at", now).
			Msg(logEventAutoApplyTaskCreated)
		return true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	task := orm.ResourceUpdateTask{
		ID:             common.GenerateID(),
		TaskType:       orm.ResourceUpdateTaskTypeAutoApplyReview,
		ResourceType:   resourceType,
		UserID:         userID,
		ResourceID:     resourceID,
		TriggerType:    triggerType,
		TriggerID:      triggerID,
		Status:         orm.ResourceUpdateTaskStatusPending,
		ReviewResultID: reviewResultID,
		NextRunAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := tx.WithContext(ctx).Create(&task).Error; err != nil {
		return false, err
	}
	resourceUpdateInfo(logEventAutoApplyTaskCreated).
		Str("task_id", task.ID).
		Str("resource_type", resourceType).
		Str("resource_id", resourceID).
		Str("user_id", userID).
		Str("trigger_type", triggerType).
		Str("trigger_id", triggerID).
		Str("review_result_id", reviewResultID).
		Time("next_run_at", now).
		Msg(logEventAutoApplyTaskCreated)
	return true, nil
}

func autoApplyTriggerID(resourceType, resourceID, reviewResultID string, trigger autoApplyTrigger) (string, string) {
	if trigger.TriggerType == orm.ResourceUpdateTriggerTypeAutoEvoEnabled {
		return trigger.TriggerType, strings.Join([]string{
			resourceType,
			resourceID,
			reviewResultID,
			strconv.FormatInt(trigger.Generation, 10),
		}, ":")
	}
	tableName := "memory_review"
	if resourceType == orm.ResourceUpdateResourceTypeSkill {
		tableName = "skill_review_results"
	}
	return orm.ResourceUpdateTriggerTypeReviewResult, tableName + ":" + reviewResultID
}

func currentAutoEvoGeneration(tx *gorm.DB, resourceType, resourceID string) (int64, error) {
	var row struct {
		AutoEvoGeneration int64 `gorm:"column:auto_evo_generation"`
	}
	var err error
	switch resourceType {
	case orm.ResourceUpdateResourceTypeMemory:
		err = tx.Model(&orm.PersonalResource{}).Select("auto_evo_generation").Where("id = ? AND resource_type = ?", resourceID, resourceType).Take(&row).Error
	case orm.ResourceUpdateResourceTypeUserPreference:
		err = tx.Model(&orm.PersonalResource{}).Select("auto_evo_generation").Where("id = ? AND resource_type = ?", resourceID, resourceType).Take(&row).Error
	case orm.ResourceUpdateResourceTypeSkill:
		err = tx.Model(&orm.SkillV2Skill{}).Select("auto_evo_generation").Where("id = ?", resourceID).Take(&row).Error
	default:
		return 0, fmt.Errorf("unsupported resource type %q", resourceType)
	}
	return row.AutoEvoGeneration, err
}
