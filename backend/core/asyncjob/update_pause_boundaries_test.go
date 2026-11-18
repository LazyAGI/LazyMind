package asyncjob

import (
	"context"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/maintenancegate"
	"lazymind/core/common/orm"
)

func TestRunnerUpdatePauseTracksClaimThroughFinalWrite(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()
	Register("test.pause.boundary", func(ctx context.Context, _ Job, _ Reporter) (Result, error) { return Result{}, ctx.Err() })
	job := enqueueTestJob(t, db, "test.pause.boundary", 1)
	claim, releaseClaim := make(chan struct{}), make(chan struct{})
	final, releaseFinal := make(chan struct{}), make(chan struct{})
	var claimOnce, finalOnce, releaseClaimOnce, releaseFinalOnce sync.Once
	unblockClaim := func() { releaseClaimOnce.Do(func() { close(releaseClaim) }) }
	unblockFinal := func() { releaseFinalOnce.Do(func() { close(releaseFinal) }) }
	if err := db.Callback().Update().Before("gorm:update").Register("unit:pause-write-boundaries", func(tx *gorm.DB) {
		values, ok := tx.Statement.Dest.(map[string]any)
		if !ok || tx.Statement.Table != "async_jobs" {
			return
		}
		if values["status"] == string(StatusRunning) {
			claimOnce.Do(func() { close(claim) })
			<-releaseClaim
		}
		if values["status"] == string(StatusSucceeded) {
			finalOnce.Do(func() { close(final) })
			<-releaseFinal
		}
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := Start(ctx, db, Options{Concurrency: 1, PollInterval: time.Millisecond})
	defer func() {
		cancel()
		unblockClaim()
		unblockFinal()
		select {
		case <-runner.Done():
		case <-time.After(3 * time.Second):
			t.Error("boundary fixture did not stop")
		}
	}()
	select {
	case <-claim:
	case <-time.After(3 * time.Second):
		t.Fatal("claim did not reach real update")
	}
	ticket, err := runner.PauseForUpdate(1)
	if err != nil || ticket == nil {
		t.Fatal("pause", err)
	}
	assertBusy := func() {
		if ticket.Validate() != maintenancegate.ErrActive {
			t.Fatal("pause omitted admitted database work")
		}
		waitCtx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer stop()
		if err := ticket.Wait(waitCtx); err != context.DeadlineExceeded {
			t.Fatalf("pause returned before database work ended: %v", err)
		}
	}
	assertBusy()
	unblockClaim()
	select {
	case <-final:
	case <-time.After(3 * time.Second):
		t.Fatal("final write not reached")
	}
	assertBusy()
	unblockFinal()
	waitCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := ticket.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	if got := getTestJob(t, db, job.ID); got.Status != string(StatusSucceeded) {
		t.Fatalf("final state not committed: %s", got.Status)
	}
	if err := runner.ResumeFromUpdate(1); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerUpdatePauseIncludesPeriodicRecovery(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	Register("test.pause.periodic-active", func(ctx context.Context, _ Job, _ Reporter) (Result, error) {
		close(entered)
		select {
		case <-release:
			return Result{}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	})
	enqueueTestJob(t, db, "test.pause.periodic-active", 1)
	type clock struct {
		ticks   chan time.Time
		stopped chan struct{}
	}
	created := make(chan *clock, 4)
	runner := newRunner(db, Options{Concurrency: 1, PollInterval: time.Hour})
	runner.recoveryTicker = func(time.Duration) (<-chan time.Time, func()) {
		c := &clock{ticks: make(chan time.Time), stopped: make(chan struct{})}
		created <- c
		var once sync.Once
		return c.ticks, func() { once.Do(func() { close(c.stopped) }) }
	}
	ctx, cancel := context.WithCancel(context.Background())
	go runner.run(ctx)
	defer func() {
		cancel()
		unblock()
		select {
		case <-runner.Done():
		case <-time.After(3 * time.Second):
			t.Error("periodic fixture did not stop")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("handler not active")
	}
	var c *clock
	select {
	case c = <-created:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery clock not created")
	}
	if ticket, err := runner.PauseForUpdate(1); err != nil || ticket == nil {
		t.Fatal("pause", err)
	}
	expired := enqueueTestJob(t, db, "test.pause.expired", 2)
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", expired.ID).Updates(map[string]any{"status": string(StatusRunning), "attempt_count": 1, "locked_by": "old-worker", "lock_until": time.Now().UTC().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	// Two unbuffered sends establish that the first actual recovery call
	// returned. A stopped clock is also valid while the owner is paused.
	for range 2 {
		select {
		case c.ticks <- time.Now().UTC():
		case <-c.stopped:
		case <-time.After(3 * time.Second):
			t.Fatal("paused recovery neither advanced nor stopped")
		}
	}
	if got := getTestJob(t, db, expired.ID); got.Status != string(StatusRunning) || got.LockedBy != "old-worker" {
		t.Fatal("paused periodic recovery mutated job")
	}
	if err := runner.ResumeFromUpdate(1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.stopped:
		select {
		case c = <-created:
		case <-time.After(3 * time.Second):
			t.Fatal("recovery clock was not resumed")
		}
	default:
	}
	for range 2 {
		select {
		case c.ticks <- time.Now().UTC():
		case <-time.After(3 * time.Second):
			t.Fatal("resumed recovery did not consume tick")
		}
	}
	if got := getTestJob(t, db, expired.ID); got.Status != string(StatusPending) || got.LockedBy != "" {
		t.Fatal("resumed periodic recovery did not process expired job")
	}
}
