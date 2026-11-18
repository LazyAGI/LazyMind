package source

import "context"

// CountDesktopUpdateWork observes durable user work, including submissions
// waiting for Core results. Future schedule definitions are not executions.
func (r *SQLRepository) CountDesktopUpdateWork(ctx context.Context) (int64, error) {
	if r.orm == nil {
		return 0, NewStoreError(ErrCodeInternal, "repository unavailable")
	}
	var tasks, runs int64
	if err := r.orm.WithContext(ctx).Model(&ormParseTask{}).Where("status IN ?", []string{ParseTaskStatusPending, ParseTaskStatusRunning, ParseTaskStatusSubmitted}).Count(&tasks).Error; err != nil {
		return 0, err
	}
	if err := r.orm.WithContext(ctx).Model(&ormSyncRun{}).Where("status IN ?", []string{SyncRunStatusPending, SyncRunStatusRunning}).Count(&runs).Error; err != nil {
		return 0, err
	}
	return tasks + runs, nil
}
