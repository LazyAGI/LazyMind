package asyncjob

import (
	"context"
	"sync"
	"testing"
	"time"

	"lazymind/core/common/maintenancegate"
)

func TestRunnerUpdatePauseResumesWithoutLosingAdmittedWork(t *testing.T) {
	t.Setenv("TEST_DB_DRIVER", "sqlite")
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	Register("test.pause.active", func(ctx context.Context, _ Job, _ Reporter) (Result, error) {
		close(entered)
		select {
		case <-release:
			return Result{}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	})
	Register("test.pause.queued", func(ctx context.Context, _ Job, _ Reporter) (Result, error) { return Result{}, ctx.Err() })
	active := enqueueTestJob(t, db, "test.pause.active", 1)
	ctx, cancel := context.WithCancel(context.Background())
	runner := Start(ctx, db, Options{Concurrency: 1, PollInterval: time.Millisecond})
	defer func() {
		cancel()
		unblock()
		select {
		case <-runner.Done():
		case <-time.After(3 * time.Second):
			t.Error("pause fixture did not stop")
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("active job not started")
	}
	ticket, err := runner.PauseForUpdate(1)
	if err != nil || ticket == nil {
		t.Fatalf("pause: %v", err)
	}
	queued := enqueueTestJob(t, db, "test.pause.queued", 1)
	if ran, err := runner.runOnce(context.Background()); err != nil || ran {
		t.Fatalf("paused runner claimed work: %v %v", ran, err)
	}
	if ticket.Validate() != maintenancegate.ErrActive {
		t.Fatal("pause lost active handler")
	}
	if err := runner.ResumeFromUpdate(1); err != nil {
		t.Fatal(err)
	}
	if ticket.Validate() != maintenancegate.ErrStale {
		t.Fatal("resumed receipt remained valid")
	}
	second, err := runner.PauseForUpdate(2)
	if err != nil || second == nil {
		t.Fatal("second pause", err)
	}
	if second.Validate() != maintenancegate.ErrActive {
		t.Fatal("resume forgot admitted work")
	}
	if err := runner.ResumeFromUpdate(1); err != maintenancegate.ErrStale {
		t.Fatal("stale resume affected current pause", err)
	}
	if ran, err := runner.runOnce(context.Background()); err != nil || ran {
		t.Fatal("stale resume reopened work")
	}
	if err := runner.ResumeFromUpdate(2); err != nil {
		t.Fatal(err)
	}
	unblock()
	deadline := time.Now().Add(3 * time.Second)
	for getTestJob(t, db, queued.ID).Status != string(StatusSucceeded) {
		if time.Now().After(deadline) {
			t.Fatal("resumed runner did not execute queued job")
		}
		time.Sleep(time.Millisecond)
	}
	if got := getTestJob(t, db, active.ID); got.Status != string(StatusSucceeded) {
		t.Fatalf("pause canceled active work: %s", got.Status)
	}
	select {
	case <-runner.Done():
		t.Fatal("reversible pause terminated runner")
	default:
	}
	third, err := runner.PauseForUpdate(3)
	if err != nil || third == nil {
		t.Fatal(err)
	}
	waitCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := third.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.BeginDrain():
	case <-time.After(3 * time.Second):
		t.Fatal("terminal drain failed after pause")
	}
	if err := runner.ResumeFromUpdate(3); err != maintenancegate.ErrStopped {
		t.Fatal("terminal runner reopened", err)
	}
}
