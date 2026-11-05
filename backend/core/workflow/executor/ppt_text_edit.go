package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// A text-only edit explicitly reuses the rest of the deck. Rewind invalidates
// the previous producer, so copy its visible pages/notes into the new attempt.
// Never revive the old attempt, deleted pages, or overwrite selected user edits.
// The caller holds the publication transaction and session lock.
func carryPPTTextEditPages(tx *gorm.DB, session *orm.WorkflowSession, attempt AttemptContext, editedIndex int, now time.Time) error {
	for _, slot := range []string{"preview_html", "preview_notes"} {
		var order orm.WorkflowSlotOrder
		if err := tx.Where("session_id = ? AND slot_id = ?", session.ID, slot).First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		var indices []int
		if err := json.Unmarshal(order.OrderList, &indices); err != nil {
			return err
		}
		for _, index := range indices {
			if slot == "preview_html" && index == editedIndex {
				continue
			}
			var selected int64
			if err := tx.Model(&orm.WorkflowSlotRevision{}).Where(
				"session_id = ? AND slot_id = ? AND list_index = ? AND selected = ?",
				session.ID, slot, index, true).Count(&selected).Error; err != nil {
				return err
			}
			if selected > 0 {
				continue
			}
			var previous orm.WorkflowSlotRevision
			if err := tx.Where("session_id = ? AND slot_id = ? AND list_index = ?", session.ID, slot, index).
				Order("revision DESC").First(&previous).Error; err != nil {
				return err
			}
			if previous.Validity != "stale" || previous.StepID != attempt.StepID {
				continue
			}
			value := orm.WorkflowHumanArtifact{ContentType: "text", Value: previous.ContentSnapshot}
			if previous.HumanArtifactID != nil {
				if err := tx.First(&value, "id = ?", *previous.HumanArtifactID).Error; err != nil {
					return err
				}
			} else if previous.ArtifactSeq != nil {
				var producer orm.WorkflowSessionStep
				if err := tx.Where("session_id = ? AND step_id = ? AND attempt = ?",
					session.ID, previous.StepID, previous.Attempt).First(&producer).Error; err != nil {
					return err
				}
				var artifact orm.SubAgentArtifact
				if err := tx.Where("task_id = ? AND slot = ? AND seq = ? AND hidden = ?",
					producer.TaskID, previous.Slot, *previous.ArtifactSeq, false).First(&artifact).Error; err != nil {
					return err
				}
				value.ContentType, value.Value, value.Caption = artifact.ContentType, artifact.Value, artifact.Caption
			}
			if len(value.Value) == 0 {
				return fmt.Errorf("missing historical PPT content: %s[%d]", slot, index)
			}
			value.ID, value.SessionID, value.Slot = uuid.NewString(), session.ID, slot
			value.DraftVersion, value.CreatedAt = 1, now
			if err := tx.Create(&value).Error; err != nil {
				return err
			}
			row := orm.WorkflowSlotRevision{ID: uuid.NewString(), SessionID: session.ID,
				SlotID: slot, Slot: slot, ListIndex: &index, Revision: previous.Revision + 1,
				Selected: true, Validity: "effective", HumanArtifactID: &value.ID,
				ProducerAttemptID: attempt.AttemptID, StepID: attempt.StepID, Attempt: attempt.AttemptNo,
				ChangeSource: "host", CreatedAt: now}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			session.StateVersion++
			if err := tx.Model(session).Updates(map[string]any{"state_version": session.StateVersion, "updated_at": now}).Error; err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]any{"artifact_id": row.ID, "attempt_id": attempt.AttemptID,
				"slot": slot, "revision": row.Revision, "list_index": index, "state_version": session.StateVersion})
			if err := tx.Create(&orm.WorkflowEvent{SessionID: session.ID, OwnerUserID: session.CreateUserID,
				ContractVersion: "workflow.v1", EventType: "artifact.upsert", EntityID: row.ID,
				StateVersion: session.StateVersion, PayloadJSON: payload, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
