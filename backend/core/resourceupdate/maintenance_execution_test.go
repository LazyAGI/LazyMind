package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/maintenance"
	"lazymind/core/store"
)

func maintenanceReview(t *testing.T, db *gorm.DB, id, user string, now time.Time) {
	t.Helper()
	insertTask(t, db, orm.ResourceUpdateTask{ID: id, TaskType: orm.ResourceUpdateTaskTypeGenerateReview, ResourceType: orm.ResourceUpdateResourceTypeMemory, UserID: user,
		TriggerType: "manual", TriggerID: id, Status: "pending", NextRunAt: now, LaneKey: MemoryMaintenanceLaneKey(user), LanePriority: MemoryReviewLanePriority,
		LaneOrderAt: now, CreatedAt: now, UpdatedAt: now})
}

func TestOrganizerWaitsForClaimedReviewThenLeadsPendingReviews(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	w := NewWorker(db, Config{WorkerBatchSize: 10, WorkerLockTTL: time.Minute}, "worker")
	maintenanceReview(t, db, "review-1", "u1", now)
	claimed, err := w.claimPending(ctx, now)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	maintenanceReview(t, db, "review-2", "u1", now)
	org, _, err := EnqueuePreferenceOrganizer(ctx, db, "u1", "manual", "manual-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if next, err := w.claimPending(ctx, now); err != nil || len(next) != 0 {
		t.Fatalf("claimed during review: %v %v", next, err)
	}
	id := maintenance.Identity{TaskID: "memory_review_" + claimed[0].ID, RunID: claimed[0].RunID}
	err = maintenance.UserTransaction(ctx, db, "u1", func(tx *gorm.DB) error {
		return maintenance.Authorize(maintenance.WithIdentity(ctx, id), tx, "u1", true)
	})
	if err != nil {
		t.Fatalf("pending organizer froze active review: %v", err)
	}
	if err = w.finishTask(ctx, claimed[0], taskOutcome{Status: "done"}); err != nil {
		t.Fatal(err)
	}
	next, err := w.claimPending(ctx, now)
	if err != nil || len(next) != 1 || next[0].ID != org.ID {
		t.Fatalf("organizer must run next: %v %v", next, err)
	}
	var pending orm.ResourceUpdateTask
	db.First(&pending, "id = ?", "review-2")
	if pending.AttemptCount != 0 || pending.Status != "pending" {
		t.Fatalf("pending review consumed attempt: %#v", pending)
	}
	if err = w.finishTask(ctx, next[0], deferredOutcome("maintenance_busy", "full", 2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if next, err = w.claimPending(ctx, now); err != nil || len(next) != 0 {
		t.Fatalf("review bypassed deferred organizer: %v %v", next, err)
	}
	db.Model(&orm.ResourceUpdateTask{}).Where("id = ?", org.ID).Update("next_run_at", now)
	next, err = w.claimPending(ctx, now)
	if err != nil || len(next) != 1 || next[0].AttemptCount != 1 {
		t.Fatalf("deferred consumed attempt: %v %v", next, err)
	}
	if err = w.finishTask(ctx, next[0], taskOutcome{Status: "done"}); err != nil {
		t.Fatal(err)
	}
	next, err = w.claimPending(ctx, now)
	if err != nil || len(next) != 1 || next[0].ID != "review-2" {
		t.Fatalf("review not resumed: %v %v", next, err)
	}
}

func TestManualUpgradesPendingAutomaticAndReusesRunning(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	first, _, err := EnqueuePreferenceOrganizer(ctx, db, "u", "preference_changed", "event-u-1", now)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, created, err := EnqueuePreferenceOrganizer(ctx, db, "u", "manual", "manual-1", now)
	if err != nil || created || upgraded.ID != first.ID {
		t.Fatal(upgraded, created, err)
	}
	var request PreferenceOrganizerRequest
	if err = json.Unmarshal(upgraded.RequestJSON, &request); err != nil || !request.ForceAnalysis {
		t.Fatal(request, err)
	}
	w := NewWorker(db, Config{}, "w")
	claimed, err := w.claimPending(ctx, now)
	if err != nil || len(claimed) != 1 {
		t.Fatal(claimed, err)
	}
	reused, created, err := EnqueuePreferenceOrganizer(ctx, db, "u", "manual", "manual-2", now)
	if err != nil || created || reused.ID != first.ID {
		t.Fatal(reused, created, err)
	}
	if err = w.finishTask(ctx, claimed[0], taskOutcome{Status: "done"}); err != nil {
		t.Fatal(err)
	}
	fresh, created, err := EnqueuePreferenceOrganizer(ctx, db, "u", "preference_changed", "event-u-2", now)
	if err != nil || !created || fresh.ID == first.ID {
		t.Fatal(fresh, created, err)
	}
	other, created, err := EnqueuePreferenceOrganizer(ctx, db, "other", "preference_changed", "event-other-1", now)
	if err != nil || !created || other.ID == fresh.ID {
		t.Fatal(other, created, err)
	}
}

func TestExpiredRunCannotRenewFinishOrWriteAfterRecovery(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	maintenanceReview(t, db, "review", "u", now)
	w := NewWorker(db, Config{WorkerLockTTL: time.Minute}, "same-worker")
	first, err := w.claimPending(ctx, now)
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	db.Model(&orm.ResourceUpdateTask{}).Where("id = ?", "review").Update("locked_until", now.Add(-time.Second))
	if err = w.updateOwned(ctx, first[0], map[string]any{"locked_until": now.Add(time.Minute)}); !errors.Is(err, maintenance.ErrLeaseLost) {
		t.Fatalf("expired run renewed: %v", err)
	}
	if n, err := w.recoverExpiredRunning(ctx, now); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	second, err := w.claimPending(ctx, now)
	if err != nil || len(second) != 1 || second[0].RunID == first[0].RunID {
		t.Fatal(second, err)
	}
	if err = w.finishTask(ctx, first[0], taskOutcome{Status: "done"}); !errors.Is(err, maintenance.ErrLeaseLost) {
		t.Fatalf("old success accepted: %v", err)
	}
	stale := maintenance.WithIdentity(ctx, maintenance.Identity{TaskID: "memory_review_review", RunID: first[0].RunID})
	for _, freeze := range []bool{true, false} {
		err = maintenance.UserTransaction(ctx, db, "u", func(tx *gorm.DB) error { return maintenance.Authorize(stale, tx, "u", freeze) })
		if !errors.Is(err, maintenance.ErrLeaseLost) {
			t.Fatalf("old write accepted: %v", err)
		}
	}
}

func TestCommonHeartbeatRenewsAndNeverAcceptsSuccessAfterLeaseLoss(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	maintenanceReview(t, db, "review", "u", now)
	w := NewWorker(db, Config{WorkerLockTTL: 150 * time.Millisecond}, "worker")
	claimed, err := w.claimPending(ctx, now)
	if err != nil || len(claimed) != 1 {
		t.Fatal(claimed, err)
	}
	task := claimed[0]
	result := w.withTaskLeaseHeartbeat(ctx, task, func(callCtx context.Context) taskOutcome {
		deadline := time.Now().Add(2 * time.Second)
		for {
			var actual orm.ResourceUpdateTask
			db.First(&actual, "id = ?", task.ID)
			if actual.LockedUntil.After(*task.LockedUntil) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("heartbeat never renewed")
			}
			time.Sleep(5 * time.Millisecond)
		}
		db.Model(&orm.ResourceUpdateTask{}).Where("id = ?", task.ID).Update("run_id", "replacement")
		select {
		case <-callCtx.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("lease loss did not cancel handler")
		}
		return taskOutcome{Status: "done"}
	})
	if result.Status == "done" || result.ErrorCode != "task_lease_lost" {
		t.Fatalf("lost lease accepted success: %#v", result)
	}
}

func TestConcurrentOrganizerEnqueueAndReviewClaimHaveOneWinner(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 8; i++ {
		user := time.Now().Format("150405.000000000")
		maintenanceReview(t, db, "review-"+user, user, now)
		w := NewWorker(db, Config{}, "worker")
		var wg sync.WaitGroup
		wg.Add(2)
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := EnqueuePreferenceOrganizer(ctx, db, user, "manual", "manual-"+user, now)
			errs <- err
		}()
		go func() { defer wg.Done(); <-start; _, err := w.claimPending(ctx, now); errs <- err }()
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var running []orm.ResourceUpdateTask
		db.Where("user_id = ? AND status = ?", user, "running").Find(&running)
		if len(running) > 1 {
			t.Fatal("two owners", running)
		}
		if len(running) == 1 {
			if err := w.finishTask(ctx, running[0], taskOutcome{Status: "done"}); err != nil {
				t.Fatal(err)
			}
		}
		db.Where("user_id = ?", user).Delete(&orm.ResourceUpdateTask{})
	}
}

func TestLatestOrganizerReturnsExplicitNullAndUserScopedActiveTask(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	store.Init(db, nil, nil)
	defer store.Init(nil, nil, nil)
	get := func(user string) map[string]any {
		req := httptest.NewRequest("GET", "/memory/preferences:organize", nil)
		req.Header.Set("X-User-Id", user)
		rec := httptest.NewRecorder()
		GetLatestPreferenceOrganizer(rec, req)
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	empty := get("u1")
	if data, exists := empty["data"]; !exists || data != nil {
		t.Fatalf("expected explicit null: %#v", empty)
	}
	now := time.Now().UTC()
	task, _, err := EnqueuePreferenceOrganizer(context.Background(), db, "u1", "manual", "event-1", now)
	if err != nil {
		t.Fatal(err)
	}
	got := get("u1")["data"].(map[string]any)
	if got["task_id"] != task.ID || got["waiting_reason"] != "resources" {
		t.Fatal(got)
	}
	if get("u2")["data"] != nil {
		t.Fatal("cross-user task exposed")
	}
	db.Model(&task).Update("status", "done")
	if get("u1")["data"].(map[string]any)["status"] != "done" {
		t.Fatal("latest terminal task missing")
	}
}
