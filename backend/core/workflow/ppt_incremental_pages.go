package workflow

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func isPPTPreviewSlot(session *orm.WorkflowSession, slotID string) bool {
	return (session.WorkflowRef == "builtin:ppt-workflow" || session.WorkflowID == "ppt-workflow") &&
		(slotID == "preview_html" || slotID == "preview_notes")
}

// An explicit insertion reuses existing pages. Rewinds invalidate their producer
// attempt, so carry the visible historical values into new revisions owned by the
// current attempt. Never reactivate stale attempts or overwrite selected edits.
// The caller holds the insertion transaction; failures roll back the whole insert.
func carryForwardPPTPages(ctx context.Context, tx *gorm.DB, session *orm.WorkflowSession, slotID, stepID string, attempt int) error {
	if !isPPTPreviewSlot(session, slotID) {
		return nil
	}
	if _, err := lockArtifactMutationSession(tx, session.ID); err != nil {
		return err
	}
	order, err := GetSlotOrder(ctx, tx, session.ID, slotID)
	if err != nil || order == nil {
		return err
	}
	var indices []int
	if err := json.Unmarshal(order.OrderList, &indices); err != nil {
		return err
	}
	for _, index := range indices {
		var selected int64
		query := tx.WithContext(ctx).Model(&orm.WorkflowSlotRevision{}).
			Where("session_id = ? AND slot_id = ? AND list_index = ?", session.ID, slotID, index)
		if err := query.Where("selected = ?", true).Count(&selected).Error; err != nil {
			return err
		}
		if selected > 0 {
			continue
		}
		var previous orm.WorkflowSlotRevision
		if err := tx.WithContext(ctx).Where("session_id = ? AND slot_id = ? AND list_index = ?", session.ID, slotID, index).
			Order("revision DESC").First(&previous).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		if previous.Validity != "stale" {
			continue
		}
		value, err := LoadSlotRevisionValue(ctx, tx, previous)
		if err != nil {
			return err
		}
		contentType := "text"
		var caption *string
		if previous.HumanArtifactID != nil {
			var artifact orm.WorkflowHumanArtifact
			if err := tx.First(&artifact, "id = ?", *previous.HumanArtifactID).Error; err != nil {
				return err
			}
			contentType, caption = artifact.ContentType, artifact.Caption
		} else if previous.ArtifactSeq != nil {
			taskID, err := loadSlotRevisionTaskID(ctx, tx, previous)
			if err != nil {
				return err
			}
			var artifact orm.SubAgentArtifact
			if err := tx.Where("task_id = ? AND slot = ? AND seq = ? AND hidden = ?", taskID, previous.Slot, *previous.ArtifactSeq, false).First(&artifact).Error; err != nil {
				return err
			}
			contentType, caption = artifact.ContentType, artifact.Caption
		}
		if _, err := WriteSlotRevisionWithHumanArtifact(ctx, tx,
			session.ID, slotID, previous.Slot, stepID, attempt, "list", &index,
			contentType, value, caption, "host", nil, nil); err != nil {
			return err
		}
	}
	return nil
}
