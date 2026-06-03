package task

import (
	"context"
	"errors"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type JobQueue interface {
	EnqueueSyncRun(ctx context.Context, run store.SyncRun) (store.SyncRun, bool, error)
}

type SyncRunEnqueueStore interface {
	EnqueueSyncRun(ctx context.Context, run store.SyncRun) (store.SyncRun, bool, error)
}

type jobQueueStore interface {
	SyncRunEnqueueStore
}

type DBJobQueue struct {
	store jobQueueStore
}

func NewDBJobQueue(store jobQueueStore) *DBJobQueue {
	if store == nil {
		panic("db job queue store is required")
	}
	return &DBJobQueue{store: store}
}

func (q *DBJobQueue) EnqueueSyncRun(ctx context.Context, run store.SyncRun) (store.SyncRun, bool, error) {
	if err := ctx.Err(); err != nil {
		return store.SyncRun{}, false, err
	}
	if q == nil || q.store == nil {
		return store.SyncRun{}, false, errors.New("db job queue store is required")
	}
	return q.store.EnqueueSyncRun(ctx, run)
}
