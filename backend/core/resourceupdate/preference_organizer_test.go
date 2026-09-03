package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common/orm"
	"lazymind/core/maintenance"
)

func TestEnqueuePreferenceOrganizerIsIdempotentWhileActive(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	now := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	first, created, err := EnqueuePreferenceOrganizer(
		context.Background(), db, "user-1", orm.ResourceUpdateTriggerTypeManual, "manual-1", now,
	)
	if err != nil || !created {
		t.Fatalf("first enqueue: created=%v err=%v", created, err)
	}
	second, created, err := EnqueuePreferenceOrganizer(
		context.Background(), db, "user-1", orm.ResourceUpdateTriggerTypeManual, "manual-2", now.Add(time.Second),
	)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second enqueue: first=%s second=%s created=%v err=%v", first.ID, second.ID, created, err)
	}
}

func TestMaintenanceLaneClaimsOrganizerBeforeReviewWithoutBlockingOtherUsers(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	now := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	insertTask(t, db, orm.ResourceUpdateTask{
		ID: "organizer-u1", TaskType: orm.ResourceUpdateTaskTypeOrganizePreference,
		ResourceType: orm.ResourceUpdateResourceTypeUserPreference, UserID: "user-1",
		TriggerType: orm.ResourceUpdateTriggerTypeManual, TriggerID: "organizer-u1",
		Status: orm.ResourceUpdateTaskStatusPending, NextRunAt: now,
		LaneKey: MemoryMaintenanceLaneKey("user-1"), LanePriority: PreferenceOrganizerLanePriority,
		LaneOrderAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	})
	insertTask(t, db, orm.ResourceUpdateTask{
		ID: "review-u1", TaskType: orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeMemory, UserID: "user-1",
		TriggerType: orm.ResourceUpdateTriggerTypeConversationIdle, TriggerID: "review-u1",
		Status: orm.ResourceUpdateTaskStatusPending, NextRunAt: now,
		LaneKey: MemoryMaintenanceLaneKey("user-1"), LanePriority: MemoryReviewLanePriority,
		LaneOrderAt: now, CreatedAt: now, UpdatedAt: now,
	})
	insertTask(t, db, orm.ResourceUpdateTask{
		ID: "organizer-u2", TaskType: orm.ResourceUpdateTaskTypeOrganizePreference,
		ResourceType: orm.ResourceUpdateResourceTypeUserPreference, UserID: "user-2",
		TriggerType: orm.ResourceUpdateTriggerTypeManual, TriggerID: "organizer-u2",
		Status: orm.ResourceUpdateTaskStatusPending, NextRunAt: now,
		LaneKey: MemoryMaintenanceLaneKey("user-2"), LanePriority: PreferenceOrganizerLanePriority,
		LaneOrderAt: now, CreatedAt: now, UpdatedAt: now,
	})
	insertTask(t, db, orm.ResourceUpdateTask{
		ID: "unlaned-old", TaskType: orm.ResourceUpdateTaskTypeGenerateReview,
		ResourceType: orm.ResourceUpdateResourceTypeSkill, UserID: "user-3",
		TriggerType: orm.ResourceUpdateTriggerTypeScheduled, TriggerID: "unlaned-old",
		Status: orm.ResourceUpdateTaskStatusPending, NextRunAt: now,
		CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now,
	})

	worker := NewWorker(db, Config{WorkerBatchSize: 3, WorkerLockTTL: time.Minute}, "lane-worker")
	var claimed []orm.ResourceUpdateTask
	for i := 0; i < 3; i++ {
		next, err := worker.claimPending(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}
		if len(next) != 1 {
			t.Fatalf("must claim exactly one task: %#v", next)
		}
		claimed = append(claimed, next...)
	}
	got := map[string]bool{}
	for _, task := range claimed {
		got[task.ID] = true
	}
	if !got["organizer-u1"] || !got["organizer-u2"] || got["review-u1"] {
		t.Fatalf("claimed tasks = %#v", got)
	}
	if len(claimed) != 3 || claimed[0].ID != "unlaned-old" {
		t.Fatalf("claim order = %#v", claimed)
	}
}

func TestPreferenceOrganizerFreezeOnlyAllowsCurrentAlgorithmTask(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	now := time.Now().UTC()
	until := now.Add(time.Minute)
	insertTask(t, db, orm.ResourceUpdateTask{
		ID: "task-1", RunID: "run-test", LockedUntil: &until, TaskType: orm.ResourceUpdateTaskTypeOrganizePreference,
		ResourceType: orm.ResourceUpdateResourceTypeUserPreference, UserID: "user-1",
		TriggerType: orm.ResourceUpdateTriggerTypeManual, TriggerID: "task-1",
		Status: orm.ResourceUpdateTaskStatusRunning, NextRunAt: now,
		LaneKey: MemoryMaintenanceLaneKey("user-1"), LanePriority: PreferenceOrganizerLanePriority,
		LaneOrderAt: now, CreatedAt: now, UpdatedAt: now,
	})

	authorize := func(ctx context.Context, userID, taskID string) error {
		id := maintenance.Execution(ctx)
		id.TaskID = taskID
		return maintenance.UserTransaction(ctx, db, userID, func(tx *gorm.DB) error {
			return maintenance.Authorize(maintenance.WithIdentity(ctx, id), tx, userID, true)
		})
	}

	err := authorize(context.Background(), "user-1", "ordinary")
	var organizing *maintenance.PreferenceOrganizingError
	if !errors.As(err, &organizing) || organizing.TaskID != "task-1" {
		t.Fatalf("ordinary write error = %#v", err)
	}
	if err := authorize(
		maintenance.WithIdentity(context.Background(), maintenance.Identity{RunID: "run-test"}), "user-1", PreferenceOrganizerAlgorithmTaskID("task-1"),
	); err != nil {
		t.Fatalf("organizer write rejected: %v", err)
	}
	if err := authorize(context.Background(), "user-2", "ordinary"); err != nil {
		t.Fatalf("other user write rejected: %v", err)
	}
}

func TestPreferenceOrganizerWorkerPersistsStructuredResult(t *testing.T) {
	db := newResourceUpdateTestDB(t)
	now := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	requestJSON, err := json.Marshal(DefaultPreferenceOrganizerRequest())
	if err != nil {
		t.Fatal(err)
	}
	insertTask(t, db, orm.ResourceUpdateTask{
		ID: "organizer-worker-1", TaskType: orm.ResourceUpdateTaskTypeOrganizePreference,
		ResourceType: orm.ResourceUpdateResourceTypeUserPreference, UserID: "user-1", ResourceID: "user-1",
		TriggerType: orm.ResourceUpdateTriggerTypeManual, TriggerID: "manual-worker-1",
		Status: orm.ResourceUpdateTaskStatusPending, RequestJSON: requestJSON, NextRunAt: now,
		LaneKey: MemoryMaintenanceLaneKey("user-1"), LanePriority: PreferenceOrganizerLanePriority,
		LaneOrderAt: now, CreatedAt: now, UpdatedAt: now,
	})
	worker := NewWorker(db, Config{
		WorkerBatchSize: 1, WorkerLockTTL: time.Minute, MaxAttempts: 1,
	}, "organizer-worker")
	worker.clock = func() time.Time { return now }
	worker.loadLLMConfig = func(context.Context, *gorm.DB, string) (map[string]any, error) {
		return map[string]any{"llm": map[string]any{"api_key": "secret"}}, nil
	}
	worker.callers.PreferenceOrganizer = func(
		_ context.Context,
		request algo.PreferenceOrganizerRequest,
	) (*algo.PreferenceOrganizerResponse, int, error) {
		if request.TaskID != PreferenceOrganizerAlgorithmTaskID("organizer-worker-1") ||
			request.RunID == "" {
			t.Fatalf("unexpected organizer request: %#v", request)
		}
		return &algo.PreferenceOrganizerResponse{
			Status: "success", TaskID: request.TaskID, Outcome: "organized_with_remaining",
			Result: map[string]any{
				"current_pass": 2, "passes_attempted": 2, "total_changes": 7,
				"target_reached": false, "stop_reason": "no_further_safe_changes",
				"passes": []any{},
			},
		}, http.StatusOK, nil
	}

	result, err := worker.RunOnce(context.Background())
	if err != nil || result.Done != 1 {
		t.Fatalf("worker result=%#v err=%v", result, err)
	}
	var task orm.ResourceUpdateTask
	if err := db.First(&task, "id = ?", "organizer-worker-1").Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != orm.ResourceUpdateTaskStatusDone ||
		!strings.Contains(string(task.ResultJSON), `"passes_attempted":2`) ||
		!strings.Contains(string(task.ResultJSON), `"stop_reason":"no_further_safe_changes"`) ||
		strings.Contains(string(task.RequestJSON), "secret") {
		t.Fatalf("unexpected persisted task: %#v result=%s", task, task.ResultJSON)
	}
}
