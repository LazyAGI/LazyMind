package controlstore

import (
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
	"time"
)

// PrepareMaterialEdit preserves the producer and invalidates only consumers of
// the replaced material. The caller holds the session lock and writes atomically.
func PrepareMaterialEdit(tx *gorm.DB, session *orm.WorkflowSession, slot string) error {
	if err := GuardMaterialEdit(tx, *session, slot); err != nil {
		return err
	}
	var revisions []orm.WorkflowSlotRevision
	if err := tx.Where("session_id = ? AND slot_id = ? AND selected = ? AND validity = ?", session.ID, slot, true, "effective").Find(&revisions).Error; err != nil {
		return err
	}
	if len(revisions) == 0 {
		return nil
	}
	var consumers []orm.WorkflowSessionStep
	ids := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		ids = append(ids, revision.ID)
	}
	if err := tx.Where("session_id = ? AND validity = 'effective' AND id IN (?)", session.ID,
		tx.Model(&orm.WorkflowAttemptInputBinding{}).Select("attempt_id").Where("material_revision_id IN ?", ids)).Find(&consumers).Error; err != nil {
		return err
	}
	var decisions []orm.WorkflowRouteDecision
	if err := tx.Where("session_id = ? AND validity = 'effective'", session.ID).Find(&decisions).Error; err != nil {
		return err
	}
	for _, decision := range decisions {
		var witnesses []graphengine.Witness
		if err := json.Unmarshal(decision.WitnessJSON, &witnesses); err != nil {
			return err
		}
		affected := false
		for _, w := range witnesses {
			for _, id := range ids {
				if w.RevisionID == id {
					affected = true
				}
			}
		}
		if !affected {
			continue
		}
		if err := EnqueueExclusiveRouteAttempts(tx, session.ID, decision, &consumers); err != nil {
			return err
		}
		if err := tx.Model(&orm.WorkflowRouteDecision{}).Where("id = ?", decision.ID).Update("validity", "stale").Error; err != nil {
			return err
		}
	}
	if err := InvalidateAttempts(tx, session, consumers); err != nil {
		return err
	}
	if Controlled(*session) {
		// Editing accepted output reopens its review; superseded downstream reviews
		// must not prevent the revised material from being used on continuation.
		stale := tx.Model(&orm.WorkflowSessionStep{}).Select("id").Where("session_id = ? AND validity <> 'effective'", session.ID)
		if err := tx.Model(&orm.WorkflowReviewCheckpoint{}).Where("session_id = ? AND attempt_id IN (?) AND status IN ?", session.ID, stale, []string{"pending", "accepted"}).Update("status", "superseded").Error; err != nil {
			return err
		}
		for _, revision := range revisions {
			if err := tx.Model(&orm.WorkflowReviewCheckpoint{}).Where("session_id = ? AND step_id = ? AND status = 'accepted'", session.ID, revision.StepID).Update("status", "pending").Error; err != nil {
				return err
			}
		}
		if err := ConsumeContinuation(tx, session.ID, ""); err != nil {
			return err
		}
	}
	binding, err := DecodeBinding(*session)
	if err != nil {
		return err
	}
	binding.EditPaused = true
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	session.ControlBindingJSON = string(raw)
	if err := tx.Model(session).Update("control_binding_json", session.ControlBindingJSON).Error; err != nil {
		return err
	}

	now := time.Now().UTC()
	if session.Status != "stopped" {
		session.Status = "waiting"
	}
	session.LastStoppedAt = &now
	return tx.Model(session).Updates(map[string]any{"status": session.Status, "last_stopped_at": now}).Error
}

func ClearEditPause(tx *gorm.DB, session *orm.WorkflowSession) error {
	binding, err := DecodeBinding(*session)
	if err != nil {
		return err
	}
	if !binding.EditPaused {
		return nil
	}
	binding.EditPaused = false
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	session.ControlBindingJSON = string(raw)
	return tx.Model(session).Update("control_binding_json", session.ControlBindingJSON).Error
}

func EditPaused(session orm.WorkflowSession) bool {
	binding, err := DecodeBinding(session)
	return err == nil && binding.EditPaused
}
