package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	DefaultGlobalConcurrency = 20
	DefaultSourceConcurrency = 2
)

type Runner struct {
	worker            *DefaultParseWorker
	globalConcurrency int
	sourceConcurrency int
}

type ReconcilerRunner struct {
	reconciler *CoreResultReconciler
	limit      int
}

func NewReconcilerRunner(reconciler *CoreResultReconciler, limit int) *ReconcilerRunner {
	if limit <= 0 {
		limit = DefaultGlobalConcurrency
	}
	return &ReconcilerRunner{reconciler: reconciler, limit: limit}
}

func (r *ReconcilerRunner) RunPending(ctx context.Context, workerID string) error {
	if r == nil || r.reconciler == nil {
		return errors.New("core result reconciler is required")
	}
	for i := 0; i < r.limit; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.reconciler.RunOnce(ctx, workerID); err != nil {
			if errors.Is(err, ErrNoTask) {
				return nil
			}
			return err
		}
	}
	return nil
}

type TempCleanupRunner struct {
	cleaner TempObjectCleaner
	ttl     time.Duration
}

func NewTempCleanupRunner(cleaner TempObjectCleaner, ttl time.Duration) *TempCleanupRunner {
	return &TempCleanupRunner{cleaner: cleaner, ttl: ttl}
}

func (r *TempCleanupRunner) RunOnce(ctx context.Context) error {
	if r == nil || r.cleaner == nil {
		return nil
	}
	_, err := r.cleaner.CleanupExpired(ctx, r.ttl)
	return err
}

type RunnerOption func(*Runner)

func NewRunner(parseWorker *DefaultParseWorker, options ...RunnerOption) *Runner {
	r := &Runner{
		worker:            parseWorker,
		globalConcurrency: DefaultGlobalConcurrency,
		sourceConcurrency: DefaultSourceConcurrency,
	}
	for _, option := range options {
		option(r)
	}
	return r
}

func WithGlobalConcurrency(limit int) RunnerOption {
	return func(r *Runner) {
		if limit > 0 {
			r.globalConcurrency = limit
		}
	}
}

func WithSourceConcurrency(limit int) RunnerOption {
	return func(r *Runner) {
		if limit > 0 {
			r.sourceConcurrency = limit
		}
	}
}

func (r *Runner) RunPending(ctx context.Context, workerID string) error {
	if r == nil || r.worker == nil {
		return errors.New("parse worker is required")
	}
	limiter := newSourceLimiter(r.globalConcurrency, r.sourceConcurrency)
	var wg sync.WaitGroup
	errCh := make(chan error, r.globalConcurrency)
	deferred := []store.ParseTask{}
	defer func() {
		for _, task := range deferred {
			_ = r.worker.release(ctx, task)
		}
	}()
	for i := 0; i < r.globalConcurrency; i++ {
		if err := ctx.Err(); err != nil {
			break
		}
		task, ok, err := r.worker.claim(ctx, workerID)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if !limiter.tryAcquire(task.SourceID) {
			deferred = append(deferred, task)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer limiter.release(task.SourceID)
			if err := r.worker.runClaimed(ctx, task); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

type sourceLimiter struct {
	mu          sync.Mutex
	globalLimit int
	sourceLimit int
	global      int
	bySource    map[string]int
}

func newSourceLimiter(globalLimit, sourceLimit int) *sourceLimiter {
	if globalLimit <= 0 {
		globalLimit = DefaultGlobalConcurrency
	}
	if sourceLimit <= 0 {
		sourceLimit = DefaultSourceConcurrency
	}
	return &sourceLimiter{
		globalLimit: globalLimit,
		sourceLimit: sourceLimit,
		bySource:    map[string]int{},
	}
}

func (l *sourceLimiter) tryAcquire(sourceID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.global >= l.globalLimit {
		return false
	}
	if l.bySource[sourceID] >= l.sourceLimit {
		return false
	}
	l.global++
	l.bySource[sourceID]++
	return true
}

func (l *sourceLimiter) release(sourceID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.global > 0 {
		l.global--
	}
	if l.bySource[sourceID] > 0 {
		l.bySource[sourceID]--
	}
}
