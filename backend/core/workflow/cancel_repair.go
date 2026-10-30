package workflow

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// CancelWorkflowDraftRepair retires the current repair owner and its queued job
// atomically. Bumping the version also fences older repair handlers that use
// version-based writes, so a late model response cannot replace the draft.
func CancelWorkflowDraftRepair(w http.ResponseWriter, r *http.Request) {
	draftID, userID := common.PathVar(r, "draft_id"), common.UserID(r)
	if draftID == "" || userID == "" {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	db := store.DB()
	var draft orm.WorkflowDraft
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND created_by = ? AND deleted_at IS NULL", draftID, userID).First(&draft).Error; err != nil {
			return err
		}
		if draft.GenerateStatus != generateStatusRepairing {
			return errRepairStaleDraft
		}
		var run orm.WorkflowRepairRun
		if err := tx.Where("draft_id = ? AND status IN ?", draftID, []string{"queued", "repairing"}).Order("created_at DESC").First(&run).Error; err != nil {
			return err
		}
		var jobs []orm.AsyncJob
		if err := tx.Where("job_type = ? AND resource_type = ? AND resource_id = ? AND status IN ?", workflowDraftRepairJobType, "workflow_draft", draftID, []string{string(asyncjob.StatusPending), string(asyncjob.StatusRunning)}).Find(&jobs).Error; err != nil {
			return err
		}
		var payload workflowDraftRepairPayload
		found := false
		for _, job := range jobs {
			if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
				return err
			}
			if payload.RepairRunID == run.ID && payload.DraftID == draftID {
				found = true
				break
			}
		}
		if !found {
			return errRepairStaleDraft
		}
		if _, err := asyncjob.CancelResourceJobs(r.Context(), tx, workflowDraftRepairJobType, "workflow_draft", draftID, "workflow repair canceled by user"); err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&run).Updates(map[string]any{
			"status": "canceled", "updated_at": now,
			"diagnostics_after_json": diagnosticsJSON([]repairDiagnostic{{Code: "E_REPAIR_CANCELED", Severity: "info", Message: "用户已停止本次修复，未应用修复结果。"}}),
		}).Error; err != nil {
			return err
		}
		return tx.Model(&draft).Updates(map[string]any{"generate_status": payload.PrevStatus, "version": draft.Version + 1, "updated_at": now}).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, errRepairStaleDraft) {
		common.ReplyErr(w, "workflow is not repairing", http.StatusConflict)
		return
	}
	if err != nil {
		common.ReplyErr(w, "cancel repair failed", http.StatusInternalServerError)
		return
	}
	if err := db.WithContext(r.Context()).Where("id = ? AND created_by = ? AND deleted_at IS NULL", draftID, userID).First(&draft).Error; err != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, toEnrichedDraftResponse(db, draft))
}
