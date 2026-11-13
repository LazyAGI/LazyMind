package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/state"
	"lazymind/core/workflow/controlstore"
)

// A durable pause request survives both Core and executor restarts. Re-deliver
// it until the execution host confirms exit; elapsed time alone is never proof.
func StartNativeWorkflowStopRecovery(ctx context.Context, db *gorm.DB, stateStore state.Store) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			if err := recoverNativeWorkflowStops(ctx, db, stateStore); err != nil && ctx.Err() == nil {
				log.Logger.Warn().Err(err).Msg("workflow stop recovery failed")
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

func recoverNativeWorkflowStops(ctx context.Context, db *gorm.DB, stateStore state.Store) error {
	var sessions []orm.WorkflowSession
	if err := db.WithContext(ctx).Where("status = ? AND controller_host = ?", "pausing", "lazymind").Find(&sessions).Error; err != nil {
		return err
	}
	var failures []error
	for i := range sessions {
		if controlstore.Controlled(sessions[i]) {
			continue
		}
		if err := stopWorkflowSession(ctx, db, stateStore, &sessions[i], ""); err != nil {
			failures = append(failures, fmt.Errorf("reconcile workflow stop %s: %w", sessions[i].ID, err))
		}
	}
	return errors.Join(failures...)
}
