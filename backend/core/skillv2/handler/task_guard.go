package handler

import (
	"net/http"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/skillv2/taskguard"
	"lazymind/core/store"
)

func MaintenanceTaskStatus(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	userID, _, ok := requireUser(w, r)
	if !ok {
		return
	}
	decision, err := taskguard.EvaluateSkillOperation(r.Context(), db, store.State(), taskguard.SkillOperationRequest{
		UserID:    userID,
		Operation: taskguard.TriggerSkillReview,
	})
	if err != nil {
		common.ReplyErrWithData(w, "无法查询 Skill 维护任务状态", map[string]any{"code": taskguard.ReasonTaskStatusUnavailable}, http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, map[string]any{
		"has_active_task": !decision.Allowed,
		"task":            decision.BlockingTask,
		"message":         decision.Message,
	})
}

func ensureUserDraftWriteAllowed(w http.ResponseWriter, r *http.Request, db *gorm.DB, userID, skillID string) bool {
	decision, err := taskguard.EvaluateSkillOperation(r.Context(), db, store.State(), taskguard.SkillOperationRequest{
		UserID:    userID,
		SkillID:   skillID,
		Operation: taskguard.StartUserEdit,
	})
	if err != nil {
		replyTaskGuardUnavailable(w, decision)
		return false
	}
	if !decision.Allowed {
		replyTaskGuardBlocked(w, decision)
		return false
	}
	if err := takeOverUserDraft(r, db, userID, skillID); err != nil {
		replyServiceError(w, err)
		return false
	}
	return true
}

func takeOverUserDraft(r *http.Request, db *gorm.DB, userID, skillID string) error {
	return db.WithContext(r.Context()).Model(&orm.SkillV2Draft{}).
		Where("skill_id = ? AND task_id <> '' AND EXISTS (SELECT 1 FROM skill_draft_entries WHERE skill_draft_entries.skill_id = skill_drafts.skill_id)", skillID).
		Updates(map[string]any{
			"task_id":         "",
			"conversation_id": nil,
			"updated_by":      strings.TrimSpace(userID),
		}).Error
}

func replyTaskGuardBlocked(w http.ResponseWriter, decision taskguard.SkillOperationDecision) {
	common.ReplyErrWithData(w, decision.Message, map[string]any{
		"code":            decision.ReasonCode,
		"blocking_task":   decision.BlockingTask,
		"blocking_skills": decision.BlockingSkills,
	}, http.StatusConflict)
}

func replyTaskGuardUnavailable(w http.ResponseWriter, decision taskguard.SkillOperationDecision) {
	message := decision.Message
	if strings.TrimSpace(message) == "" {
		message = "无法确认 Skill 任务状态"
	}
	code := decision.ReasonCode
	if strings.TrimSpace(code) == "" {
		code = taskguard.ReasonTaskStatusUnavailable
	}
	common.ReplyErrWithData(w, message, map[string]any{"code": code}, http.StatusServiceUnavailable)
}
