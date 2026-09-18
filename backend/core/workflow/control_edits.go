package workflow

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
)

// reviewMaterialTransaction shares the actual material write transaction with
// the dependency invalidation and review version update, for both panel hosts.
func reviewMaterialTransaction(ctx context.Context, db *gorm.DB, sessionID, slotID string, write func(*gorm.DB) error, publication ...bool) error {
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

		if len(publication) == 0 || !publication[0] {
			if err := controlstore.PrepareMaterialEdit(tx, &session, slotID); err != nil {
				return err
			}
		}
		if err := write(tx); err != nil {
			return err
		}
		if !controlstore.Controlled(session) {
			return nil
		}
		if err := controlstore.RefreshReviews(tx, &session); err != nil {
			return err
		}
		return controlstore.BumpEvent(tx, &session, "artifact.edited", slotID, "", map[string]any{"slot": slotID})
	})
}
