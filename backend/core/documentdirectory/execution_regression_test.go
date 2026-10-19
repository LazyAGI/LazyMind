package documentdirectory

import (
	"context"
	"fmt"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real runner and commit path against concurrent canceled executions.
func TestReviewRetryRejectsOldExecution(t *testing.T) {
	db := testDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	d := create(t, db, "retry")
	c := sample("/old-result")
	input := Input{Manual: true, Observations: []orm.DocumentObservation{{PayloadJSON: encoded(Observation{Candidates: []Candidate{c}})}}}
	b := orm.DocumentDirectoryBatch{ID: "review-retry-batch", UserID: "u", OwnerKey: "u", TriggerID: "review-retry", DirectoryID: d.ID, InputJSON: encoded(input), CreatedAt: time.Now().UTC()}
	jobType := fmt.Sprintf("review.retry.%d", time.Now().UnixNano())
	must(t, enqueue(ctx, db, &b, jobType, d.ID))
	started := make(chan asyncjob.Job, 2)
	releaseOld, releaseNew := make(chan struct{}), make(chan struct{})
	oldResult := make(chan error, 1)
	oldWrites := make(chan [2]error, 1)
	var calls atomic.Int32
	asyncjob.Register(jobType, func(hctx context.Context, j asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
		var executionBatch orm.DocumentDirectoryBatch
		if err := db.Where("id=?", b.ID).Take(&executionBatch).Error; err != nil {
			return asyncjob.Result{}, err
		}
		first := calls.Add(1) == 1
		started <- j
		if first {
			<-releaseOld
			oldWrites <- [2]error{reporter.Heartbeat(hctx), reporter.SetProgress(hctx, 7, 10)}
			err := applyOrganization(hctx, db, j, &executionBatch, d.Version, input, Proposal{Assignments: []Assignment{{ResourceKey: resourceKey(c)}}})
			oldResult <- err
			return asyncjob.Result{}, err
		}
		<-releaseNew
		return asyncjob.Result{}, nil
	})
	runner := asyncjob.Start(ctx, db, asyncjob.Options{JobTypes: []string{jobType}, WorkerID: "review-worker", Concurrency: 2, PollInterval: 10 * time.Millisecond, LockTTL: time.Minute})
	defer func() { close(releaseNew); cancel(); <-runner.Done() }()
	receive := func() asyncjob.Job {
		select {
		case j := <-started:
			return j
		case <-time.After(5 * time.Second):
			close(releaseOld)
			t.Fatal("runner did not start")
			return asyncjob.Job{}
		}
	}
	old := receive()
	command(t, db, Command{Operation: "cancel", BatchID: b.ID})
	command(t, db, Command{Operation: "retry", BatchID: b.ID})
	next := receive()
	close(releaseOld)
	var err error
	select {
	case err = <-oldResult:
	case <-time.After(5 * time.Second):
		t.Fatal("old commit did not return")
	}
	writes := <-oldWrites
	var jobRow orm.AsyncJob
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		must(t, db.Where("id=?", next.ID).Take(&jobRow).Error)
		if jobRow.Status == "succeeded" {
			break
		}
		time.Sleep(time.Millisecond * 10)
	}
	var count int64
	must(t, db.Model(&orm.DocumentDirectoryEntry{}).Where("directory_id=?", d.ID).Count(&count).Error)
	t.Logf("old_job=%s old_attempt=%d new_job=%s new_attempt=%d old_commit_error=%v persisted_entries=%d", old.ID, old.AttemptCount, next.ID, next.AttemptCount, err, count)
	t.Logf("old_heartbeat_error=%v old_progress_error=%v new_job_status_while_new_handler_blocked=%s", writes[0], writes[1], jobRow.Status)
	if old.ID == next.ID || writes[0] == nil || writes[1] == nil || jobRow.Status != "running" || err == nil || count != 0 {
		t.Errorf("old canceled execution committed after retry")
	}
}

func TestReviewLateObservationPreservesVersion(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	d := create(t, db, "late")
	late := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	early := late.Add(-time.Hour)
	current := sample("/same")
	current.Title = "Same"
	current.ReadAt = &late
	current.ReadState = "read"
	current.ContentVersion = "version:2"
	current.AccessState = "accessible"
	e := entryFromCandidate(d.ID, current, late)
	e.Confirmed = true
	must(t, db.Create(&e).Error)
	old := current
	old.ReadAt = &early
	old.ContentVersion = "version:1"
	old.Excerpt = "old reading"
	merged := mergeCandidate(current, old)
	input := Input{Observations: []orm.DocumentObservation{{PayloadJSON: encoded(Observation{Candidates: []Candidate{old}})}}}
	b, j := batch(t, db, d, input)
	must(t, applyOrganization(ctx, db, j, &b, d.Version, input, Proposal{Assignments: []Assignment{{ResourceKey: resourceKey(old), Confirmed: true, EvidenceBasis: "read", Reason: "old read"}}}))
	var got orm.DocumentDirectoryEntry
	must(t, db.Where("id=?", e.ID).Take(&got).Error)
	t.Logf("merge_version=%s persisted_version=%s read_at=%s confirmed=%v content_state=%s", merged.ContentVersion, got.ContentVersion, got.ReadAt.Format(time.RFC3339), got.Confirmed, got.ContentState)
	if got.ContentVersion != "version:2" || !got.Confirmed {
		t.Errorf("late evidence regressed accepted facts")
	}
}
