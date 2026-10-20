package attempt

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactgraph"
	"lazymind/core/workflow/publication"
	"time"
)

func finishTransactionalOutputs(ctx context.Context, tx *gorm.DB, current orm.WorkflowSessionStep, status string) error {
	// Some legacy host-only stores have no Session schema.
	if !tx.Migrator().HasTable(&orm.WorkflowSession{}) {
		return nil
	}
	var session orm.WorkflowSession
	err := tx.Where("id = ?", current.SessionID).First(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	enabled, err := publication.PublicationEnabled(tx, session)
	if err != nil || !enabled {
		return err
	}
	if _, err := artifactgraph.LockSession(tx, current.SessionID); err != nil {
		return err
	}
	policy, err := publication.Policy(tx, session)
	if err != nil {
		return err
	}
	if status == "succeeded" && policy.Publication != nil && current.StepID == policy.Publication.Step {
		if err := publication.Publish(tx, session, current, *policy.Publication); err != nil {
			return err
		}
	}
	var outputs []orm.WorkflowSlotRevision
	if err := tx.Where("session_id = ? AND producer_attempt_id = ?", current.SessionID, current.ID).
		Order("revision ASC, created_at ASC").Find(&outputs).Error; err != nil {
		return err
	}
	if len(outputs) == 0 {
		return nil
	}
	type slotKey struct {
		slot  string
		index int
		list  bool
	}
	latest := make(map[slotKey]orm.WorkflowSlotRevision, len(outputs))
	wasSelected := make(map[slotKey]bool, len(outputs))
	for _, output := range outputs {
		key := slotKey{slot: output.SlotID}
		if output.ListIndex != nil {
			key.index, key.list = *output.ListIndex, true
		}
		latest[key] = output
		wasSelected[key] = wasSelected[key] || output.Selected
	}
	if status != "succeeded" {
		if err := tx.Model(&orm.WorkflowSlotRevision{}).
			Where("session_id = ? AND producer_attempt_id = ?", current.SessionID, current.ID).
			Updates(map[string]any{"validity": "stale", "selected": false}).Error; err != nil {
			return err
		}
	} else {
		// A retry may have emitted multiple revisions of one slot. Only its last
		// revision becomes selected after the complete Attempt succeeds.
		if err := tx.Model(&orm.WorkflowSlotRevision{}).
			Where("session_id = ? AND producer_attempt_id = ?", current.SessionID, current.ID).
			Update("selected", false).Error; err != nil {
			return err
		}
	}
	for key, output := range latest {
		selected := tx.Model(&orm.WorkflowSlotRevision{}).
			Where("session_id = ? AND slot_id = ? AND selected = ?", current.SessionID, key.slot, true)
		if key.list {
			selected = selected.Where("list_index = ?", key.index)
		} else {
			selected = selected.Where("list_index IS NULL")
		}
		if status == "succeeded" {
			var replaced []orm.WorkflowSlotRevision
			if err := selected.Where("id != ?", output.ID).Select("id").Find(&replaced).Error; err != nil {
				return err
			}
			ids := make([]string, 0, len(replaced))
			for _, revision := range replaced {
				ids = append(ids, revision.ID)
			}
			if err := artifactgraph.InvalidateConsumersPreservingProducer(ctx, tx, current.SessionID, output, ids...); err != nil {
				return err
			}
			// GORM's chained Where above mutates the query statement. Rebuild the
			// selector so it also clears any previously selected revision.
			selected = tx.Model(&orm.WorkflowSlotRevision{}).
				Where("session_id = ? AND slot_id = ? AND selected = ?", current.SessionID, key.slot, true)
			if key.list {
				selected = selected.Where("list_index = ?", key.index)
			} else {
				selected = selected.Where("list_index IS NULL")
			}
			if err := selected.Update("selected", false).Error; err != nil {
				return err
			}
			if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", output.ID).
				Update("selected", true).Error; err != nil {
				return err
			}
			continue
		}
		if !wasSelected[key] {
			continue
		}
		// Outputs written by an older binary may already have deselected the
		// previous successful revision. Restore it after a failed Attempt.
		var prior orm.WorkflowSlotRevision
		query := tx.Table("plugin_slot_revisions AS revisions").Select("revisions.*").
			Joins("LEFT JOIN plugin_session_steps AS steps ON steps.id = revisions.producer_attempt_id").
			Where("revisions.session_id = ? AND revisions.slot_id = ? AND revisions.validity = 'effective' AND revisions.producer_attempt_id != ?", current.SessionID, key.slot, current.ID).
			Where("steps.status = 'succeeded' OR (revisions.change_source = 'human' AND revisions.producer_attempt_id = '')")
		if key.list {
			query = query.Where("revisions.list_index = ?", key.index)
		} else {
			query = query.Where("revisions.list_index IS NULL")
		}
		err := query.Order("revisions.revision DESC, revisions.created_at DESC").First(&prior).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", prior.ID).
				Update("selected", true).Error; err != nil {
				return err
			}
		}
	}
	return tx.Model(&orm.WorkflowSession{}).Where("id = ?", current.SessionID).
		Updates(map[string]any{"state_version": gorm.Expr("state_version + 1"), "updated_at": time.Now().UTC()}).Error
}
