package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestCheckpointScheduleEngineEnqueuesManualRunAndAllowsDistinctActiveManualScopes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := scheduleTestTime()
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeManual}, nil, now)
	engine := NewCheckpointScheduleEngine(repo, repo, WithClock(func() time.Time { return now }), WithIDGenerator(scheduleIDs()))

	first, err := engine.EnqueueManualSync(ctx, ManualSyncRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		ScopeType: connector.ScopeTypePartial,
		ScopeRef:  connector.ScopeRef{"root_object_key": "folder-1"},
	})
	if err != nil {
		t.Fatalf("enqueue manual sync: %v", err)
	}
	if !first.Created || first.Run.TriggerType != TriggerTypeManual || first.Run.ScopeType != string(connector.ScopeTypePartial) {
		t.Fatalf("unexpected manual run intent: %+v", first)
	}
	if first.Run.ScopeRef["root_object_key"] != "folder-1" || first.Run.Status != store.SyncRunStatusPending {
		t.Fatalf("manual run fields not preserved: %+v", first.Run)
	}

	second, err := engine.EnqueueManualSync(ctx, ManualSyncRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		ScopeType: connector.ScopeTypePartial,
		ScopeRef:  connector.ScopeRef{"root_object_key": "folder-2"},
	})
	if err != nil {
		t.Fatalf("enqueue second manual sync: %v", err)
	}
	if !second.Created || second.Run.RunID == first.Run.RunID {
		t.Fatalf("expected distinct active manual scopes to queue separately, first=%+v second=%+v", first, second)
	}
}

func TestCheckpointScheduleEngineEnqueuesDueRunsAndDedupes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := scheduleTestTime()
	due := now.Add(-time.Minute)
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeScheduled, SchedulePolicy: testSchedulePolicy("UTC", testScheduleRule([]string{"everyday"}, "10:00:00"))}, &due, now)
	engine := NewCheckpointScheduleEngine(repo, repo, WithClock(func() time.Time { return now }), WithIDGenerator(scheduleIDs()))

	intents, err := engine.EnqueueDueSyncRuns(ctx, 10)
	if err != nil {
		t.Fatalf("enqueue due sync runs: %v", err)
	}
	if len(intents) != 1 || !intents[0].Created {
		t.Fatalf("expected one created due run, got %+v", intents)
	}
	if intents[0].Run.TriggerType != TriggerTypeScheduled || intents[0].Run.ScopeType != string(connector.ScopeTypeFull) {
		t.Fatalf("unexpected due run fields: %+v", intents[0].Run)
	}
	if intents[0].Run.ScheduledFireAt == nil || !intents[0].Run.ScheduledFireAt.Equal(due) {
		t.Fatalf("scheduled run did not preserve fire time: %+v want=%v", intents[0].Run.ScheduledFireAt, due)
	}

	again, err := engine.EnqueueDueSyncRuns(ctx, 10)
	if err != nil {
		t.Fatalf("enqueue duplicate due sync runs: %v", err)
	}
	if len(again) != 1 || again[0].Created || again[0].Run.RunID != intents[0].Run.RunID {
		t.Fatalf("expected due run dedupe, first=%+v again=%+v", intents, again)
	}
}

func TestCheckpointScheduleEngineEnqueuesWatchDueRunsAsReconcile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := scheduleTestTime()
	due := now.Add(-time.Minute)
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeWatch}, &due, now)
	engine := NewCheckpointScheduleEngine(repo, repo, WithClock(func() time.Time { return now }), WithIDGenerator(scheduleIDs()))

	intents, err := engine.EnqueueDueSyncRuns(ctx, 10)
	if err != nil {
		t.Fatalf("enqueue watch reconcile run: %v", err)
	}
	if len(intents) != 1 || intents[0].Run.TriggerType != TriggerTypeReconcile || intents[0].Run.ScopeType != string(connector.ScopeTypeFull) {
		t.Fatalf("watch due run should be full reconcile, got %+v", intents)
	}
}

func TestCheckpointScheduleEngineTriggerInitialSyncSkipsWatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := scheduleTestTime()
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeWatch}, nil, now)
	engine := NewCheckpointScheduleEngine(repo, repo, WithClock(func() time.Time { return now }), WithIDGenerator(scheduleIDs()))

	ids, err := engine.TriggerInitialSync(ctx, repo.binding)
	if err != nil {
		t.Fatalf("trigger initial watch sync: %v", err)
	}
	if len(ids) != 0 || len(repo.runs) != 0 {
		t.Fatalf("watch mode should wait for checkpoint reconcile, ids=%v runs=%+v", ids, repo.runs)
	}
}

func TestCheckpointScheduleEngineFinishSuccessAdvancesCheckpoint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := scheduleTestTime()
	due := now.Add(-time.Minute)
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeScheduled, SchedulePolicy: testSchedulePolicy("UTC", testScheduleRule([]string{"everyday"}, "10:00:00"))}, &due, now)
	engine := NewCheckpointScheduleEngine(repo, repo, WithClock(func() time.Time { return now }), WithIDGenerator(scheduleIDs()))
	intents, err := engine.EnqueueDueSyncRuns(ctx, 1)
	if err != nil || len(intents) != 1 {
		t.Fatalf("enqueue due run intents=%+v err=%v", intents, err)
	}
	claimed := repo.claimRun(t, intents[0].Run.RunID, "worker-a")

	finishAt := now.Add(2 * time.Minute)
	engine.clock = func() time.Time { return finishAt }
	finished, ok, err := engine.FinishRun(ctx, FinishRunRequest{
		RunID:          claimed.RunID,
		WorkerID:       "worker-a",
		Cursor:         "cursor-2",
		SeenCount:      2,
		NewCount:       1,
		UnchangedCount: 1,
		Coverage:       store.JSON{"complete": true},
	})
	if err != nil || !ok {
		t.Fatalf("finish run ok=%v err=%v", ok, err)
	}
	if finished.Status != store.SyncRunStatusSucceeded || finished.SeenCount != 2 {
		t.Fatalf("unexpected finished run: %+v", finished)
	}
	checkpoint, err := repo.GetSyncCheckpoint(ctx, "binding-1")
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	wantNext := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	if checkpoint.Cursor != "cursor-2" || checkpoint.NextSyncAt == nil || !checkpoint.NextSyncAt.Equal(wantNext) {
		t.Fatalf("checkpoint was not advanced: %+v want_next=%v", checkpoint, wantNext)
	}
	if checkpoint.LastSuccessAt == nil || !checkpoint.LastSuccessAt.Equal(finishAt) || checkpoint.RetryCount != 0 || checkpoint.LockOwner != "" {
		t.Fatalf("checkpoint success state not recorded: %+v", checkpoint)
	}
}

func TestCheckpointScheduleEngineFinishSuccessGeneratesPendingParseTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := scheduleTestTime()
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeScheduled, SchedulePolicy: testSchedulePolicy("UTC", testScheduleRule([]string{"everyday"}, "10:00:00"))}, &now, now)
	planner := &pendingTaskPlannerStub{}
	engine := NewCheckpointScheduleEngine(
		repo,
		repo,
		WithClock(func() time.Time { return now }),
		WithIDGenerator(scheduleIDs()),
		WithTaskPlanner(planner),
	)
	intents, err := engine.EnqueueDueSyncRuns(ctx, 1)
	if err != nil || len(intents) != 1 {
		t.Fatalf("enqueue due run intents=%+v err=%v", intents, err)
	}
	claimed := repo.claimRun(t, intents[0].Run.RunID, "worker-a")

	_, ok, err := engine.FinishRun(ctx, FinishRunRequest{
		RunID:     claimed.RunID,
		WorkerID:  "worker-a",
		SeenCount: 134,
		NewCount:  34,
		Coverage:  store.JSON{"complete": true},
	})
	if err != nil || !ok {
		t.Fatalf("finish run ok=%v err=%v", ok, err)
	}
	if len(planner.calls) != 1 {
		t.Fatalf("expected one pending task generation call, got %+v", planner.calls)
	}
	call := planner.calls[0]
	if call.sourceID != "source-1" || call.bindingID != "binding-1" || call.runID != claimed.RunID {
		t.Fatalf("pending task generation call lost sync context: %+v", call)
	}
}

func TestCheckpointScheduleEngineFinishFailureRecordsRetryIntent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := scheduleTestTime()
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeManual}, nil, now)
	repo.checkpoint.Cursor = "cursor-before"
	engine := NewCheckpointScheduleEngine(
		repo,
		repo,
		WithClock(func() time.Time { return now }),
		WithIDGenerator(scheduleIDs()),
		WithRetryBackoff(func(int64) time.Duration { return 7 * time.Minute }),
	)
	intent, err := engine.EnqueueManualSync(ctx, ManualSyncRequest{SourceID: "source-1", BindingID: "binding-1"})
	if err != nil {
		t.Fatalf("enqueue manual sync: %v", err)
	}
	claimed := repo.claimRun(t, intent.Run.RunID, "worker-a")

	finished, ok, err := engine.FinishRun(ctx, FinishRunRequest{
		RunID:        claimed.RunID,
		WorkerID:     "worker-a",
		Status:       store.SyncRunStatusFailed,
		Cursor:       "cursor-after-failure",
		ErrorCode:    "FETCH_FAILED",
		ErrorMessage: "temporary connector error",
	})
	if err != nil || !ok {
		t.Fatalf("finish failed run ok=%v err=%v", ok, err)
	}
	if finished.Status != store.SyncRunStatusFailed || finished.ErrorCode != "FETCH_FAILED" {
		t.Fatalf("unexpected failed run: %+v", finished)
	}
	checkpoint, err := repo.GetSyncCheckpoint(ctx, "binding-1")
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	wantRetry := now.Add(7 * time.Minute)
	if checkpoint.RetryCount != 1 || checkpoint.NextSyncAt == nil || !checkpoint.NextSyncAt.Equal(wantRetry) {
		t.Fatalf("failure retry intent not recorded: %+v", checkpoint)
	}
	if checkpoint.LastError["code"] != "FETCH_FAILED" || checkpoint.LockOwner != "" {
		t.Fatalf("failure details not recorded: %+v", checkpoint)
	}
	if checkpoint.Cursor != "cursor-before" {
		t.Fatalf("failed run advanced cursor: %+v", checkpoint)
	}
}

func TestCheckpointScheduleEngineFinishFailureDoesNotGeneratePendingParseTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := scheduleTestTime()
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeManual}, nil, now)
	planner := &pendingTaskPlannerStub{}
	engine := NewCheckpointScheduleEngine(
		repo,
		repo,
		WithClock(func() time.Time { return now }),
		WithIDGenerator(scheduleIDs()),
		WithTaskPlanner(planner),
	)
	intent, err := engine.EnqueueManualSync(ctx, ManualSyncRequest{SourceID: "source-1", BindingID: "binding-1"})
	if err != nil {
		t.Fatalf("enqueue manual sync: %v", err)
	}
	claimed := repo.claimRun(t, intent.Run.RunID, "worker-a")

	_, ok, err := engine.FinishRun(ctx, FinishRunRequest{
		RunID:    claimed.RunID,
		WorkerID: "worker-a",
		Status:   store.SyncRunStatusFailed,
	})
	if err != nil || !ok {
		t.Fatalf("finish failed run ok=%v err=%v", ok, err)
	}
	if len(planner.calls) != 0 {
		t.Fatalf("failed sync run should not generate pending parse tasks: %+v", planner.calls)
	}
}

func TestCheckpointScheduleEngineBuildCheckpointUsesEverydaySchedule(t *testing.T) {
	t.Parallel()

	now := scheduleTestTime()
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeScheduled, SchedulePolicy: testSchedulePolicy("UTC", testScheduleRule([]string{"everyday"}, "10:30:00"))}, nil, now)
	engine := NewCheckpointScheduleEngine(repo, repo)
	checkpoint, err := engine.BuildCheckpoint(context.Background(), store.Binding{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 3,
		SyncMode:          SyncModeScheduled,
		SchedulePolicy:    testSchedulePolicy("UTC", testScheduleRule([]string{"everyday"}, "10:30:00")),
	}, now)
	if err != nil {
		t.Fatalf("build checkpoint: %v", err)
	}
	want := time.Date(2026, 5, 28, 10, 30, 0, 0, time.UTC)
	if checkpoint.NextSyncAt == nil || !checkpoint.NextSyncAt.Equal(want) {
		t.Fatalf("scheduled checkpoint did not compute next sync: %+v", checkpoint)
	}
}

func TestCheckpointScheduleEngineBuildCheckpointUsesPolicyTimezone(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 27, 17, 30, 0, 0, time.UTC)
	policy := testSchedulePolicy("Asia/Shanghai", testScheduleRule([]string{"everyday"}, "02:00:00"))
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeScheduled, SchedulePolicy: policy}, nil, now)
	engine := NewCheckpointScheduleEngine(repo, repo)
	checkpoint, err := engine.BuildCheckpoint(context.Background(), store.Binding{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 3,
		SyncMode:          SyncModeScheduled,
		SchedulePolicy:    policy,
	}, now)
	if err != nil {
		t.Fatalf("build checkpoint: %v", err)
	}
	want := time.Date(2026, 5, 28, 2, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	if checkpoint.NextSyncAt == nil || !checkpoint.NextSyncAt.Equal(want) {
		t.Fatalf("schedule policy did not compute next sync: got=%v want=%v", checkpoint.NextSyncAt, want)
	}
}

func TestCheckpointScheduleEngineRejectsInvalidSchedulePolicy(t *testing.T) {
	t.Parallel()

	now := scheduleTestTime()
	policy := testSchedulePolicy("UTC", testScheduleRule([]string{"everyday"}, "02:00:99"))
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeScheduled, SchedulePolicy: policy}, nil, now)
	engine := NewCheckpointScheduleEngine(repo, repo)
	_, err := engine.BuildCheckpoint(context.Background(), store.Binding{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 3,
		SyncMode:          SyncModeScheduled,
		SchedulePolicy:    policy,
	}, now)
	if err == nil {
		t.Fatal("expected invalid schedule policy to be rejected")
	}
}

func TestNextSyncAtSupportsWeeklySelectorsAndDedupesOverlappingRules(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC) // Monday
	policy := testSchedulePolicy("UTC",
		testScheduleRule([]string{"everyday"}, "02:00:00"),
		testScheduleRule([]string{"workday"}, "02:00:00"),
		testScheduleRule([]string{"mon", "fri"}, "02:00:00"),
		testScheduleRule([]string{"non_workday"}, "03:00:00"),
	)
	next, err := NextSyncAt(policy, now)
	if err != nil {
		t.Fatalf("next sync at: %v", err)
	}
	want := time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("overlapping rules were not deduped to earliest candidate: got=%v want=%v", next, want)
	}

	nextAfterTwo, err := NextSyncAt(policy, time.Date(2026, 6, 1, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("next sync at after exact fire time: %v", err)
	}
	wantAfterTwo := time.Date(2026, 6, 2, 2, 0, 0, 0, time.UTC)
	if !nextAfterTwo.Equal(wantAfterTwo) {
		t.Fatalf("expected exact fire time to advance to next candidate: got=%v want=%v", nextAfterTwo, wantAfterTwo)
	}
}

func TestNextSyncAtSupportsNonWorkday(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 5, 4, 0, 0, 0, time.UTC) // Friday
	policy := testSchedulePolicy("UTC", testScheduleRule([]string{"non_workday"}, "03:00:00"))
	next, err := NextSyncAt(policy, now)
	if err != nil {
		t.Fatalf("next sync at: %v", err)
	}
	want := time.Date(2026, 6, 6, 3, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("non-workday schedule did not pick Saturday: got=%v want=%v", next, want)
	}
}

func TestNextSyncAtRejectsInvalidCalendarAndDay(t *testing.T) {
	t.Parallel()

	if _, err := NextSyncAt(store.JSON{
		"timezone": "UTC",
		"calendar": "cn",
		"rules":    []any{store.JSON{"days": []any{"everyday"}, "time": "02:00:00"}},
	}, scheduleTestTime()); err == nil {
		t.Fatal("expected invalid calendar to be rejected")
	}
	if _, err := NextSyncAt(testSchedulePolicy("UTC", testScheduleRule([]string{"holiday"}, "02:00:00")), scheduleTestTime()); err == nil {
		t.Fatal("expected invalid day selector to be rejected")
	}
}

func TestCheckpointScheduleEngineBuildCheckpointUsesWatchReconcileInterval(t *testing.T) {
	t.Parallel()

	now := scheduleTestTime()
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeWatch}, nil, now)
	engine := NewCheckpointScheduleEngine(repo, repo)
	checkpoint, err := engine.BuildCheckpoint(context.Background(), store.Binding{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 3,
		SyncMode:          SyncModeWatch,
	}, now)
	if err != nil {
		t.Fatalf("build watch checkpoint: %v", err)
	}
	if checkpoint.NextSyncAt == nil || !checkpoint.NextSyncAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("watch checkpoint did not schedule reconcile: %+v", checkpoint)
	}
}

func TestCheckpointScheduleEngineBuildCheckpointKeepsFutureBindingNextSync(t *testing.T) {
	t.Parallel()

	now := scheduleTestTime()
	future := now.Add(3 * time.Minute)
	repo := newScheduleStore(store.Binding{SyncMode: SyncModeWatch}, nil, now)
	engine := NewCheckpointScheduleEngine(repo, repo)
	checkpoint, err := engine.BuildCheckpoint(context.Background(), store.Binding{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 3,
		SyncMode:          SyncModeWatch,
		NextSyncAt:        &future,
	}, now)
	if err != nil {
		t.Fatalf("build watch checkpoint: %v", err)
	}
	if checkpoint.NextSyncAt == nil || !checkpoint.NextSyncAt.Equal(future) {
		t.Fatalf("future binding next_sync_at was not preserved: %+v", checkpoint)
	}
}

type scheduleStore struct {
	binding    store.Binding
	checkpoint store.SyncCheckpoint
	runs       map[string]store.SyncRun
	runWorkers map[string]string
}

func newScheduleStore(binding store.Binding, nextSyncAt *time.Time, now time.Time) *scheduleStore {
	if binding.SourceID == "" {
		binding.SourceID = "source-1"
	}
	if binding.BindingID == "" {
		binding.BindingID = "binding-1"
	}
	if binding.BindingGeneration == 0 {
		binding.BindingGeneration = 1
	}
	if binding.Status == "" {
		binding.Status = "ACTIVE"
	}
	binding.CreatedAt = now
	binding.UpdatedAt = now
	return &scheduleStore{
		binding: binding,
		checkpoint: store.SyncCheckpoint{
			SourceID:          binding.SourceID,
			BindingID:         binding.BindingID,
			BindingGeneration: binding.BindingGeneration,
			NextSyncAt:        nextSyncAt,
			LastError:         store.JSON{},
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		runs:       map[string]store.SyncRun{},
		runWorkers: map[string]string{},
	}
}

func (s *scheduleStore) GetBinding(context.Context, string, string) (store.Binding, error) {
	return s.binding, nil
}

func (s *scheduleStore) GetSyncRun(_ context.Context, runID string) (store.SyncRun, error) {
	run, ok := s.runs[runID]
	if !ok {
		return store.SyncRun{}, store.NewStoreError(store.ErrCodeNotFound, "sync run not found")
	}
	return run, nil
}

func (s *scheduleStore) GetSyncCheckpoint(context.Context, string) (store.SyncCheckpoint, error) {
	return s.checkpoint, nil
}

func (s *scheduleStore) ListDueSyncCheckpoints(_ context.Context, now time.Time, limit int) ([]store.SyncCheckpoint, error) {
	if limit == 0 || s.checkpoint.NextSyncAt == nil || s.checkpoint.NextSyncAt.After(now) || s.binding.Status != "ACTIVE" {
		return nil, nil
	}
	return []store.SyncCheckpoint{s.checkpoint}, nil
}

func (s *scheduleStore) EnqueueSyncRun(_ context.Context, run store.SyncRun) (store.SyncRun, bool, error) {
	if existing, ok := s.runs[run.RunID]; ok {
		return existing, false, nil
	}
	if run.TriggerType != TriggerTypeManual {
		for _, existing := range s.runs {
			if existing.BindingID == run.BindingID && existing.BindingGeneration == run.BindingGeneration {
				switch existing.Status {
				case store.SyncRunStatusPending, store.SyncRunStatusRunning:
					return existing, false, nil
				}
			}
		}
	}
	s.runs[run.RunID] = run
	return run, true, nil
}

func (s *scheduleStore) FinishSyncRun(_ context.Context, runID, workerID string, finish store.SyncRunFinish) (store.SyncRun, bool, error) {
	run, ok := s.runs[runID]
	if !ok || s.runWorkers[runID] != workerID {
		return store.SyncRun{}, false, nil
	}
	run.Status = finish.Status
	run.Coverage = finish.Coverage
	run.SeenCount = finish.SeenCount
	run.NewCount = finish.NewCount
	run.ModifiedCount = finish.ModifiedCount
	run.DeletedCount = finish.DeletedCount
	run.UnchangedCount = finish.UnchangedCount
	run.ErrorCode = finish.ErrorCode
	run.ErrorMessage = finish.ErrorMessage
	run.FinishedAt = &finish.FinishedAt
	s.runs[runID] = run

	s.checkpoint.NextSyncAt = finish.NextSyncAt
	s.checkpoint.LockOwner = ""
	s.checkpoint.LockUntil = nil
	s.checkpoint.UpdatedAt = finish.FinishedAt
	if finish.Status == store.SyncRunStatusSucceeded {
		s.checkpoint.Cursor = finish.Cursor
		s.checkpoint.RetryCount = 0
		s.checkpoint.LastSuccessAt = &finish.FinishedAt
		s.checkpoint.LastError = store.JSON{}
	} else {
		s.checkpoint.RetryCount++
		s.checkpoint.LastError = store.JSON{"code": finish.ErrorCode, "message": finish.ErrorMessage}
	}
	return run, true, nil
}

func (s *scheduleStore) claimRun(t *testing.T, runID, workerID string) store.SyncRun {
	t.Helper()
	run, ok := s.runs[runID]
	if !ok {
		t.Fatalf("run %q not found", runID)
	}
	run.Status = store.SyncRunStatusRunning
	s.runs[runID] = run
	s.runWorkers[runID] = workerID
	s.checkpoint.LockOwner = workerID
	return run
}

func scheduleIDs() func(string) string {
	count := 0
	return func(prefix string) string {
		count++
		return prefix + "-test-" + string(rune('0'+count))
	}
}

func scheduleTestTime() time.Time {
	return time.Date(2026, 5, 28, 9, 0, 0, 0, time.UTC)
}

func testSchedulePolicy(timezone string, rules ...store.JSON) store.JSON {
	items := make([]any, 0, len(rules))
	for _, rule := range rules {
		items = append(items, rule)
	}
	return store.JSON{
		"timezone": timezone,
		"calendar": "weekly",
		"rules":    items,
	}
}

func testScheduleRule(days []string, fireTime string) store.JSON {
	items := make([]any, 0, len(days))
	for _, day := range days {
		items = append(items, day)
	}
	return store.JSON{"days": items, "time": fireTime}
}

type pendingTaskPlannerCall struct {
	sourceID  string
	bindingID string
	runID     string
}

type pendingTaskPlannerStub struct {
	calls []pendingTaskPlannerCall
}

func (p *pendingTaskPlannerStub) GeneratePendingTasks(_ context.Context, sourceID, bindingID, runID string) error {
	p.calls = append(p.calls, pendingTaskPlannerCall{sourceID: sourceID, bindingID: bindingID, runID: runID})
	return nil
}
