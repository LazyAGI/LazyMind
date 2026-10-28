package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/log"
)

// The queue owns lease expiry; this reconciler owns the corresponding draft
// transition. It also runs after restart, when the original handler cannot
// execute its failure cleanup. It never retries an AI request automatically.
func StartWorkflowRepairRecovery(ctx context.Context, db *gorm.DB) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if err := recoverWorkflowRepairs(ctx, db); err != nil && ctx.Err() == nil {
				log.Logger.Warn().Err(err).Msg("workflow repair recovery failed")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func recoverWorkflowRepairs(ctx context.Context, db *gorm.DB) error {
	db = db.WithContext(ctx)
	var runs []orm.WorkflowRepairRun
	if err := db.Where("target = ? AND status IN ? AND draft_id IN (?)", "node_integration", []string{"queued", "repairing"},
		db.Model(&orm.WorkflowDraft{}).Select("id").Where("generate_status = ? AND deleted_at IS NULL", generateStatusRepairing)).Find(&runs).Error; err != nil {
		return err
	}
	for _, run := range runs {
		var jobs []orm.AsyncJob
		if err := db.Where("job_type = ? AND resource_type = ? AND resource_id = ?", workflowDraftRepairJobType, "workflow_draft", run.DraftID).Order("created_at DESC").Limit(1).Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 || (jobs[0].Status != string(asyncjob.StatusFailed) && jobs[0].Status != string(asyncjob.StatusCanceled)) {
			continue
		}
		var payload workflowDraftRepairPayload
		if err := json.Unmarshal(jobs[0].PayloadJSON, &payload); err != nil {
			return fmt.Errorf("decode interrupted workflow repair %s: %w", jobs[0].ID, err)
		}
		if payload.RepairRunID != run.ID || payload.DraftID != run.DraftID {
			continue
		}
		if err := finishNodeIntegrationFailure(db, payload, "failed", "E_REPAIR_INTERRUPTED", "后台修复任务已中断，本次没有应用修改。草稿已恢复编辑，请重新修复。"); err != nil {
			return err
		}
	}
	return nil
}
