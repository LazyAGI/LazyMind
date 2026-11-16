package asyncjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestRunnerPersistsFailureResultUnderLease(t *testing.T) {
	for _, attempts := range []int{1, 2} {
		for _, withResult := range []bool{false, true} {
			t.Run(fmt.Sprintf("attempts=%d/result=%t", attempts, withResult), func(t *testing.T) {
				db := newTestDB(t)
				resetRegistryForTest()
				defer resetRegistryForTest()
				previous := json.RawMessage(`{"model_requests":2}`)
				partial := json.RawMessage(`{"failed_scope":"formula","model_requests":2}`)
				Register("test.failure.result", func(ctx context.Context, job Job, reporter Reporter) (Result, error) {
					if err := reporter.SetProgress(ctx, 1, 11); err != nil {
						return Result{}, err
					}
					if withResult {
						return Result{ResultJSON: partial}, errors.New("safe model timeout")
					}
					return Result{}, errors.New("safe model timeout")
				})
				job := enqueueTestJob(t, db, "test.failure.result", attempts)
				if err := db.Model(&orm.AsyncJob{}).Where("id = ?", job.ID).Update("result_json", previous).Error; err != nil {
					t.Fatal(err)
				}
				runner := newTestRunner(db)
				if _, err := runner.runOnce(context.Background()); err != nil {
					t.Fatal(err)
				}
				saved := getTestJob(t, db, job.ID)
				expected := previous
				if withResult {
					expected = partial
				}
				status := string(StatusFailed)
				if attempts > 1 {
					status = string(StatusPending)
				}
				if saved.Status != status || string(saved.ResultJSON) != string(expected) || saved.ProgressCurrent != 1 {
					t.Fatalf("lost failed-attempt evidence: status=%s result=%s progress=%d", saved.Status, saved.ResultJSON, saved.ProgressCurrent)
				}
			})
		}
	}
}

func TestFailureResultCannotOverwriteNewerAttempt(t *testing.T) {
	db := newTestDB(t)
	runner := newTestRunner(db)
	job := enqueueTestJob(t, db, "test.stale.failure.result", 2)
	row, err := runner.claimOne(context.Background(), time.Now())
	if err != nil || row == nil {
		t.Fatalf("claim: %v", err)
	}
	current := json.RawMessage(`{"model_requests":5}`)
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", job.ID).Updates(map[string]any{"attempt_count": 2, "locked_by": "new-worker", "result_json": current}).Error; err != nil {
		t.Fatal(err)
	}
	err = runner.markFailedAttempt(context.Background(), *row, Result{ResultJSON: json.RawMessage(`{"failed_scope":"formula"}`)}, errors.New("late failure"))
	if err != nil {
		t.Fatal(err)
	}
	saved := getTestJob(t, db, job.ID)
	if saved.Status != string(StatusRunning) || string(saved.ResultJSON) != string(current) || saved.LockedBy != "new-worker" {
		t.Fatalf("stale finalizer replaced result: %+v", saved)
	}
}
