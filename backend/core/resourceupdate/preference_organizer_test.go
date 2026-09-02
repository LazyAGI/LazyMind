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
	claimed, err := worker.claimPending(context.Background(), now)
	if err != nil {
		t.Fatalf("claim pending: %v", err)
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
	now := time.Date(2026, 9, 2, 1, 0, 0, 0, time.UTC)
	insertTask(t, db, orm.ResourceUpdateTask{
		ID: "task-1", TaskType: orm.ResourceUpdateTaskTypeOrganizePreference,
		ResourceType: orm.ResourceUpdateResourceTypeUserPreference, UserID: "user-1",
		TriggerType: orm.ResourceUpdateTriggerTypeManual, TriggerID: "task-1",
		Status: orm.ResourceUpdateTaskStatusRunning, NextRunAt: now,
		LaneKey: MemoryMaintenanceLaneKey("user-1"), LanePriority: PreferenceOrganizerLanePriority,
		LaneOrderAt: now, CreatedAt: now, UpdatedAt: now,
	})

	err := AuthorizePreferenceMutation(context.Background(), db, "user-1", "memory_review_other")
	var organizing *PreferenceOrganizingError
	if !errors.As(err, &organizing) || organizing.TaskID != "task-1" {
		t.Fatalf("ordinary write error = %#v", err)
	}
	if err := AuthorizePreferenceMutation(
		context.Background(), db, "user-1", PreferenceOrganizerAlgorithmTaskID("task-1"),
	); err != nil {
		t.Fatalf("organizer write rejected: %v", err)
	}
	if err := AuthorizePreferenceMutation(context.Background(), db, "user-2", "ordinary"); err != nil {
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
			request.MaxPasses != 2 || request.MaxChanges != 50 {
			t.Fatalf("unexpected organizer request: %#v", request)
		}
		return &algo.PreferenceOrganizerResponse{
			Status: "success", TaskID: request.TaskID, Outcome: "organized",
			Result: map[string]any{
				"current_pass": 2, "passes_attempted": 2, "total_changes": 7,
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
		strings.Contains(string(task.RequestJSON), "secret") {
		t.Fatalf("unexpected persisted task: %#v result=%s", task, task.ResultJSON)
	}
}
