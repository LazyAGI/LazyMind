package task

import (
	"context"
	"testing"
	"time"

	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
	statepkg "github.com/lazymind/scan_control_plane/internal/sourceengine/state"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestGenerateTasksSkipsContainersAndUnselectableStates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newPlannerStore(now)
	repo.objects["doc"] = sourceObject("doc", true)
	repo.objects["folder"] = sourceObject("folder", false)
	repo.objects["blocked"] = sourceObject("blocked", true)
	repo.states["doc"] = documentState("doc", statepkg.SourceStateNew, true, now)
	repo.states["folder"] = documentState("folder", statepkg.SourceStateNew, true, now)
	repo.states["blocked"] = documentState("blocked", statepkg.SourceStateNew, false, now)
	planner := NewDBTaskPlanner(repo, WithClock(func() time.Time { return now }), WithIDGenerator(repo.nextID))

	result, err := planner.GenerateTasks(ctx, GenerateRequest{
		SourceID:   "source-1",
		BindingID:  "binding-1",
		ObjectKeys: []string{"doc", "folder", "blocked"},
	})
	if err != nil {
		t.Fatalf("generate tasks: %v", err)
	}
	if result.RequestedCount != 3 || result.AcceptedCount != 1 || result.SkippedCount != 2 || result.DuplicateCount != 0 {
		t.Fatalf("unexpected generate result: %+v", result)
	}
	if len(repo.tasks) != 1 {
		t.Fatalf("expected only one task, tasks=%d", len(repo.tasks))
	}
	task := repo.tasks[result.TaskIDs[0]]
	if task.ObjectKey != "doc" || task.TaskAction != TaskActionCreate {
		t.Fatalf("unexpected task generated: %+v", task)
	}
	if task.Status != TaskStatusPending || !task.NextRunAt.Equal(now) {
		t.Fatalf("generated task should be pending and immediately due: %+v", task)
	}
	if repo.states["doc"].ActiveTaskID != task.TaskID || repo.states["doc"].ParseQueueState != statepkg.ParseQueueStateQueued || repo.states["doc"].DocumentID == "" {
		t.Fatalf("accepted state was not linked to task: %+v", repo.states["doc"])
	}
	if repo.states["folder"].ActiveTaskID != "" || repo.states["blocked"].ActiveTaskID != "" {
		t.Fatalf("skipped states should not be linked: folder=%+v blocked=%+v", repo.states["folder"], repo.states["blocked"])
	}
}

func TestGenerateTasksReusesActiveTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newPlannerStore(now)
	repo.objects["doc"] = sourceObject("doc", true)
	repo.states["doc"] = documentState("doc", statepkg.SourceStateNew, true, now)
	existing := store.ParseTask{
		TaskID:            "task-existing",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ObjectKey:         "doc",
		TaskAction:        TaskActionCreate,
		TargetVersionID:   "v1",
		SourceVersion:     "v1",
		Status:            TaskStatusPending,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	existing.IdempotencyKey = IdempotencyKey(existing)
	repo.tasks[existing.TaskID] = existing
	planner := NewDBTaskPlanner(repo, WithClock(func() time.Time { return now }), WithIDGenerator(repo.nextID))

	result, err := planner.GenerateTasks(ctx, GenerateRequest{
		SourceID:   "source-1",
		BindingID:  "binding-1",
		ObjectKeys: []string{"doc"},
	})
	if err != nil {
		t.Fatalf("generate tasks: %v", err)
	}
	if result.AcceptedCount != 0 || result.DuplicateCount != 1 || result.AlreadyActiveCount != 1 || len(result.TaskIDs) != 1 || result.TaskIDs[0] != existing.TaskID {
		t.Fatalf("expected active task reuse, got %+v", result)
	}
	if len(repo.tasks) != 1 {
		t.Fatalf("duplicate should not create new task, tasks=%d", len(repo.tasks))
	}
}

func TestGenerateTasksRejectsManualRequestOverObjectLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newPlannerStore(now)
	repo.run.Coverage = store.JSON{"complete": true, "covered_target_root": true, "scope_type": "full"}
	for _, key := range []string{"doc-1", "doc-2"} {
		repo.objects[key] = sourceObject(key, true)
		repo.states[key] = documentState(key, statepkg.SourceStateNew, true, now)
	}
	planner := NewDBTaskPlanner(
		repo,
		WithClock(func() time.Time { return now }),
		WithIDGenerator(repo.nextID),
		WithMaxObjectsPerGenerateRequest(1),
	)

	result, err := planner.GenerateTasks(ctx, GenerateRequest{
		SourceID:   "source-1",
		BindingID:  "binding-1",
		ObjectKeys: []string{"doc-1", "doc-2"},
	})
	if ErrorCodeOf(err) != ErrCodeParseBatchObjectLimitExceeded {
		t.Fatalf("expected parse batch limit error, result=%+v err=%v", result, err)
	}
	serviceErr, ok := err.(*ServiceError)
	if !ok || serviceErr.Details["limit"] != 1 || serviceErr.Details["actual"] != 2 {
		t.Fatalf("expected limit details, err=%#v", err)
	}
	if len(repo.tasks) != 0 {
		t.Fatalf("manual over-limit request should not create tasks: %+v", repo.tasks)
	}
}

func TestGenerateTasksQueuesManualSyncForSelectedPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newPlannerStore(now)
	syncer := &manualSyncSchedulerStub{}
	planner := NewDBTaskPlanner(
		repo,
		WithClock(func() time.Time { return now }),
		WithIDGenerator(repo.nextID),
		WithManualSyncScheduler(syncer),
	)

	result, err := planner.GenerateTasks(ctx, GenerateRequest{
		CallerID:  "user-1",
		TenantID:  "tenant-1",
		SourceID:  "source-1",
		BindingID: "binding-1",
		Mode:      "partial",
		Paths:     []string{"/workspace/docs/111.txt"},
	})
	if err != nil {
		t.Fatalf("generate selected path: %v", err)
	}
	if result.RequestedCount != 1 || result.AcceptedCount != 1 || result.QueuedSyncCount != 1 || len(result.RunIDs) != 1 {
		t.Fatalf("expected one queued sync run, got %+v", result)
	}
	if len(repo.tasks) != 0 {
		t.Fatalf("selected path should queue sync before parse tasks, got %+v", repo.tasks)
	}
	if len(syncer.calls) != 1 {
		t.Fatalf("expected one sync call, got %+v", syncer.calls)
	}
	call := syncer.calls[0]
	if call.CallerID != "user-1" || call.TenantID != "tenant-1" || call.SourceID != "source-1" || call.BindingID != "binding-1" {
		t.Fatalf("sync call did not preserve actor/source: %+v", call)
	}
	if call.ScopeType != "partial" || call.ScopeRef["path"] != "/workspace/docs/111.txt" {
		t.Fatalf("selected path was not converted to partial sync scope: %+v", call)
	}
}

func TestGenerateTasksQueuesManualSyncForTreeNodeKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newPlannerStore(now)
	syncer := &manualSyncSchedulerStub{}
	planner := NewDBTaskPlanner(
		repo,
		WithClock(func() time.Time { return now }),
		WithIDGenerator(repo.nextID),
		WithManualSyncScheduler(syncer),
	)

	_, err := planner.GenerateTasks(ctx, GenerateRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		Mode:      "partial",
		Paths:     []string{"binding-1:local_fs:agent-1:path:/workspace/docs/111.txt"},
	})
	if err != nil {
		t.Fatalf("generate selected tree node key: %v", err)
	}
	if len(syncer.calls) != 1 {
		t.Fatalf("expected one sync call, got %+v", syncer.calls)
	}
	call := syncer.calls[0]
	if call.ScopeType != "partial" || call.ScopeRef["object_key"] != "local_fs:agent-1:path:/workspace/docs/111.txt" {
		t.Fatalf("tree node key should be converted to object_key scope: %+v", call)
	}
}

func TestGeneratePendingTasksBypassesManualRequestLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newPlannerStore(now)
	for _, key := range []string{"doc-1", "doc-2"} {
		repo.objects[key] = sourceObject(key, true)
		repo.states[key] = documentState(key, statepkg.SourceStateNew, true, now)
	}
	planner := NewDBTaskPlanner(
		repo,
		WithClock(func() time.Time { return now }),
		WithIDGenerator(repo.nextID),
		WithMaxObjectsPerGenerateRequest(1),
	)

	result, err := planner.GeneratePendingTasks(ctx, GeneratePendingRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		RunID:     "run-1",
	})
	if err != nil {
		t.Fatalf("generate pending tasks: %v", err)
	}
	if result.RequestedCount != 2 || result.AcceptedCount != 2 || len(repo.tasks) != 2 {
		t.Fatalf("pending task generation should bypass manual limit, result=%+v tasks=%+v", result, repo.tasks)
	}
}

func TestGeneratePendingTasksUsesPendingActionForDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newPlannerStore(now)
	repo.run.Coverage = store.JSON{"complete": true, "covered_object_keys": []any{"doc-delete"}, "scope_type": "delta"}
	repo.objects["doc-delete"] = sourceObject("doc-delete", true)
	repo.states["doc-delete"] = documentState("doc-delete", statepkg.SourceStateUnchanged, true, now)
	deleteState := repo.states["doc-delete"]
	deleteState.PendingAction = statepkg.PendingActionDelete
	deleteState.BaselineVersion = "v1"
	repo.states["doc-delete"] = deleteState
	planner := NewDBTaskPlanner(repo, WithClock(func() time.Time { return now }), WithIDGenerator(repo.nextID))

	result, err := planner.GeneratePendingTasks(ctx, GeneratePendingRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		RunID:     "run-1",
	})
	if err != nil {
		t.Fatalf("generate pending delete task: %v", err)
	}
	if result.AcceptedCount != 1 || len(result.TaskIDs) != 1 {
		t.Fatalf("expected one delete task, got %+v", result)
	}
	task := repo.tasks[result.TaskIDs[0]]
	if task.TaskAction != TaskActionDelete || task.TargetVersionID != "v1" {
		t.Fatalf("pending delete action was not preserved: %+v", task)
	}
}

func TestGeneratePendingTasksOnlyQueuesCoveredStates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newPlannerStore(now)
	repo.run.Coverage = store.JSON{"complete": true, "covered_object_keys": []any{"doc-covered"}, "scope_type": "delta"}
	for _, key := range []string{"doc-covered", "doc-outside"} {
		repo.objects[key] = sourceObject(key, true)
		repo.states[key] = documentState(key, statepkg.SourceStateNew, true, now)
	}
	planner := NewDBTaskPlanner(repo, WithClock(func() time.Time { return now }), WithIDGenerator(repo.nextID))

	result, err := planner.GeneratePendingTasks(ctx, GeneratePendingRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		RunID:     "run-1",
	})
	if err != nil {
		t.Fatalf("generate pending tasks: %v", err)
	}
	if result.RequestedCount != 1 || result.AcceptedCount != 1 || len(result.TaskIDs) != 1 {
		t.Fatalf("expected only covered pending state queued, got %+v", result)
	}
	task := repo.tasks[result.TaskIDs[0]]
	if task.ObjectKey != "doc-covered" {
		t.Fatalf("queued outside coverage: %+v", task)
	}
}

func TestGeneratePendingTasksRejectsStaleRunGeneration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	repo := newPlannerStore(now)
	repo.run.BindingGeneration = 0
	planner := NewDBTaskPlanner(repo, WithClock(func() time.Time { return now }), WithIDGenerator(repo.nextID))

	_, err := planner.GeneratePendingTasks(ctx, GeneratePendingRequest{
		SourceID:  "source-1",
		BindingID: "binding-1",
		RunID:     "run-1",
	})
	if ErrorCodeOf(err) != ErrCodeTaskSuperseded {
		t.Fatalf("expected stale run generation to be superseded, got %v", err)
	}
}

func TestRetryTaskClearsCoreIDsAndReturnsPendingClaimableTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	retryAt := now.Add(time.Hour)
	repo := newPlannerStore(now)
	leaseUntil := now.Add(time.Minute)
	task := store.ParseTask{
		TaskID:            "task-1",
		TenantID:          "tenant-1",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ObjectKey:         "doc-1",
		DocumentID:        "document-1",
		TaskAction:        TaskActionCreate,
		TargetVersionID:   "v1",
		SourceVersion:     "v1",
		Status:            TaskStatusFailed,
		CoreTaskID:        "core-task-old",
		CoreDocumentID:    "core-document-old",
		LeaseOwner:        "worker-old",
		LeaseUntil:        &leaseUntil,
		NextRunAt:         now.Add(24 * time.Hour),
		LastError:         store.JSON{"reason": "submit failed"},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	task.IdempotencyKey = IdempotencyKey(task)
	repo.tasks[task.TaskID] = task
	repo.objects[task.ObjectKey] = sourceObject(task.ObjectKey, true)
	repo.states[task.ObjectKey] = documentState(task.ObjectKey, statepkg.SourceStateNew, true, now)
	repo.docs[task.ObjectKey] = store.Document{
		DocumentID:       task.DocumentID,
		TenantID:         task.TenantID,
		SourceID:         task.SourceID,
		BindingID:        task.BindingID,
		ObjectKey:        task.ObjectKey,
		CurrentVersionID: "",
		SourceVersion:    task.SourceVersion,
		DisplayName:      "doc-1",
		ParseStatus:      TaskStatusFailed,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	planner := NewDBTaskPlanner(repo, WithClock(func() time.Time { return retryAt }))

	response, err := planner.RetryTask(ctx, RetryRequest{TaskID: task.TaskID})
	if err != nil {
		t.Fatalf("retry task: %v", err)
	}
	if response.Task.Status != TaskStatusPending || response.Task.CoreTaskID != "core-task-old" || response.Task.CoreDocumentID != "core-document-old" {
		t.Fatalf("retry response should preserve core ids for idempotent recovery: %+v", response.Task)
	}
	saved, err := repo.GetParseTask(ctx, task.TaskID)
	if err != nil {
		t.Fatalf("get retried task: %v", err)
	}
	if saved.Task.CoreTaskID != "core-task-old" || saved.Task.CoreDocumentID != "core-document-old" || saved.Task.LeaseOwner != "" || saved.Task.LeaseUntil != nil {
		t.Fatalf("retry should keep core ids and clear lease: %+v", saved.Task)
	}
	if saved.Task.Status != TaskStatusPending || saved.Task.RetryCount != task.RetryCount || !saved.Task.NextRunAt.Equal(retryAt) {
		t.Fatalf("retry should reschedule immediately as pending: %+v", saved.Task)
	}
}

type plannerStore struct {
	source  store.Source
	binding store.Binding
	run     store.SyncRun
	objects map[string]store.SourceObject
	states  map[string]store.DocumentState
	docs    map[string]store.Document
	tasks   map[string]store.ParseTask
	now     time.Time
	next    int
}

type manualSyncSchedulerStub struct {
	calls []sourceengine.TriggerSourceSyncRequest
}

func (s *manualSyncSchedulerStub) TriggerSourceSync(_ context.Context, req sourceengine.TriggerSourceSyncRequest) (sourceengine.TriggerSourceSyncResponse, error) {
	s.calls = append(s.calls, req)
	runID := "sync-run-" + string(rune('0'+len(s.calls)))
	return sourceengine.TriggerSourceSyncResponse{
		RunIDs: []string{runID},
		JobIDs: []string{runID},
		Intents: []sourceengine.SyncRunIntentResponse{{
			RunID:       runID,
			JobID:       runID,
			SourceID:    req.SourceID,
			BindingID:   req.BindingID,
			Status:      store.SyncRunStatusPending,
			TriggerType: "manual",
			ScopeType:   req.ScopeType,
			ScopeRef:    req.ScopeRef,
			Created:     true,
		}},
	}, nil
}

func newPlannerStore(now time.Time) *plannerStore {
	return &plannerStore{
		source: store.Source{
			SourceID:  "source-1",
			TenantID:  "tenant-1",
			DatasetID: "dataset-1",
			Status:    "ACTIVE",
			CreatedAt: now,
			UpdatedAt: now,
		},
		binding: store.Binding{
			SourceID:             "source-1",
			BindingID:            "binding-1",
			BindingGeneration:    1,
			CoreParentDocumentID: "core-folder-1",
			Status:               "ACTIVE",
			CreatedAt:            now,
			UpdatedAt:            now,
		},
		run: store.SyncRun{
			RunID:             "run-1",
			SourceID:          "source-1",
			BindingID:         "binding-1",
			BindingGeneration: 1,
			Status:            store.SyncRunStatusSucceeded,
			Coverage:          store.JSON{"complete": true, "covered_target_root": true, "scope_type": "full"},
			StartedAt:         now,
			FinishedAt:        &now,
		},
		objects: map[string]store.SourceObject{},
		states:  map[string]store.DocumentState{},
		docs:    map[string]store.Document{},
		tasks:   map[string]store.ParseTask{},
		now:     now,
	}
}

func (s *plannerStore) nextID(prefix string) string {
	s.next++
	return prefix + "-test-" + string(rune('0'+s.next))
}

func (s *plannerStore) GetSource(context.Context, string) (store.Source, error) {
	return s.source, nil
}

func (s *plannerStore) GetBinding(context.Context, string, string) (store.Binding, error) {
	return s.binding, nil
}

func (s *plannerStore) ListBindings(context.Context, string) ([]store.Binding, error) {
	return []store.Binding{s.binding}, nil
}

func (s *plannerStore) GetSyncRun(context.Context, string) (store.SyncRun, error) {
	return s.run, nil
}

func (s *plannerStore) ListPendingStates(_ context.Context, _, _ string, objectKeys []string) ([]store.DocumentState, error) {
	want := map[string]struct{}{}
	for _, key := range objectKeys {
		want[key] = struct{}{}
	}
	out := make([]store.DocumentState, 0, len(s.states))
	for _, state := range s.states {
		if len(want) > 0 {
			if _, ok := want[state.ObjectKey]; !ok {
				continue
			}
		}
		out = append(out, state)
	}
	return out, nil
}

func (s *plannerStore) GetDocumentState(_ context.Context, _, _, objectKey string) (store.DocumentState, error) {
	state, ok := s.states[objectKey]
	if !ok {
		return store.DocumentState{}, store.NewStoreError(store.ErrCodeNotFound, "state not found")
	}
	return state, nil
}

func (s *plannerStore) GetObject(_ context.Context, _, _, objectKey string) (store.SourceObject, error) {
	object, ok := s.objects[objectKey]
	if !ok {
		return store.SourceObject{}, store.NewStoreError(store.ErrCodeNotFound, "object not found")
	}
	return object, nil
}

func (s *plannerStore) UpsertDocument(_ context.Context, document store.Document) (store.Document, error) {
	if existing, ok := s.docs[document.ObjectKey]; ok {
		document.DocumentID = existing.DocumentID
		document.CoreDocumentID = existing.CoreDocumentID
		document.CurrentVersionID = existing.CurrentVersionID
		document.CreatedAt = existing.CreatedAt
	}
	s.docs[document.ObjectKey] = document
	return document, nil
}

func (s *plannerStore) FindActiveTask(_ context.Context, sourceID, bindingID, objectKey, targetVersionID, action string) (store.ParseTask, bool, error) {
	for _, task := range s.tasks {
		if task.SourceID == sourceID && task.BindingID == bindingID && task.ObjectKey == objectKey && task.TargetVersionID == targetVersionID && task.TaskAction == action {
			switch task.Status {
			case TaskStatusPending, TaskStatusRunning, TaskStatusSubmitted:
				return task, true, nil
			}
		}
	}
	return store.ParseTask{}, false, nil
}

func (s *plannerStore) CreateParseTask(_ context.Context, task store.ParseTask) error {
	for _, existing := range s.tasks {
		if existing.IdempotencyKey == task.IdempotencyKey {
			return store.NewStoreError(store.ErrCodeIdempotencyKeyReused, "duplicate task")
		}
	}
	s.tasks[task.TaskID] = task
	return nil
}

func (s *plannerStore) SaveDocumentState(_ context.Context, state store.DocumentState) error {
	s.states[state.ObjectKey] = state
	return nil
}

func (s *plannerStore) ListParseTasks(_ context.Context, req store.ParseTaskListRequest) ([]store.ParseTaskWithRefs, int, error) {
	items := make([]store.ParseTaskWithRefs, 0, len(s.tasks))
	for _, task := range s.tasks {
		if req.SourceID != "" && task.SourceID != req.SourceID {
			continue
		}
		if req.BindingID != "" && task.BindingID != req.BindingID {
			continue
		}
		items = append(items, store.ParseTaskWithRefs{Task: task})
	}
	return items, len(items), nil
}

func (s *plannerStore) GetParseTask(_ context.Context, taskID string) (store.ParseTaskWithRefs, error) {
	task, ok := s.tasks[taskID]
	if !ok {
		return store.ParseTaskWithRefs{}, store.NewStoreError(store.ErrCodeTaskNotFound, "parse task not found")
	}
	return store.ParseTaskWithRefs{Task: task}, nil
}

func (s *plannerStore) SaveParseTask(_ context.Context, task store.ParseTask) error {
	s.tasks[task.TaskID] = task
	return nil
}

func sourceObject(objectKey string, isDocument bool) store.SourceObject {
	return store.SourceObject{
		SourceID:      "source-1",
		BindingID:     "binding-1",
		ObjectKey:     objectKey,
		DisplayName:   objectKey,
		IsDocument:    isDocument,
		IsContainer:   !isDocument,
		SourceVersion: "v1",
		CreatedAt:     time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC),
	}
}

func documentState(objectKey, sourceState string, selectable bool, now time.Time) store.DocumentState {
	return store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           objectKey,
		SourceVersion:       "v1",
		SourceState:         sourceState,
		SyncState:           statepkg.SyncStateIdle,
		PendingAction:       statepkg.PendingActionCreate,
		DocumentListVisible: true,
		Selectable:          selectable,
		ParseQueueState:     statepkg.ParseQueueStateNone,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

var _ Store = (*plannerStore)(nil)
