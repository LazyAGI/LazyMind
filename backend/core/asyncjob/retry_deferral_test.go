package asyncjob

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunnerDurablyDefersWithoutConsumingAttempt(t *testing.T) {
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()
	retryAt := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	Register("test.defer", func(context.Context, Job, Reporter) (Result, error) {
		return Result{RetryAt: retryAt, DeferAttempt: true}, errors.New("rate limited")
	})
	job := enqueueTestJob(t, db, "test.defer", 1)
	if ran, e := newTestRunner(db).runOnce(context.Background()); e != nil || !ran {
		t.Fatal(ran, e)
	}
	got := getTestJob(t, db, job.ID)
	if got.Status != string(StatusPending) || got.AttemptCount != 0 || !got.NextRunAt.Equal(retryAt) || got.LockedBy != "" || got.LockUntil != nil || got.FinishedAt != nil {
		t.Fatalf("invalid deferred job: %+v", got)
	}
	restarted := newTestRunner(db)
	if ran, e := restarted.runOnce(context.Background()); e != nil || ran {
		t.Fatal("claimed before cooldown", ran, e)
	}
	claimed, e := restarted.claimOne(context.Background(), retryAt.Add(time.Second))
	if e != nil || claimed == nil || claimed.AttemptCount != 1 {
		t.Fatalf("cannot retry after cooldown: %+v %v", claimed, e)
	}
}
func TestRunnerInvalidDeferralStillConsumesAttempt(t *testing.T) {
	for _, at := range []time.Time{{}, time.Now().Add(-time.Hour)} {
		t.Run(at.String(), func(t *testing.T) {
			db := newTestDB(t)
			resetRegistryForTest()
			defer resetRegistryForTest()
			Register("test.defer.invalid", func(context.Context, Job, Reporter) (Result, error) {
				return Result{RetryAt: at, DeferAttempt: true}, errors.New("failed")
			})
			job := enqueueTestJob(t, db, "test.defer.invalid", 1)
			if _, e := newTestRunner(db).runOnce(context.Background()); e != nil {
				t.Fatal(e)
			}
			got := getTestJob(t, db, job.ID)
			if got.Status != string(StatusFailed) || got.AttemptCount != 1 {
				t.Fatalf("invalid deferral bypassed budget: %+v", got)
			}
		})
	}
}
func TestRunnerRetryAtOverridesBackoffButConsumesAttempt(t *testing.T) {
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()
	at := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	Register("test.retryat", func(context.Context, Job, Reporter) (Result, error) { return Result{RetryAt: at}, errors.New("failed") })
	job := enqueueTestJob(t, db, "test.retryat", 2)
	if _, e := newTestRunner(db).runOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	got := getTestJob(t, db, job.ID)
	if got.Status != string(StatusPending) || got.AttemptCount != 1 || !got.NextRunAt.Equal(at) {
		t.Fatalf("retry time ignored: %+v", got)
	}
}
