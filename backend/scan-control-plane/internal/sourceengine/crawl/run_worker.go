package crawl

import (
	"context"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type SyncRunClaimer interface {
	ClaimDueSyncRun(ctx context.Context, workerID string, now time.Time, ttl time.Duration) (store.SyncRun, bool, error)
	GetSyncCheckpoint(ctx context.Context, bindingID string) (store.SyncCheckpoint, error)
}

type RunFinisher interface {
	FinishRun(ctx context.Context, req schedule.FinishRunRequest) (store.SyncRun, bool, error)
}

type RunOnceWorker struct {
	store    SyncRunClaimer
	crawler  *DefaultCrawlEngine
	finisher RunFinisher
	clock    func() time.Time
	ttl      time.Duration
}

type RunOnceOption func(*RunOnceWorker)

func NewRunOnceWorker(store SyncRunClaimer, crawler *DefaultCrawlEngine, finisher RunFinisher, options ...RunOnceOption) *RunOnceWorker {
	w := &RunOnceWorker{
		store:    store,
		crawler:  crawler,
		finisher: finisher,
		clock:    time.Now,
		ttl:      5 * time.Minute,
	}
	for _, option := range options {
		option(w)
	}
	return w
}

func WithRunWorkerClock(clock func() time.Time) RunOnceOption {
	return func(w *RunOnceWorker) {
		if clock != nil {
			w.clock = clock
		}
	}
}

func WithRunLeaseTTL(ttl time.Duration) RunOnceOption {
	return func(w *RunOnceWorker) {
		if ttl > 0 {
			w.ttl = ttl
		}
	}
}

func (w *RunOnceWorker) RunOnce(ctx context.Context, workerID string) (store.SyncRun, bool, error) {
	run, ok, err := w.store.ClaimDueSyncRun(ctx, workerID, w.clock().UTC(), w.ttl)
	if err != nil || !ok {
		return run, ok, err
	}
	result, crawlErr := w.run(ctx, run)
	finish := finishRequestFromResult(run.RunID, workerID, result)
	if crawlErr != nil {
		finish = failedFinishRequest(run.RunID, workerID, crawlErr)
	}
	finished, finishedOK, finishErr := w.finisher.FinishRun(ctx, finish)
	if finishErr != nil || !finishedOK {
		return finished, finishedOK, finishErr
	}
	return finished, true, nil
}

func (w *RunOnceWorker) run(ctx context.Context, run store.SyncRun) (RunResult, error) {
	if run.ScopeType == string(connector.ScopeTypeCleanup) {
		return RunResult{
			RunID:  run.RunID,
			Status: RunStatusSucceeded,
			Coverage: Coverage{
				ScopeType:         connector.ScopeTypeCleanup,
				CoveredTargetRoot: true,
				Complete:          true,
			},
		}, nil
	}
	return w.crawler.Run(ctx, w.claimFromRun(ctx, run))
}

func (w *RunOnceWorker) claimFromRun(ctx context.Context, run store.SyncRun) BindingRunClaim {
	checkpoint, err := w.store.GetSyncCheckpoint(ctx, run.BindingID)
	cursor := ""
	if err == nil {
		cursor = checkpoint.Cursor
	}
	return BindingRunClaim{
		RunID:             run.RunID,
		SourceID:          run.SourceID,
		BindingID:         run.BindingID,
		BindingGeneration: run.BindingGeneration,
		TriggerType:       run.TriggerType,
		ScopeType:         connector.ScopeType(run.ScopeType),
		ScopeRef:          connector.ScopeRef(scopeRefStrings(run.ScopeRef)),
		Cursor:            cursor,
	}
}

func finishRequestFromResult(runID, workerID string, result RunResult) schedule.FinishRunRequest {
	return schedule.FinishRunRequest{
		RunID:          runID,
		WorkerID:       workerID,
		Status:         storeStatus(result.Status),
		Cursor:         result.NextCursor,
		Coverage:       coverageJSON(result.Coverage),
		SeenCount:      result.Counts.Seen,
		NewCount:       result.Counts.New,
		ModifiedCount:  result.Counts.Modified,
		DeletedCount:   result.Counts.Deleted,
		UnchangedCount: result.Counts.Unchanged,
		ErrorCode:      result.ErrorCode,
		ErrorMessage:   result.ErrorMessage,
	}
}

func failedFinishRequest(runID, workerID string, err error) schedule.FinishRunRequest {
	req := schedule.FinishRunRequest{
		RunID:        runID,
		WorkerID:     workerID,
		Status:       store.SyncRunStatusFailed,
		ErrorMessage: err.Error(),
	}
	if code, ok := connector.ErrorCodeOf(err); ok {
		req.ErrorCode = string(code)
	}
	return req
}

func coverageJSON(coverage Coverage) store.JSON {
	return store.JSON{
		"scope_type":          string(coverage.ScopeType),
		"covered_object_keys": coverage.CoveredObjectKeys,
		"covered_subtrees":    coverage.CoveredSubtrees,
		"covered_target_root": coverage.CoveredTargetRoot,
		"complete":            coverage.Complete,
		"watermark":           coverage.Watermark,
		"excluded_reason":     coverage.ExcludedReason,
	}
}

func scopeRefStrings(scopeRef store.JSON) map[string]string {
	out := make(map[string]string, len(scopeRef))
	for key, value := range scopeRef {
		if text, ok := value.(string); ok {
			out[key] = text
		}
	}
	return out
}

func storeStatus(status string) string {
	switch status {
	case RunStatusCanceled:
		return store.SyncRunStatusCanceled
	case RunStatusFailed:
		return store.SyncRunStatusFailed
	default:
		return store.SyncRunStatusSucceeded
	}
}
