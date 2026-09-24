package knowledge_market

import (
	"context"
	"encoding/json"
	"time"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

type marketExecutionKey struct{}

// The persisted canceled status is also the cross-process stop signal. Keep
// the runner's lease until cleanup finishes so another update cannot overtake it.
func marketExecutionContext(parent context.Context, job asyncjob.Job) (context.Context, func() error) {
	if id, ok := parent.Value(marketExecutionKey{}).(string); ok && id == job.ID {
		return parent, func() error { return nil }
	}
	ctx, cancel := context.WithCancel(parent)
	db := store.DB()
	if db == nil {
		cancel()
		return parent, func() error { return nil }
	}
	stopped := func(ctx context.Context) (bool, error) {
		var row orm.AsyncJob
		err := db.WithContext(ctx).Select("status").Where("id = ?", job.ID).Find(&row).Error
		return row.Status == "canceled", err
	}
	watchCtx := ctx
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				if stop, err := stopped(watchCtx); err == nil && stop {
					cancel()
					return
				}
			}
		}
	}()
	var saved *doc.MarketImportResult
	checkpoint := func(result *doc.MarketImportResult) error {
		if saved == nil {
			saved = &doc.MarketImportResult{DatasetID: result.DatasetID}
		}
		seen := make(map[string]bool, len(saved.TaskIDs))
		for _, id := range saved.TaskIDs {
			seen[id] = true
		}
		for _, id := range result.TaskIDs {
			if !seen[id] {
				saved.TaskIDs = append(saved.TaskIDs, id)
				seen[id] = true
			}
		}
		saved.Failures = append([]doc.MarketFileFailure(nil), result.Failures...)
		saved.Submitted = result.Submitted
		sent := make(map[string]bool, len(saved.DispatchedTaskIDs))
		for _, id := range saved.DispatchedTaskIDs {
			sent[id] = true
		}
		for _, id := range result.DispatchedTaskIDs {
			if !sent[id] {
				saved.DispatchedTaskIDs = append(saved.DispatchedTaskIDs, id)
				sent[id] = true
			}
		}
		writeCtx, release := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer release()
		data, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		if err = db.WithContext(writeCtx).Model(&orm.AsyncJob{}).Where("id = ? AND attempt_count = ?", job.ID, job.AttemptCount).Update("result_json", json.RawMessage(data)).Error; err != nil {
			return err
		}
		if stop, err := stopped(writeCtx); err != nil {
			return err
		} else if stop {
			cancel()
			return context.Canceled
		}
		return ctx.Err()
	}
	ctx = doc.WithMarketImportCheckpoint(context.WithValue(ctx, marketExecutionKey{}, job.ID), checkpoint)
	finish := func() error {
		cancel()
		<-done
		cleanup, release := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
		defer release()
		stop, err := stopped(cleanup)
		if err != nil || !stop {
			return err
		}
		if saved != nil {
			if err := doc.ReconcileInterruptedMarketFiles(cleanup, saved); err != nil {
				return err
			}
			var payload installJobPayload
			if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
				return err
			}
			install, err := loadMarketInstallRow(cleanup, db, payload.UserID, payload.MarketItemID)
			if err != nil {
				return err
			}
			cfg := decodeInstallConfig(install)
			cfg.TaskIDs = saved.TaskIDs
			cfg.Failures = saved.Failures
			if err := setInstallState(cleanup, db, payload.MarketItemID, payload.UserID, orm.InstallStateDone, saved.DatasetID, "", &cfg); err != nil {
				return err
			}
		} else {
			if err := db.WithContext(cleanup).Model(&orm.AsyncJob{}).Where("id = ? AND attempt_count = ?", job.ID, job.AttemptCount).Update("result_json", json.RawMessage(`{"reason":"stopped"}`)).Error; err != nil {
				return err
			}
		}
		return db.WithContext(cleanup).Model(&orm.AsyncJob{}).Where("id = ? AND status = ? AND attempt_count = ?", job.ID, "canceled", job.AttemptCount).Updates(map[string]any{"locked_by": "", "lock_until": nil, "finished_at": time.Now().UTC()}).Error
	}
	if stop, _ := stopped(ctx); stop {
		cancel()
	}
	return ctx, finish
}
