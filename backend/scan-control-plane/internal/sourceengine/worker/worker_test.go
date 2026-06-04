package worker_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	statepkg "github.com/lazymind/scan_control_plane/internal/sourceengine/state"
	taskpkg "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestWorkerUsesCoreClientIdempotencyAndSupersede(t *testing.T) {
	t.Run("submits through core client and advances baseline", func(t *testing.T) {
		ctx := context.Background()
		now := time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
		repo := newWorkerIdempotencyStore(now)
		conn := &workerSpyConnector{exportVersion: "v1"}
		registry := mustRegistry(t, conn)
		core := newWorkerIdempotencyCoreClient()
		reducer := statepkg.NewDBStateReducer(repo, statepkg.WithClock(func() time.Time { return now }))
		temp := &workerIdempotencyTempObjectStore{Objects: map[string][]byte{"doc-worker": []byte("content")}}
		task := repo.seedPendingTask("doc-worker", "v1", statepkg.SourceStateNew, statepkg.PendingActionCreate)
		state := repo.states[workerIdempotencyKey("source-1", "binding-1", "doc-worker")]
		state.LastError = store.JSON{"code": "previous", "message": "old failure"}
		repo.states[workerIdempotencyKey("source-1", "binding-1", "doc-worker")] = state
		parseWorker := worker.NewDefaultParseWorker(repo, registry, core, reducer, temp, worker.WithClock(func() time.Time { return now }))

		if err := parseWorker.RunOnce(ctx, "worker-1"); err != nil {
			t.Fatalf("run worker: %v", err)
		}
		if len(core.Submissions) != 1 {
			t.Fatalf("expected one core submission, got %d", len(core.Submissions))
		}
		if core.Submissions[0].IdempotencyKey != task.IdempotencyKey || core.Submissions[0].ContentURI == "" {
			t.Fatalf("core request did not use task idempotency/content uri: %+v", core.Submissions[0])
		}
		if core.Submissions[0].Action != coreclient.ActionCreate || core.Submissions[0].ParentDocumentID != "core-folder-1" || core.Submissions[0].SourceDocumentID != "" {
			t.Fatalf("create task should parse uploaded content under binding root: %+v", core.Submissions[0])
		}
		if core.Submissions[0].UserID != "user-1" {
			t.Fatalf("create task should submit as source owner, got %q", core.Submissions[0].UserID)
		}
		if conn.exportCalls != 1 {
			t.Fatalf("expected connector export once, got %d", conn.exportCalls)
		}
		object := repo.objects[workerIdempotencyKey("source-1", "binding-1", "doc-worker")]
		if object.SizeBytes != int64(len("exported:doc-worker")) {
			t.Fatalf("exported object size was not reflected in source index: %+v", object)
		}
		if !slices.Contains(temp.CleanupTokens, "cleanup-doc-worker") {
			t.Fatalf("expected temp cleanup, got %v", temp.CleanupTokens)
		}
		saved := repo.tasks[task.TaskID]
		if saved.Status != worker.TaskStatusSucceeded {
			t.Fatalf("expected task success, got %+v", saved)
		}
		state = repo.states[workerIdempotencyKey("source-1", "binding-1", "doc-worker")]
		if state.BaselineVersion != "v1" || state.ActiveTaskID != "" || state.SourceState != statepkg.SourceStateUnchanged {
			t.Fatalf("baseline was not advanced correctly: %+v", state)
		}
		if len(state.LastError) != 0 {
			t.Fatalf("successful task should clear previous error: %+v", state.LastError)
		}
	})

	t.Run("recovers submitted task by idempotency without resubmitting", func(t *testing.T) {
		ctx := context.Background()
		now := time.Date(2026, 5, 27, 11, 0, 0, 0, time.UTC)
		repo := newWorkerIdempotencyStore(now)
		conn := &workerSpyConnector{exportVersion: "v2"}
		registry := mustRegistry(t, conn)
		core := newWorkerIdempotencyCoreClient()
		reducer := statepkg.NewDBStateReducer(repo, statepkg.WithClock(func() time.Time { return now }))
		task := repo.seedPendingTask("doc-retry", "v2", statepkg.SourceStateNew, statepkg.PendingActionCreate)
		task.CoreTaskID = "core-task-existing"
		task.CoreDocumentID = "core-doc-existing"
		repo.tasks[task.TaskID] = task
		core.Responses[task.IdempotencyKey] = coreclient.SubmitParseTaskResponse{
			CoreTaskID:     "core-task-existing",
			CoreDocumentID: "core-doc-existing",
			Status:         coreclient.StatusSucceeded,
			VersionID:      "core-version-existing",
		}
		parseWorker := worker.NewDefaultParseWorker(repo, registry, core, reducer, &workerIdempotencyTempObjectStore{}, worker.WithClock(func() time.Time { return now }))

		if err := parseWorker.RunOnce(ctx, "worker-1"); err != nil {
			t.Fatalf("run worker retry: %v", err)
		}
		if len(core.Submissions) != 0 {
			t.Fatalf("expected no duplicate core submit, got %d", len(core.Submissions))
		}
		if conn.exportCalls != 0 {
			t.Fatalf("expected no duplicate export, got %d", conn.exportCalls)
		}
		state := repo.states[workerIdempotencyKey("source-1", "binding-1", "doc-retry")]
		if state.BaselineVersion != "v2" {
			t.Fatalf("idempotency recovery did not finalize baseline: %+v", state)
		}
	})

	t.Run("superseded task does not submit or advance baseline", func(t *testing.T) {
		ctx := context.Background()
		now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
		repo := newWorkerIdempotencyStore(now)
		conn := &workerSpyConnector{exportVersion: "v1"}
		registry := mustRegistry(t, conn)
		core := newWorkerIdempotencyCoreClient()
		reducer := statepkg.NewDBStateReducer(repo, statepkg.WithClock(func() time.Time { return now }))
		task := repo.seedPendingTask("doc-obsolete", "v1", statepkg.SourceStateNew, statepkg.PendingActionCreate)
		state := repo.states[workerIdempotencyKey("source-1", "binding-1", "doc-obsolete")]
		state.SourceVersion = "v2"
		repo.states[workerIdempotencyKey("source-1", "binding-1", "doc-obsolete")] = state
		parseWorker := worker.NewDefaultParseWorker(repo, registry, core, reducer, &workerIdempotencyTempObjectStore{}, worker.WithClock(func() time.Time { return now }))

		if err := parseWorker.RunOnce(ctx, "worker-1"); err != nil {
			t.Fatalf("run superseded worker: %v", err)
		}
		if len(core.Submissions) != 0 || conn.exportCalls != 0 {
			t.Fatalf("superseded task should not export/submit, exports=%d submissions=%d", conn.exportCalls, len(core.Submissions))
		}
		if repo.tasks[task.TaskID].Status != worker.TaskStatusSuperseded {
			t.Fatalf("expected task superseded, got %+v", repo.tasks[task.TaskID])
		}
		state = repo.states[workerIdempotencyKey("source-1", "binding-1", "doc-obsolete")]
		if state.BaselineVersion != "" {
			t.Fatalf("superseded task advanced baseline: %+v", state)
		}
	})
}

func TestWorkerDispatchesReparseAndDeleteCoreActions(t *testing.T) {
	t.Run("modified document exports content and reparses existing core document", func(t *testing.T) {
		ctx := context.Background()
		now := time.Date(2026, 5, 27, 13, 0, 0, 0, time.UTC)
		repo := newWorkerIdempotencyStore(now)
		conn := &workerSpyConnector{exportVersion: "v2"}
		core := newWorkerIdempotencyCoreClient()
		reducer := statepkg.NewDBStateReducer(repo, statepkg.WithClock(func() time.Time { return now }))
		temp := &workerIdempotencyTempObjectStore{Objects: map[string][]byte{"doc-reparse": []byte("content v2")}}
		task := repo.seedPendingTask("doc-reparse", "v2", statepkg.SourceStateModified, statepkg.PendingActionReparse)
		task.TaskAction = taskpkg.TaskActionReparse
		task.TargetVersionID = "v2"
		task.IdempotencyKey = taskpkg.IdempotencyKey(task)
		repo.tasks[task.TaskID] = task
		doc := repo.documents[workerIdempotencyKey("source-1", "binding-1", "doc-reparse")]
		doc.CoreDocumentID = "core-doc-existing"
		doc.CurrentVersionID = "v1"
		repo.documents[workerIdempotencyKey("source-1", "binding-1", "doc-reparse")] = doc
		parseWorker := worker.NewDefaultParseWorker(repo, mustRegistry(t, conn), core, reducer, temp, worker.WithClock(func() time.Time { return now }))

		if err := parseWorker.RunOnce(ctx, "worker-1"); err != nil {
			t.Fatalf("run reparse worker: %v", err)
		}
		if len(core.Submissions) != 1 {
			t.Fatalf("expected one core submission, got %d", len(core.Submissions))
		}
		got := core.Submissions[0]
		if got.Action != coreclient.ActionReparse || got.SourceDocumentID != "core-doc-existing" || got.Content == nil {
			t.Fatalf("reparse task should target existing core document with exported content: %+v", got)
		}
		if got.UserID != "user-1" {
			t.Fatalf("reparse task should submit as source owner, got %q", got.UserID)
		}
		if conn.exportCalls != 1 {
			t.Fatalf("expected export for reparse, got %d", conn.exportCalls)
		}
	})

	t.Run("deleted document skips export and deletes existing core document", func(t *testing.T) {
		ctx := context.Background()
		now := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
		repo := newWorkerIdempotencyStore(now)
		conn := &workerSpyConnector{exportVersion: "v1"}
		core := newWorkerIdempotencyCoreClient()
		reducer := statepkg.NewDBStateReducer(repo, statepkg.WithClock(func() time.Time { return now }))
		task := repo.seedPendingTask("doc-delete", "v1", statepkg.SourceStateDeleted, statepkg.PendingActionDelete)
		task.TaskAction = taskpkg.TaskActionDelete
		task.TargetVersionID = "v1"
		task.IdempotencyKey = taskpkg.IdempotencyKey(task)
		repo.tasks[task.TaskID] = task
		doc := repo.documents[workerIdempotencyKey("source-1", "binding-1", "doc-delete")]
		doc.CoreDocumentID = "core-doc-delete"
		doc.CurrentVersionID = "v1"
		repo.documents[workerIdempotencyKey("source-1", "binding-1", "doc-delete")] = doc
		delete(repo.objects, workerIdempotencyKey("source-1", "binding-1", "doc-delete"))
		parseWorker := worker.NewDefaultParseWorker(repo, mustRegistry(t, conn), core, reducer, &workerIdempotencyTempObjectStore{}, worker.WithClock(func() time.Time { return now }))

		if err := parseWorker.RunOnce(ctx, "worker-1"); err != nil {
			t.Fatalf("run delete worker: %v", err)
		}
		if len(core.Submissions) != 1 {
			t.Fatalf("expected one core delete submission, got %d", len(core.Submissions))
		}
		got := core.Submissions[0]
		if got.Action != coreclient.ActionDelete || got.SourceDocumentID != "core-doc-delete" || got.Content != nil {
			t.Fatalf("delete task should call core document delete without export: %+v", got)
		}
		if got.UserID != "user-1" {
			t.Fatalf("delete task should submit as source owner, got %q", got.UserID)
		}
		if conn.exportCalls != 0 {
			t.Fatalf("delete should not export source content, got %d exports", conn.exportCalls)
		}
	})
}

func TestRunnerRequeuesDeferredSameSourceTasks(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 4, 11, 0, 0, 0, time.UTC)
	repo := newWorkerIdempotencyStore(now)
	conn := &workerSpyConnector{exportVersion: "v1"}
	registry := mustRegistry(t, conn)
	core := newWorkerIdempotencyCoreClient()
	reducer := statepkg.NewDBStateReducer(repo, statepkg.WithClock(func() time.Time { return now }))
	temp := &workerIdempotencyTempObjectStore{Objects: map[string][]byte{
		"doc-a": []byte("content-a"),
		"doc-b": []byte("content-b"),
	}}
	first := repo.seedPendingTask("doc-a", "v1", statepkg.SourceStateNew, statepkg.PendingActionCreate)
	deferred := repo.seedPendingTask("doc-b", "v1", statepkg.SourceStateNew, statepkg.PendingActionCreate)
	parseWorker := worker.NewDefaultParseWorker(repo, registry, core, reducer, temp, worker.WithClock(func() time.Time { return now }))
	runner := worker.NewRunner(parseWorker, worker.WithGlobalConcurrency(2), worker.WithSourceConcurrency(1))

	if err := runner.RunPending(ctx, "worker-1"); err != nil {
		t.Fatalf("run pending: %v", err)
	}
	if got := repo.tasks[first.TaskID].Status; got != worker.TaskStatusSucceeded {
		t.Fatalf("first same-source task should run, got %s", got)
	}
	if got := repo.tasks[deferred.TaskID]; got.Status != worker.TaskStatusPending || got.LeaseOwner != "" || got.LeaseUntil != nil {
		t.Fatalf("deferred same-source task should be requeued, got %+v", got)
	}
}

func TestWorkerRetriesTransientFailureThenDeadLetters(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 15, 0, 0, 0, time.UTC)
	repo := newWorkerIdempotencyStore(now)
	conn := &workerSpyConnector{exportVersion: "v1", exportErr: connector.NewError(connector.ErrorCodeTransient, "temporary export failure")}
	core := newWorkerIdempotencyCoreClient()
	reducer := statepkg.NewDBStateReducer(repo, statepkg.WithClock(func() time.Time { return now }))
	temp := &workerIdempotencyTempObjectStore{}
	task := repo.seedPendingTask("doc-transient", "v1", statepkg.SourceStateNew, statepkg.PendingActionCreate)
	parseWorker := worker.NewDefaultParseWorker(
		repo,
		mustRegistry(t, conn),
		core,
		reducer,
		temp,
		worker.WithClock(func() time.Time { return now }),
		worker.WithDeadLetterAfter(2),
		worker.WithMaxBackoff(time.Minute),
	)

	if err := parseWorker.RunOnce(ctx, "worker-1"); err != nil {
		t.Fatalf("first transient run: %v", err)
	}
	saved := repo.tasks[task.TaskID]
	if saved.Status != worker.TaskStatusPending || saved.RetryCount != 1 || !saved.NextRunAt.Equal(now.Add(time.Second)) {
		t.Fatalf("transient failure should retry with backoff: %+v", saved)
	}
	if _, ok := repo.deadLetters[task.TaskID]; ok {
		t.Fatalf("first retry should not dead-letter")
	}

	saved.NextRunAt = now
	repo.tasks[task.TaskID] = saved
	if err := parseWorker.RunOnce(ctx, "worker-2"); err != nil {
		t.Fatalf("second transient run: %v", err)
	}
	saved = repo.tasks[task.TaskID]
	if saved.Status != worker.TaskStatusFailed || saved.RetryCount != 2 {
		t.Fatalf("second transient failure should fail task: %+v", saved)
	}
	if _, ok := repo.deadLetters[task.TaskID]; !ok {
		t.Fatalf("expected task to be dead-lettered")
	}
	state := repo.states[workerIdempotencyKey("source-1", "binding-1", "doc-transient")]
	if state.ParseQueueState != statepkg.ParseQueueStateFailed {
		t.Fatalf("dead-lettered task should record state failure: %+v", state)
	}
}

type workerIdempotencyStore struct {
	sources     map[string]store.Source
	bindings    map[string]store.Binding
	objects     map[string]store.SourceObject
	states      map[string]store.DocumentState
	documents   map[string]store.Document
	tasks       map[string]store.ParseTask
	deadLetters map[string]store.ParseTask
	now         time.Time
}

func newWorkerIdempotencyStore(now time.Time) *workerIdempotencyStore {
	repo := &workerIdempotencyStore{
		sources:     make(map[string]store.Source),
		bindings:    make(map[string]store.Binding),
		objects:     make(map[string]store.SourceObject),
		states:      make(map[string]store.DocumentState),
		documents:   make(map[string]store.Document),
		tasks:       make(map[string]store.ParseTask),
		deadLetters: make(map[string]store.ParseTask),
		now:         now,
	}
	repo.sources["source-1"] = store.Source{
		SourceID:  "source-1",
		TenantID:  "tenant-1",
		CreatedBy: "user-1",
		Name:      "Docs",
		DatasetID: "dataset-1",
		Status:    "ACTIVE",
		CreatedAt: now,
		UpdatedAt: now,
	}
	repo.bindings[workerIdempotencyBindingKey("source-1", "binding-1")] = store.Binding{
		SourceID:             "source-1",
		BindingID:            "binding-1",
		ConnectorType:        string(workerSpyConnectorType),
		TargetType:           string(workerSpyTargetType),
		TargetRef:            "spy://root",
		TreeKey:              "spy-root",
		BindingGeneration:    1,
		CoreParentDocumentID: "core-folder-1",
		Status:               "ACTIVE",
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	return repo
}

func (s *workerIdempotencyStore) seedPendingTask(objectKey, version, sourceState, pendingAction string) store.ParseTask {
	s.objects[workerIdempotencyKey("source-1", "binding-1", objectKey)] = store.SourceObject{
		SourceID:      "source-1",
		BindingID:     "binding-1",
		TreeKey:       "spy-root",
		ObjectKey:     objectKey,
		DisplayName:   objectKey + ".md",
		SearchName:    objectKey,
		ObjectType:    string(connector.ObjectTypeFile),
		IsDocument:    true,
		SourceVersion: version,
		MimeType:      "text/markdown",
		FileExtension: ".md",
		ProviderMeta:  store.JSON{"object_id": objectKey},
		CreatedAt:     s.now,
		UpdatedAt:     s.now,
	}
	state := docState(objectKey, version, "", sourceState, pendingAction, s.now)
	state.ActiveTaskID = "task-" + objectKey
	s.states[workerIdempotencyKey("source-1", "binding-1", objectKey)] = state
	s.documents[workerIdempotencyKey("source-1", "binding-1", objectKey)] = store.Document{
		DocumentID:       "document-" + objectKey,
		TenantID:         "tenant-1",
		SourceID:         "source-1",
		BindingID:        "binding-1",
		ObjectKey:        objectKey,
		DesiredVersionID: version,
		SourceVersion:    version,
		DisplayName:      objectKey + ".md",
		MimeType:         "text/markdown",
		FileExtension:    ".md",
		ParseStatus:      taskpkg.DocumentParseStatusPending,
		CreatedAt:        s.now,
		UpdatedAt:        s.now,
	}
	parseTask := store.ParseTask{
		TaskID:               "task-" + objectKey,
		TenantID:             "tenant-1",
		SourceID:             "source-1",
		BindingID:            "binding-1",
		BindingGeneration:    1,
		ObjectKey:            objectKey,
		DocumentID:           "document-" + objectKey,
		TaskAction:           taskpkg.TaskActionCreate,
		TargetVersionID:      version,
		SourceVersion:        version,
		CoreParentDocumentID: "core-folder-1",
		Status:               taskpkg.TaskStatusPending,
		NextRunAt:            s.now,
		CreatedAt:            s.now,
		UpdatedAt:            s.now,
	}
	parseTask.IdempotencyKey = taskpkg.IdempotencyKey(parseTask)
	s.tasks[parseTask.TaskID] = parseTask
	return parseTask
}

func (s *workerIdempotencyStore) ClaimDueTask(_ context.Context, workerID string, now time.Time, ttl time.Duration) (store.ParseTask, bool, error) {
	taskIDs := make([]string, 0, len(s.tasks))
	for taskID := range s.tasks {
		taskIDs = append(taskIDs, taskID)
	}
	slices.Sort(taskIDs)
	for _, taskID := range taskIDs {
		task := s.tasks[taskID]
		if _, dead := s.deadLetters[task.TaskID]; dead {
			continue
		}
		if task.Status != taskpkg.TaskStatusPending || task.NextRunAt.After(now) {
			if task.Status == taskpkg.TaskStatusRunning && task.LeaseUntil != nil && !task.LeaseUntil.After(now) {
				task.Status = taskpkg.TaskStatusPending
			} else {
				continue
			}
		}
		task.Status = taskpkg.TaskStatusRunning
		task.LeaseOwner = workerID
		until := now.Add(ttl)
		task.LeaseUntil = &until
		task.UpdatedAt = now
		s.tasks[task.TaskID] = task
		return task, true, nil
	}
	return store.ParseTask{}, false, nil
}

func (s *workerIdempotencyStore) GetSource(_ context.Context, sourceID string) (store.Source, error) {
	source, ok := s.sources[sourceID]
	if !ok {
		return store.Source{}, store.NewStoreError(store.ErrCodeSourceNotFound, "source not found")
	}
	return source, nil
}

func (s *workerIdempotencyStore) GetBinding(_ context.Context, sourceID, bindingID string) (store.Binding, error) {
	binding, ok := s.bindings[workerIdempotencyBindingKey(sourceID, bindingID)]
	if !ok {
		return store.Binding{}, store.NewStoreError(store.ErrCodeBindingNotFound, "binding not found")
	}
	return binding, nil
}

func (s *workerIdempotencyStore) GetDocumentState(_ context.Context, sourceID, bindingID, objectKey string) (store.DocumentState, error) {
	state, ok := s.states[workerIdempotencyKey(sourceID, bindingID, objectKey)]
	if !ok {
		return store.DocumentState{}, store.NewStoreError(store.ErrCodeNotFound, "state not found")
	}
	return state, nil
}

func (s *workerIdempotencyStore) SaveDocumentState(_ context.Context, state store.DocumentState) error {
	s.states[workerIdempotencyKey(state.SourceID, state.BindingID, state.ObjectKey)] = state
	return nil
}

func (s *workerIdempotencyStore) ListDocumentStates(_ context.Context, sourceID, bindingID string) ([]store.DocumentState, error) {
	out := make([]store.DocumentState, 0)
	for _, state := range s.states {
		if state.SourceID == sourceID && state.BindingID == bindingID {
			out = append(out, state)
		}
	}
	return out, nil
}

func (s *workerIdempotencyStore) GetDocument(_ context.Context, sourceID, bindingID, objectKey string) (store.Document, error) {
	document, ok := s.documents[workerIdempotencyKey(sourceID, bindingID, objectKey)]
	if !ok {
		return store.Document{}, store.NewStoreError(store.ErrCodeNotFound, "document not found")
	}
	return document, nil
}

func (s *workerIdempotencyStore) UpdateDocument(_ context.Context, document store.Document) error {
	s.documents[workerIdempotencyKey(document.SourceID, document.BindingID, document.ObjectKey)] = document
	return nil
}

func (s *workerIdempotencyStore) GetObject(_ context.Context, sourceID, bindingID, objectKey string) (store.SourceObject, error) {
	object, ok := s.objects[workerIdempotencyKey(sourceID, bindingID, objectKey)]
	if !ok {
		return store.SourceObject{}, store.NewStoreError(store.ErrCodeNotFound, "object not found")
	}
	return object, nil
}

func (s *workerIdempotencyStore) UpsertObjects(_ context.Context, objects []store.SourceObject) error {
	for _, object := range objects {
		s.objects[workerIdempotencyKey(object.SourceID, object.BindingID, object.ObjectKey)] = object
	}
	return nil
}

func (s *workerIdempotencyStore) SaveParseTask(_ context.Context, task store.ParseTask) error {
	s.tasks[task.TaskID] = task
	return nil
}

func (s *workerIdempotencyStore) ReleaseTaskLease(_ context.Context, taskID, workerID string, nextRunAt time.Time) (store.ParseTask, bool, error) {
	task, ok := s.tasks[taskID]
	if !ok {
		return store.ParseTask{}, false, store.NewStoreError(store.ErrCodeTaskNotFound, "task not found")
	}
	if task.LeaseOwner != workerID {
		return task, false, nil
	}
	if task.Status == taskpkg.TaskStatusRunning {
		task.Status = taskpkg.TaskStatusPending
	}
	task.LeaseOwner = ""
	task.LeaseUntil = nil
	task.NextRunAt = nextRunAt
	task.UpdatedAt = s.now
	s.tasks[taskID] = task
	return task, true, nil
}

func (s *workerIdempotencyStore) SupersedeTask(_ context.Context, taskID string, reason string) error {
	task, ok := s.tasks[taskID]
	if !ok {
		return store.NewStoreError(store.ErrCodeTaskNotFound, "task not found")
	}
	task.Status = taskpkg.TaskStatusSuperseded
	task.LastError = store.JSON{"reason": reason}
	task.UpdatedAt = s.now
	s.tasks[taskID] = task
	return nil
}

func (s *workerIdempotencyStore) FailTask(_ context.Context, taskID string, reason string) error {
	task, ok := s.tasks[taskID]
	if !ok {
		return store.NewStoreError(store.ErrCodeTaskNotFound, "task not found")
	}
	task.Status = taskpkg.TaskStatusFailed
	task.LastError = store.JSON{"reason": reason}
	task.UpdatedAt = s.now
	s.tasks[taskID] = task
	return nil
}

func (s *workerIdempotencyStore) RetryOrDeadLetterTask(_ context.Context, taskID, reason string, now time.Time, maxRetries int64, backoff time.Duration) (store.ParseTask, bool, error) {
	task, ok := s.tasks[taskID]
	if !ok {
		return store.ParseTask{}, false, store.NewStoreError(store.ErrCodeTaskNotFound, "task not found")
	}
	task.RetryCount++
	task.LastError = store.JSON{"reason": reason}
	task.LeaseOwner = ""
	task.LeaseUntil = nil
	task.UpdatedAt = now
	if task.RetryCount >= maxRetries {
		task.Status = taskpkg.TaskStatusFailed
		s.tasks[taskID] = task
		s.deadLetters[taskID] = task
		return task, true, nil
	}
	task.Status = taskpkg.TaskStatusPending
	task.NextRunAt = now.Add(backoff)
	s.tasks[taskID] = task
	return task, false, nil
}

func docState(objectKey, sourceVersion, baselineVersion, sourceState, pendingAction string, now time.Time) store.DocumentState {
	return store.DocumentState{
		SourceID:            "source-1",
		BindingID:           "binding-1",
		BindingGeneration:   1,
		ObjectKey:           objectKey,
		SourceVersion:       sourceVersion,
		BaselineVersion:     baselineVersion,
		SourceState:         sourceState,
		SyncState:           statepkg.SyncStateIdle,
		PendingAction:       pendingAction,
		DocumentListVisible: true,
		Selectable:          true,
		ParseQueueState:     statepkg.ParseQueueStateQueued,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func workerIdempotencyKey(sourceID, bindingID, objectKey string) string {
	return sourceID + "/" + bindingID + "/" + objectKey
}

func workerIdempotencyBindingKey(sourceID, bindingID string) string {
	return sourceID + "/" + bindingID
}

const (
	workerSpyConnectorType connector.ConnectorType = "worker_spy"
	workerSpyTargetType    connector.TargetType    = "worker_spy_root"
)

type workerIdempotencyCoreClient struct {
	Submissions []coreclient.SubmitParseTaskRequest
	Responses   map[string]coreclient.SubmitParseTaskResponse
}

func newWorkerIdempotencyCoreClient() *workerIdempotencyCoreClient {
	return &workerIdempotencyCoreClient{Responses: make(map[string]coreclient.SubmitParseTaskResponse)}
}

func (c *workerIdempotencyCoreClient) SubmitParseTask(ctx context.Context, req coreclient.SubmitParseTaskRequest) (coreclient.SubmitParseTaskResponse, error) {
	if err := ctx.Err(); err != nil {
		return coreclient.SubmitParseTaskResponse{}, err
	}
	if existing, ok := c.Responses[req.IdempotencyKey]; ok {
		existing.Created = false
		return existing, nil
	}
	c.Submissions = append(c.Submissions, req)
	response := coreclient.SubmitParseTaskResponse{
		CoreTaskID:     "core-task-" + req.IdempotencyKey,
		CoreDocumentID: req.SourceDocumentID,
		Status:         coreclient.StatusSucceeded,
		VersionID:      req.IdempotencyKey,
		Created:        true,
	}
	if response.CoreDocumentID == "" {
		response.CoreDocumentID = "core-doc-" + req.IdempotencyKey
	}
	c.Responses[req.IdempotencyKey] = response
	return response, nil
}

func (c *workerIdempotencyCoreClient) GetCoreTaskResult(ctx context.Context, req coreclient.GetCoreTaskResultRequest) (coreclient.CoreTaskResult, error) {
	if err := ctx.Err(); err != nil {
		return coreclient.CoreTaskResult{}, err
	}
	if response, ok := c.Responses[req.IdempotencyKey]; ok {
		return coreclient.CoreTaskResult{
			Status:         response.Status,
			CoreDocumentID: response.CoreDocumentID,
			VersionID:      response.VersionID,
		}, nil
	}
	return coreclient.CoreTaskResult{Status: coreclient.ResultStatusNotFound}, nil
}

type workerIdempotencyTempObjectStore struct {
	CleanupTokens []string
	Objects       map[string][]byte
}

func (s *workerIdempotencyTempObjectStore) Cleanup(ctx context.Context, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.CleanupTokens = append(s.CleanupTokens, token)
	return nil
}

func (s *workerIdempotencyTempObjectStore) Put(ctx context.Context, input worker.TempObjectInput) (worker.TempObject, error) {
	if err := ctx.Err(); err != nil {
		return worker.TempObject{}, err
	}
	content, err := io.ReadAll(input.Reader)
	if err != nil {
		return worker.TempObject{}, err
	}
	if s.Objects == nil {
		s.Objects = map[string][]byte{}
	}
	token := "worker-idempotency-temp"
	s.Objects[token] = content
	return worker.TempObject{
		URI:          "scan-temp://" + token,
		CleanupToken: token,
		SizeBytes:    int64(len(content)),
		CreatedAt:    time.Now().UTC(),
	}, nil
}

func (s *workerIdempotencyTempObjectStore) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	const prefix = "scan-temp://"
	if len(uri) <= len(prefix) || uri[:len(prefix)] != prefix {
		return nil, errors.New("invalid temp object uri")
	}
	content, ok := s.Objects[uri[len(prefix):]]
	if !ok {
		return nil, errors.New("temp object not found")
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

type workerSpyConnector struct {
	exportVersion string
	exportCalls   int
	exportErr     error
}

func (c *workerSpyConnector) Spec() connector.ConnectorSpec {
	return connector.ConnectorSpec{
		ConnectorType:         workerSpyConnectorType,
		TargetTypes:           []connector.TargetType{workerSpyTargetType},
		SupportsExportFormats: []connector.ExportFormat{connector.ExportFormatOriginal},
		MaxPageSize:           100,
	}
}

func (c *workerSpyConnector) ValidateTarget(context.Context, connector.ValidateTargetRequest) (connector.NormalizedTarget, error) {
	return connector.NormalizedTarget{}, errors.New("not used")
}

func (c *workerSpyConnector) ListChildren(context.Context, connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, errors.New("not used")
}

func (c *workerSpyConnector) Search(context.Context, connector.SearchRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, errors.New("not used")
}

func (c *workerSpyConnector) FetchPage(context.Context, connector.FetchPageRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, errors.New("not used")
}

func (c *workerSpyConnector) ExportObject(_ context.Context, req connector.ExportObjectRequest) (connector.ExportedObject, error) {
	c.exportCalls++
	if c.exportErr != nil {
		return connector.ExportedObject{}, c.exportErr
	}
	return connector.ExportedObject{
		ContentURI:      "scan-temp://" + req.ObjectKey,
		MimeType:        "text/markdown",
		FileExtension:   ".md",
		SizeBytes:       int64(len("exported:" + req.ObjectKey)),
		CleanupToken:    "cleanup-" + req.ObjectKey,
		ExportedVersion: c.exportVersion,
	}, nil
}

func (c *workerSpyConnector) MapObject(context.Context, connector.RawObject) (connector.NormalizedSourceObject, error) {
	return connector.NormalizedSourceObject{}, errors.New("not used")
}

func mustRegistry(t *testing.T, connectors ...connector.SourceConnector) connector.ConnectorRegistry {
	t.Helper()
	registry, err := connector.NewDefaultConnectorRegistry(connectors...)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	return registry
}
