package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/resourcefs"
)

var errPersonalDraftAutoCommitStale = errors.New("personal resource draft changed before auto commit")

func (w *Worker) handleAutoCommitPersonalDraft(ctx context.Context, task orm.ResourceUpdateTask) taskOutcome {
	var request personalDraftAutoCommitRequestJSON
	if len(task.RequestJSON) == 0 || json.Unmarshal(task.RequestJSON, &request) != nil {
		return permanentOutcome("invalid_request_json", "auto commit task requires task_id and draft_version")
	}
	if strings.TrimSpace(request.TaskID) == "" || request.DraftVersion <= 0 {
		return permanentOutcome("invalid_request_json", "auto commit task requires task_id and draft_version")
	}
	if task.ResourceType != orm.ResourceUpdateResourceTypeMemory && task.ResourceType != orm.ResourceUpdateResourceTypeUserPreference {
		return permanentOutcome("invalid_resource_type", "auto commit task requires a personal resource")
	}

	var revisionID string
	err := w.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var resource orm.PersonalResource
		if err := withUpdateLock(tx).
			Where("id = ? AND user_id = ? AND resource_type = ?", task.ResourceID, task.UserID, task.ResourceType).
			Take(&resource).Error; err != nil {
			return err
		}
		if !resource.AutoEvo {
			return fmt.Errorf("%w: personal resource auto_evo disabled", errReviewConflict)
		}
		var draft orm.PersonalResourceDraft
		if err := withUpdateLock(tx).Where("resource_id = ?", resource.ID).Take(&draft).Error; err != nil {
			return err
		}
		if draft.TaskID != request.TaskID || draft.Version != request.DraftVersion || strings.HasPrefix(draft.TaskID, "memory_review_") {
			return errPersonalDraftAutoCommitStale
		}
		if status := strings.TrimSpace(draft.DraftStatus); status != "pending_confirm" && status != "auto_pending" {
			return errPersonalDraftAutoCommitStale
		}
		resp, err := resourcefs.NewService(resourcefs.ServiceDeps{DB: tx}).CommitDraft(ctx, resourcefs.CommitDraftRequest{
			Ref:                  resourcefs.ResourceRef{UserID: task.UserID, ResourceType: resourcefs.ResourceType(task.ResourceType)},
			Message:              "auto commit personal resource draft",
			SourceRefType:        "remote_fs_auto_update",
			SourceRefID:          request.TaskID,
			ExpectedDraftVersion: request.DraftVersion,
			CreatedBy:            task.UserID,
		})
		if err != nil {
			return err
		}
		revisionID = resp.RevisionID
		return nil
	})
	if errors.Is(err, errPersonalDraftAutoCommitStale) || errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, errReviewConflict) || errors.Is(err, resourcefs.ErrConflict) || errors.Is(err, resourcefs.ErrDraftNotFound) {
		return taskOutcome{Status: orm.ResourceUpdateTaskStatusSkipped, ErrorCode: "personal_draft_changed", ErrorMessage: err.Error()}
	}
	if err != nil {
		return retryableOutcome("personal_draft_auto_commit_failed", err)
	}
	return taskOutcome{Status: orm.ResourceUpdateTaskStatusDone, ResultID: revisionID}
}
