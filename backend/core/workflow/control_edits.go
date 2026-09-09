package workflow

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
)

// reviewMaterialTransaction shares the actual material write transaction with
// the review version update. Legacy runs retain their existing write semantics.
func reviewMaterialTransaction(ctx context.Context, db *gorm.DB, sessionID, slotID string, write func(*gorm.DB) error) error {
	return common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		if !tx.Migrator().HasColumn(&orm.WorkflowSession{}, "control_protocol") {
			return write(tx)
		}
		session, err := controlstore.LockSession(tx, sessionID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return write(tx)
		}
		if err != nil {
			return err
		}
		if !controlstore.Controlled(session) {
			return write(tx)
		}
		if err := controlstore.GuardMaterialEdit(tx, session, slotID); err != nil {
			return err
		}
		if err := write(tx); err != nil {
			return err
		}
		if err := controlstore.RefreshReviews(tx, &session); err != nil {
			return err
		}
		return controlstore.BumpEvent(tx, &session, "artifact.edited", slotID, "", map[string]any{"slot": slotID})
	})
}

// Recovery replaces a collection; historical item identities are retained for
// lineage, but must no longer participate in current display/export ordering.
func pruneControlledListOrders(tx *gorm.DB, sessionID string) error {
	var orders []orm.WorkflowSlotOrder
	if err := tx.Where("session_id = ?", sessionID).Find(&orders).Error; err != nil {
		return err
	}
	for _, order := range orders {
		var indices []int
		if err := json.Unmarshal(order.OrderList, &indices); err != nil {
			return err
		}
		var current []int
		if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ? AND slot_id = ? AND selected = ? AND validity = ? AND list_index IS NOT NULL", sessionID, order.SlotID, true, "effective").Pluck("list_index", &current).Error; err != nil {
			return err
		}
		valid := map[int]bool{}
		for _, index := range current {
			valid[index] = true
		}
		kept := []int{}
		for _, index := range indices {
			if valid[index] {
				kept = append(kept, index)
			}
		}
		if len(kept) == len(indices) {
			continue
		}
		raw, _ := json.Marshal(kept)
		if err := tx.Model(&orm.WorkflowSlotOrder{}).Where("session_id = ? AND slot_id = ?", sessionID, order.SlotID).Updates(map[string]any{"order_list": raw, "order_version": gorm.Expr("order_version + 1")}).Error; err != nil {
			return err
		}
	}
	return nil
}
