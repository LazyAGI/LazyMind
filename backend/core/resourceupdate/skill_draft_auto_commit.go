package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	skillrevision "lazymind/core/skillv2/revision"
	"lazymind/core/skillv2/taskguard"
)

var errSkillDraftAutoCommitStale = errors.New("skill draft changed before auto commit")

func (w *Worker) handleAutoCommitSkillDraft(ctx context.Context, task orm.ResourceUpdateTask) taskOutcome {
	var request skillDraftAutoCommitRequestJSON
	if len(task.RequestJSON) == 0 || json.Unmarshal(task.RequestJSON, &request) != nil {
		return permanentOutcome("invalid_request_json", "auto commit task requires task_id and draft_version")
	}
	if strings.TrimSpace(request.TaskID) == "" || request.DraftVersion <= 0 {
		return permanentOutcome("invalid_request_json", "auto commit task requires task_id and draft_version")
	}
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
	if decision.DraftTaskID != request.TaskID || decision.DraftVersion != request.DraftVersion {
		return taskOutcome{Status: orm.ResourceUpdateTaskStatusSkipped, ErrorCode: "skill_draft_changed", ErrorMessage: errSkillDraftAutoCommitStale.Error()}
	}

	var revisionID string
	err = w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var skill orm.SkillV2Skill
		if err := withUpdateLock(tx).Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", task.ResourceID, task.UserID).Take(&skill).Error; err != nil {
			return err
		}
		if !skill.AutoEvo {
			return fmt.Errorf("%w: skill auto_evo disabled", errReviewConflict)
		}
		var draft orm.SkillV2Draft
		if err := withUpdateLock(tx).Where("skill_id = ?", task.ResourceID).Take(&draft).Error; err != nil {
			return err
		}
		var entryCount int64
		if err := tx.Model(&orm.SkillV2DraftEntry{}).Where("skill_id = ?", task.ResourceID).Count(&entryCount).Error; err != nil {
			return err
		}
		if entryCount == 0 || strings.TrimSpace(draft.TaskID) != request.TaskID || draft.Version != request.DraftVersion {
			return errSkillDraftAutoCommitStale
		}
		resp, err := newSkillV2RevisionService(tx).CommitDraft(ctx, skillrevision.CommitDraftRequest{
			SkillID:      task.ResourceID,
			UserID:       task.UserID,
			DraftVersion: request.DraftVersion,
		})
		if err != nil {
			return err
		}
		revisionID = resp.RevisionID
		if strings.HasPrefix(request.TaskID, "review_") {
			if err := acceptSkillReviewResultsForDraftTask(ctx, tx, task.UserID, request.TaskID); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errSkillDraftAutoCommitStale) || errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, errReviewConflict) {
		return taskOutcome{Status: orm.ResourceUpdateTaskStatusSkipped, ErrorCode: "skill_draft_changed", ErrorMessage: err.Error()}
	}
	if err != nil {
		return retryableOutcome("skill_draft_auto_commit_failed", err)
	}
	return taskOutcome{Status: orm.ResourceUpdateTaskStatusDone, ResultID: revisionID}
}

func acceptSkillReviewResultsForDraftTask(ctx context.Context, tx *gorm.DB, userID, taskID string) error {
	requestID := strings.TrimSpace(taskID)
	var stats struct {
		RequestID string `gorm:"column:requestid"`
	}
	err := tx.WithContext(ctx).Table("skill_review_stats").
		Select("requestid").
		Where("userid = ? AND (id = ? OR requestid = ?)", strings.TrimSpace(userID), requestID, requestID).
		Order("started_at DESC, id DESC").
		Take(&stats).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if strings.TrimSpace(stats.RequestID) != "" {
		requestID = strings.TrimSpace(stats.RequestID)
	}
	return tx.WithContext(ctx).Table("skill_review_results").
		Where("userid = ? AND requestid = ? AND review_status = ?", strings.TrimSpace(userID), requestID, reviewStatusPending).
		Update("review_status", reviewStatusAccepted).Error
}

func newSkillV2RevisionService(db *gorm.DB) *skillrevision.Service {
	root := strings.TrimSpace(os.Getenv("LAZYMIND_SKILL_OBJECT_ROOT"))
	if root == "" {
		root = filepath.Join(uploadRootForSkillV2Bridge(), "skill-objects")
	}
	return skillrevision.NewService(skillrevision.ServiceDeps{
		DB:        db,
		BlobStore: skillrevision.NewBlobStore(db, skillrevision.NewLocalObjectStore(root)),
	})
}
