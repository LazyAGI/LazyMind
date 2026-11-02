package asyncjob

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func requireRunnerDrain(t *testing.T, runner *Runner) <-chan struct{} {
	t.Helper()
	done := runner.BeginDrain()
	if done == nil {
		t.Fatal("drain returned nil completion")
	}
	if done != runner.Done() {
		t.Fatal("drain does not reuse the runner Done signal")
	}
	return done
}

func TestRunnerUpdateDrainPreservesActiveJobAndFinalWrite(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()
	entered, release := make(chan struct{}), make(chan struct{})
	finalizing, finishWrite := make(chan struct{}), make(chan struct{})
	var releaseOnce, writeOnce, finalOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }); writeOnce.Do(func() { close(finishWrite) }) }
	var canceled atomic.Bool
	Register("test.update.active", func(ctx context.Context, _ Job, _ Reporter) (Result, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			canceled.Store(true)
			return Result{}, ctx.Err()
		}
		canceled.Store(ctx.Err() != nil)
		return Result{}, nil
	})
	active := enqueueTestJob(t, db, "test.update.active", 1)
	if err := db.Callback().Update().Before("gorm:update").Register("unit:hold-final-write", func(tx *gorm.DB) {
		values, ok := tx.Statement.Dest.(map[string]any)
		if ok && tx.Statement.Table == "async_jobs" && values["status"] == string(StatusSucceeded) {
			finalOnce.Do(func() { close(finalizing) })
			<-finishWrite
		}
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := newRunner(db, Options{Concurrency: 1, PollInterval: time.Millisecond, LockTTL: 3 * time.Second})
	ticks, clockReady, clockStopped := make(chan time.Time), make(chan struct{}), make(chan struct{})
	runner.recoveryTicker = func(time.Duration) (<-chan time.Time, func()) {
		close(clockReady)
		return ticks, func() { close(clockStopped) }
	}
	go runner.run(ctx)
	defer func() {
		cancel()
		unblock()
		select {
		case <-runner.Done():
		case <-time.After(3 * time.Second):
			t.Error("runner fixture did not stop")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("active job did not enter handler")
	}
	initial := getTestJob(t, db, active.ID)
	select {
	case <-clockReady:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery clock not started")
	}
	drained := requireRunnerDrain(t, runner)
	pending := enqueueTestJob(t, db, "test.update.pending", 1)
	expired := enqueueTestJob(t, db, "test.update.expired", 2)
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", expired.ID).Updates(map[string]any{"status": string(StatusRunning), "attempt_count": 1, "locked_by": "old-worker", "lock_until": time.Now().UTC().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	// Drive the real recovery loop. A second unbuffered tick can be received
	// only after the first pass returns; stopping the clock is also conclusive.
	for range 2 {
		select {
		case ticks <- time.Now().UTC():
		case <-clockStopped:
		case <-time.After(3 * time.Second):
			t.Fatal("recovery iteration neither completed nor stopped")
		}
	}
	if got := getTestJob(t, db, expired.ID); got.Status != string(StatusRunning) || got.LockedBy != "old-worker" {
		t.Fatal("draining runner still performed periodic recovery")
	}
	deadline := time.Now().Add(5 * time.Second)
	renewed := false
	for time.Now().Before(deadline) {
		current := getTestJob(t, db, active.ID)
		if canceled.Load() || current.Status != string(StatusRunning) {
			t.Fatal("drain canceled or finalized the active job")
		}
		if current.LockUntil != nil && initial.LockUntil != nil && current.LockUntil.After(*initial.LockUntil) {
			renewed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !renewed {
		t.Fatal("drain stopped the active job heartbeat")
	}
	select {
	case <-drained:
		t.Fatal("drain completed with an active handler")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-finalizing:
	case <-time.After(3 * time.Second):
		t.Fatal("final write not reached")
	}
	select {
	case <-drained:
		t.Fatal("drain completed before final write")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case <-runner.Done():
		t.Fatal("runner done preceded final write")
	default:
	}
	writeOnce.Do(func() { close(finishWrite) })
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not complete")
	}
	select {
	case <-requireRunnerDrain(t, runner):
	case <-time.After(time.Second):
		t.Fatal("repeated drain did not complete")
	}
	if canceled.Load() {
		t.Fatal("drain canceled admitted business work")
	}
	if got := getTestJob(t, db, active.ID); got.Status != string(StatusSucceeded) || got.LockedBy != "" || got.LockUntil != nil {
		t.Fatalf("final active state: %+v", got)
	}
	if got := getTestJob(t, db, pending.ID); got.Status != string(StatusPending) || got.AttemptCount != 0 {
		t.Fatalf("drain claimed pending job: %+v", got)
	}
}

func TestRunnerUpdateDrainBeforeStartDoesNotRecoverOrClaim(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	db := newTestDB(t)
	pending := enqueueTestJob(t, db, "test.update.pending", 1)
	expired := enqueueTestJob(t, db, "test.update.expired", 2)
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", expired.ID).Updates(map[string]any{"status": string(StatusRunning), "attempt_count": 1, "locked_by": "old-worker", "lock_until": time.Now().UTC().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(db)
	drained := requireRunnerDrain(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	go runner.run(ctx)
	defer func() {
		cancel()
		select {
		case <-runner.Done():
		case <-time.After(3 * time.Second):
			t.Error("pre-drained fixture did not stop")
		}
	}()
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("pre-drained runner did not finish")
	}
	if got := getTestJob(t, db, pending.ID); got.Status != string(StatusPending) || got.AttemptCount != 0 {
		t.Fatal("pre-drained runner claimed a job")
	}
	if got := getTestJob(t, db, expired.ID); got.Status != string(StatusRunning) || got.LockedBy != "old-worker" {
		t.Fatal("pre-drained runner recovered an expired job")
	}
}

func TestRunnerUpdateDrainWaitsForAdmittedClaim(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()
	Register("test.update.claim", func(ctx context.Context, _ Job, _ Reporter) (Result, error) { return Result{}, ctx.Err() })
	job := enqueueTestJob(t, db, "test.update.claim", 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	if err := db.Callback().Update().Before("gorm:update").Register("unit:hold-claim", func(tx *gorm.DB) {
		values, ok := tx.Statement.Dest.(map[string]any)
		if ok && values["status"] == string(StatusRunning) {
			enterOnce.Do(func() { close(entered) })
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := Start(ctx, db, Options{Concurrency: 1, PollInterval: time.Millisecond})
	defer func() {
		cancel()
		unblock()
		select {
		case <-runner.Done():
		case <-time.After(3 * time.Second):
			t.Error("claim fixture did not stop")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("claim not reached")
	}
	drained := requireRunnerDrain(t, runner)
	select {
	case <-drained:
		t.Fatal("drain omitted admitted claim")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("admitted claim did not drain")
	}
	if got := getTestJob(t, db, job.ID); got.Status != string(StatusSucceeded) {
		t.Fatalf("admitted claim did not finish: %s", got.Status)
	}
}

func TestRunnerOrdinaryCancellationStillStopsHandler(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()
	entered := make(chan struct{})
	Register("test.update.cancel", func(ctx context.Context, _ Job, _ Reporter) (Result, error) {
		close(entered)
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	job := enqueueTestJob(t, db, "test.update.cancel", 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := Start(ctx, db, Options{Concurrency: 1})
	defer func() {
		cancel()
		select {
		case <-runner.Done():
		case <-time.After(3 * time.Second):
			t.Error("cancellation fixture did not stop")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler not reached")
	}
	cancel()
	select {
	case <-runner.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("ordinary cancellation did not finish")
	}
	if got := getTestJob(t, db, job.ID); got.Status != string(StatusFailed) || got.LockedBy != "" || got.LockUntil != nil {
		t.Fatalf("ordinary cancellation did not finalize: %+v", got)
	}
}
