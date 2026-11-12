package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/common/maintenancegate"
	"lazymind/core/common/orm"
	"lazymind/core/currentmemory"
)

var resourceUpdateEpoch atomic.Uint64

func pauseResourceUpdate(t *testing.T) (*maintenancegate.Ticket, uint64) {
	t.Helper()
	epoch := resourceUpdateEpoch.Add(1) + 1_000_000
	ticket, err := PauseForUpdate(epoch)
	if err != nil {
		t.Fatalf("resourceupdate update participant is unavailable: %v", err)
	}
	t.Cleanup(func() { _ = ResumeFromUpdate(epoch) })
	return ticket, epoch
}

func memoryUpdateWorker(t *testing.T) (*gorm.DB, *Worker) {
	t.Helper()
	db := newResourceUpdateTestDB(t)
	now := time.Now().UTC()
	insertTask(t, db, orm.ResourceUpdateTask{
		ID: "update-memory", TaskType: orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeMemory, UserID: "fixture-owner",
		ResourceID: "fixture-conversation", Status: "pending",
		RequestJSON: json.RawMessage(`{"conversation_id":"fixture-conversation","history":[{"role":"user","content":"synthetic"}]}`),
		NextRunAt:   now, CreatedAt: now, UpdatedAt: now,
	})
	worker := NewWorker(db, Config{WorkerBatchSize: 1, WorkerLockTTL: time.Minute}, "fixture-update-worker")
	worker.loadLLMConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"fixture": true}, nil
	}
	worker.callers.Memory = func(_ context.Context, request algo.MemoryReviewRequest) (*algo.MemoryReviewResponse, int, error) {
		return &algo.MemoryReviewResponse{Status: "success", TaskID: request.TaskID, Outcome: "no_changes"}, http.StatusOK, nil
	}
	return db, worker
}

func TestUpdateParticipantStopsNewClaimsAndResumesDurableQueue(t *testing.T) {
	db, worker := memoryUpdateWorker(t)
	ticket, epoch := pauseResourceUpdate(t)
	result, err := worker.RunOnce(context.Background())
	if err != nil || result.Claimed != 0 {
		t.Fatalf("paused worker claimed a new task: %+v %v", result, err)
	}
	var task orm.ResourceUpdateTask
	if err := db.First(&task, "id = ?", "update-memory").Error; err != nil || task.Status != "pending" || task.AttemptCount != 0 {
		t.Fatalf("pause changed pending task: %+v %v", task, err)
	}
	if err := ticket.Validate(); err != nil {
		t.Fatalf("idle paused participant: %v", err)
	}
	if err := ResumeFromUpdate(epoch + 1); !errors.Is(err, maintenancegate.ErrStale) {
		t.Fatalf("another epoch reopened this pause: %v", err)
	}
	if err := ResumeFromUpdate(epoch); err != nil {
		t.Fatal(err)
	}
	result, err = worker.RunOnce(context.Background())
	if err != nil || result.Done != 1 {
		t.Fatalf("resume did not process retained queue: %+v %v", result, err)
	}
}

func TestUpdateTicketRemainsActiveThroughFinalTaskWrite(t *testing.T) {
	db, worker := memoryUpdateWorker(t)
	entered, algorithmRelease := make(chan struct{}), make(chan struct{})
	finalWrite, persistRelease := make(chan struct{}), make(chan struct{})
	var algorithmOnce, persistOnce, finalOnce sync.Once
	worker.callers.Memory = func(ctx context.Context, request algo.MemoryReviewRequest) (*algo.MemoryReviewResponse, int, error) {
		close(entered)
		select {
		case <-algorithmRelease:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
		return &algo.MemoryReviewResponse{Status: "success", TaskID: request.TaskID, Outcome: "no_changes"}, http.StatusOK, nil
	}
	if err := db.Callback().Update().Before("gorm:update").Register("fixture:hold-final-update", func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if ok && tx.Statement.Table == "resource_update_tasks" && updates["status"] == "done" {
			finalOnce.Do(func() { close(finalWrite) })
			<-persistRelease
		}
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := worker.RunOnce(ctx); done <- err }()
	t.Cleanup(func() {
		cancel()
		algorithmOnce.Do(func() { close(algorithmRelease) })
		persistOnce.Do(func() { close(persistRelease) })
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker did not exit after fixture release")
		}
	})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("real worker never entered the algorithm client")
	}
	ticket, _ := pauseResourceUpdate(t)
	if err := ticket.Validate(); !errors.Is(err, maintenancegate.ErrActive) {
		t.Fatalf("running algorithm disappeared from update activity: %v", err)
	}
	algorithmOnce.Do(func() { close(algorithmRelease) })
	select {
	case <-finalWrite:
	case <-time.After(3 * time.Second):
		t.Fatal("real final task write was not reached")
	}
	if err := ticket.Validate(); !errors.Is(err, maintenancegate.ErrActive) {
		t.Fatalf("algorithm return released work before its final durable write: %v", err)
	}
	persistOnce.Do(func() { close(persistRelease) })
	select {
	case err := <-done:
		done <- err
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not complete")
	}
	if err := ticket.Validate(); err != nil {
		t.Fatalf("finished worker leaked activity: %v", err)
	}
}

func TestUpdateActivityUsesOrganizerFactsWithoutRewritingReservation(t *testing.T) {
	for _, scenario := range []string{"running", "missing", "unknown", "completed", "failed", "other-owner"} {
		t.Run(scenario, func(t *testing.T) {
			db := newResourceUpdateTestDB(t)
			now := time.Now().UTC()
			until := now.Add(time.Minute)
			task := orm.ResourceUpdateTask{
				ID: "update-organizer", UserID: "fixture-owner",
				ResourceType: orm.ResourceUpdateResourceTypeSkill,
				TaskType:     orm.ResourceUpdateTaskTypeOrganizeSkill, Status: "running",
				LockedUntil: &until, CreatedAt: now, UpdatedAt: now,
				RequestJSON: json.RawMessage(`{"requestid":"fixture-request"}`),
			}
			insertTask(t, db, task)
			if scenario != "missing" {
				status, owner := scenario, task.UserID
				if scenario == "running" {
					status = "organize_apply"
				} else if scenario == "other-owner" {
					status, owner = "completed", "different-owner"
				}
				insertSkillReviewStats(t, db, map[string]any{
					"id": "fixture-organize-result", "requestid": "fixture-request", "userid": owner,
					"status": status, "started_at": now.Format(time.RFC3339Nano),
					"duration_ms": 1, "summary": "{}",
				})
			}
			active, err := ActiveForUpdate(context.Background(), db)
			terminal := scenario == "completed" || scenario == "failed"
			if terminal {
				if err != nil || active != 0 {
					t.Fatalf("real terminal stats left stale reservation blocking: active=%d err=%v", active, err)
				}
			} else if err == nil && active == 0 {
				t.Fatal("unproved organizer completion authorized installation")
			}
			var after orm.ResourceUpdateTask
			if err := db.First(&after, "id = ?", task.ID).Error; err != nil || after.Status != "running" {
				t.Fatalf("read-only activity check rewrote reservation history: %+v %v", after, err)
			}
		})
	}
}

func TestUpdatePauseDefersIdleNotificationAndFallbackRecoversIt(t *testing.T) {
	db := newIdleTestDB(t)
	state := newFakeIdleStore()
	now := time.Now().UTC()
	insertIdleEvent(t, db, "fixture-event", "fixture-conversation", "fixture-owner", "fixture-history",
		now.Add(-time.Hour), now.Add(-time.Minute))
	state.history[conversationIdleHistoryKey("fixture-conversation")] = []idleHistoryMessage{
		{Role: "user", Content: "synthetic memory"},
	}
	processor := newIdleProcessorWithStore(db, state, Config{}, "fixture-idle")
	processor.clock = func() time.Time { return now }
	_, epoch := pauseResourceUpdate(t)
	if err := processor.ProcessLatestWaitingConversation(context.Background(), "fixture-conversation"); err != nil {
		t.Fatal(err)
	}
	var event orm.ConversationIdleEvent
	if err := db.First(&event, "event_id = ?", "fixture-event").Error; err != nil || event.Status != "waiting" {
		t.Fatalf("paused notification consumed the event: %+v %v", event, err)
	}
	if err := ResumeFromUpdate(epoch); err != nil {
		t.Fatal(err)
	}
	result, err := processor.RunFallbackOnce(context.Background())
	if err != nil || result.Triggered != 1 {
		t.Fatalf("fallback lost deferred notification: %+v %v", result, err)
	}
	var tasks int64
	if err := db.Model(&orm.ResourceUpdateTask{}).Count(&tasks).Error; err != nil || tasks != 1 {
		t.Fatalf("fallback did not create exactly one durable task: %d %v", tasks, err)
	}
}

func TestUpdatePauseDefersSchedulerUntilResume(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	now := time.Now().UTC()
	start := now.Add(-2 * time.Hour)
	insertConversation(t, db, "fixture-conversation", "fixture-owner", start.Add(time.Minute))
	insertHistory(t, db, "fixture-history-1", "fixture-conversation", start.Add(10*time.Minute), "one", "", 1)
	insertHistory(t, db, "fixture-history-2", "fixture-conversation", start.Add(20*time.Minute), "two", "", 1)
	insertSchedulerState(t, db, orm.SkillReviewSchedulerState{
		UserID: "fixture-owner", LastWindowEnd: start, NextRunAt: now, CreatedAt: now, UpdatedAt: now,
	})
	scheduler := NewScheduler(db, Config{
		MinUserTurns: 2, MinToolTurns: 2, QuantityCheckInterval: time.Second,
		MinInterval: time.Second, MaxWindow: 24 * time.Hour,
		Stages: []Stage{{Window: 4 * time.Hour, Interval: time.Hour, QuantityThreshold: 1, Successes: 1}},
	}, "fixture-scheduler")
	scheduler.clock = func() time.Time { return now }
	_, epoch := pauseResourceUpdate(t)
	result, err := scheduler.RunOnce(context.Background())
	if err != nil || result.CreatedTasks != 0 || result.ClaimedStates != 0 {
		t.Fatalf("paused scheduler dispatched: %+v %v", result, err)
	}
	if err := ResumeFromUpdate(epoch); err != nil {
		t.Fatal(err)
	}
	result, err = scheduler.RunOnce(context.Background())
	if err != nil || result.CreatedTasks != 1 {
		t.Fatalf("resume did not run due work: %+v %v", result, err)
	}
}

func TestUpdatePauseDefersScannerPreferenceWorkUntilResume(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	seedOrganizerIndex(t, db, "fixture-owner", 1258, time.Now().UTC())
	t.Setenv(currentmemory.PreferenceContextMaxCharsEnv, "1000")
	scanner := NewScanner(db, Config{}, "fixture-scanner")
	_, epoch := pauseResourceUpdate(t)
	result, err := scanner.RunOnce(context.Background())
	if err != nil || result.PreferenceTasksCreated != 0 {
		t.Fatalf("paused scanner created work: %+v %v", result, err)
	}
	if err := ResumeFromUpdate(epoch); err != nil {
		t.Fatal(err)
	}
	result, err = scanner.RunOnce(context.Background())
	if err != nil || result.PreferenceTasksCreated != 1 {
		t.Fatalf("resume lost scanner work: %+v %v", result, err)
	}
}
