package worker

import (
	"context"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	statepkg "github.com/lazymind/scan_control_plane/internal/sourceengine/state"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestCoreResultReconcilerRunningReleasesLeaseForPoll(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	s := newReconcilerStore(store.ParseTask{
		TaskID:         "task-1",
		IdempotencyKey: "idem-1",
		CoreTaskID:     "core-task-1",
		Status:         TaskStatusSubmitted,
		NextRunAt:      now,
	})
	core := newRecordingCoreClient()
	core.Results["idem-1"] = coreclient.CoreTaskResult{Status: coreclient.ResultStatusRunning}
	reducer := &recordingReducer{}
	reconciler := NewCoreResultReconciler(s, core, reducer, WithReconcilerClock(func() time.Time { return now }))
	if err := reconciler.RunOnce(ctx, "worker-a"); err != nil {
		t.Fatalf("run reconciler: %v", err)
	}
	saved := s.tasks["task-1"]
	if saved.Status != TaskStatusSubmitted || saved.LeaseOwner != "" || !saved.NextRunAt.Equal(now.Add(10*time.Second)) {
		t.Fatalf("running result should release for next poll: %+v", saved)
	}
	if len(reducer.successes) != 0 || len(reducer.failures) != 0 {
		t.Fatalf("running result should not write reducer intents")
	}
}

func TestCoreResultReconcilerSubmittedResultReleasesLeaseForPoll(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	s := newReconcilerStore(store.ParseTask{
		TaskID:         "task-1",
		IdempotencyKey: "idem-1",
		CoreTaskID:     "core-task-1",
		Status:         TaskStatusSubmitted,
		NextRunAt:      now,
	})
	core := newRecordingCoreClient()
	core.Results["idem-1"] = coreclient.CoreTaskResult{Status: coreclient.StatusSubmitted}
	reducer := &recordingReducer{}
	reconciler := NewCoreResultReconciler(s, core, reducer, WithReconcilerClock(func() time.Time { return now }))
	if err := reconciler.RunOnce(ctx, "worker-a"); err != nil {
		t.Fatalf("run reconciler: %v", err)
	}
	saved := s.tasks["task-1"]
	if saved.Status != TaskStatusSubmitted || saved.LeaseOwner != "" || !saved.NextRunAt.Equal(now.Add(10*time.Second)) {
		t.Fatalf("submitted result should release for next poll: %+v", saved)
	}
	if len(reducer.successes) != 0 || len(reducer.failures) != 0 {
		t.Fatalf("submitted result should not write reducer intents")
	}
}

func TestCoreResultReconcilerDoneWritesSuccessIntent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	s := newReconcilerStore(store.ParseTask{
		TaskID:         "task-1",
		IdempotencyKey: "idem-1",
		CoreTaskID:     "core-task-1",
		Status:         TaskStatusSubmitted,
		NextRunAt:      now,
	})
	core := newRecordingCoreClient()
	core.Results["idem-1"] = coreclient.CoreTaskResult{
		Status:         coreclient.ResultStatusSucceeded,
		CoreDocumentID: "core-doc-1",
		VersionID:      "v1",
	}
	reducer := &recordingReducer{}
	reconciler := NewCoreResultReconciler(s, core, reducer, WithReconcilerClock(func() time.Time { return now }))
	if err := reconciler.RunOnce(ctx, "worker-a"); err != nil {
		t.Fatalf("run reconciler: %v", err)
	}
	saved := s.tasks["task-1"]
	if saved.Status != TaskStatusSucceeded || saved.CoreDocumentID != "core-doc-1" || saved.LeaseOwner != "" {
		t.Fatalf("unexpected saved task: %+v", saved)
	}
	if len(reducer.successes) != 1 || reducer.successes[0].CoreVersionID != "v1" {
		t.Fatalf("missing success intent: %+v", reducer.successes)
	}
}

func TestCoreResultReconcilerFailedWritesFailureIntent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	s := newReconcilerStore(store.ParseTask{
		TaskID:         "task-1",
		IdempotencyKey: "idem-1",
		CoreTaskID:     "core-task-1",
		Status:         TaskStatusSubmitted,
		NextRunAt:      now,
	})
	core := newRecordingCoreClient()
	core.Results["idem-1"] = coreclient.CoreTaskResult{
		Status:       coreclient.ResultStatusFailed,
		ErrorCode:    "PARSE_FAILED",
		ErrorMessage: "bad file",
	}
	reducer := &recordingReducer{}
	reconciler := NewCoreResultReconciler(s, core, reducer, WithReconcilerClock(func() time.Time { return now }))
	if err := reconciler.RunOnce(ctx, "worker-a"); err != nil {
		t.Fatalf("run reconciler: %v", err)
	}
	saved := s.tasks["task-1"]
	if saved.Status != TaskStatusFailed {
		t.Fatalf("unexpected saved task: %+v", saved)
	}
	if len(reducer.failures) != 1 || reducer.failures[0].ErrorCode != "PARSE_FAILED" {
		t.Fatalf("missing failure intent: %+v", reducer.failures)
	}
	if len(core.Submissions) != 0 {
		t.Fatalf("reconciler must not submit parse tasks, got %+v", core.Submissions)
	}
}

func TestCoreResultReconcilerNotFoundReleasesWithoutFailureIntent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	s := newReconcilerStore(store.ParseTask{
		TaskID:         "task-1",
		IdempotencyKey: "idem-1",
		CoreTaskID:     "core-task-1",
		Status:         TaskStatusSubmitted,
		NextRunAt:      now,
	})
	core := newRecordingCoreClient()
	core.Results["idem-1"] = coreclient.CoreTaskResult{Status: coreclient.ResultStatusNotFound}
	reducer := &recordingReducer{}
	reconciler := NewCoreResultReconciler(s, core, reducer, WithReconcilerClock(func() time.Time { return now }))
	if err := reconciler.RunOnce(ctx, "worker-a"); err != nil {
		t.Fatalf("run reconciler: %v", err)
	}
	saved := s.tasks["task-1"]
	if saved.Status != TaskStatusSubmitted || saved.LeaseOwner != "" || !saved.NextRunAt.Equal(now.Add(10*time.Second)) {
		t.Fatalf("not found should be polled again without failure: %+v", saved)
	}
	if len(reducer.successes) != 0 || len(reducer.failures) != 0 {
		t.Fatalf("not found should not write reducer intents")
	}
}

func TestCoreResultReconcilerSkipsNonSubmittedTasks(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	s := newReconcilerStore(
		store.ParseTask{TaskID: "task-pending", IdempotencyKey: "idem-pending", Status: TaskStatusPending, NextRunAt: now},
		store.ParseTask{TaskID: "task-succeeded", IdempotencyKey: "idem-succeeded", Status: TaskStatusSucceeded, NextRunAt: now},
		store.ParseTask{TaskID: "task-failed", IdempotencyKey: "idem-failed", Status: TaskStatusFailed, NextRunAt: now},
	)
	core := newRecordingCoreClient()
	reducer := &recordingReducer{}
	reconciler := NewCoreResultReconciler(s, core, reducer, WithReconcilerClock(func() time.Time { return now }))
	if err := reconciler.RunOnce(ctx, "worker-a"); err != ErrNoTask {
		t.Fatalf("expected no submitted task, got %v", err)
	}
	if len(core.ResultRequests) != 0 || len(reducer.successes) != 0 || len(reducer.failures) != 0 {
		t.Fatalf("non-submitted tasks should not be polled results=%d successes=%d failures=%d", len(core.ResultRequests), len(reducer.successes), len(reducer.failures))
	}
}

func TestCoreResultReconcilerSuccessIsIdempotentAfterSave(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	s := newReconcilerStore(store.ParseTask{
		TaskID:         "task-1",
		IdempotencyKey: "idem-1",
		CoreTaskID:     "core-task-1",
		Status:         TaskStatusSubmitted,
		NextRunAt:      now,
	})
	core := newRecordingCoreClient()
	core.Results["idem-1"] = coreclient.CoreTaskResult{Status: coreclient.ResultStatusSucceeded, CoreDocumentID: "core-doc-1", VersionID: "v1"}
	reducer := &recordingReducer{}
	reconciler := NewCoreResultReconciler(s, core, reducer, WithReconcilerClock(func() time.Time { return now }))
	if err := reconciler.RunOnce(ctx, "worker-a"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := reconciler.RunOnce(ctx, "worker-b"); err != ErrNoTask {
		t.Fatalf("second run should skip terminal task, got %v", err)
	}
	if len(reducer.successes) != 1 {
		t.Fatalf("success reducer should be idempotent, got %+v", reducer.successes)
	}
}

func TestCoreResultReconcilerSupersedesStaleStateBeforePollingCore(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	task := store.ParseTask{
		TaskID:            "task-1",
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		ObjectKey:         "doc-1",
		DocumentID:        "document-1",
		SourceVersion:     "v1",
		TaskAction:        "CREATE",
		IdempotencyKey:    "idem-1",
		CoreTaskID:        "core-task-1",
		Status:            TaskStatusSubmitted,
		NextRunAt:         now,
	}
	s := newReconcilerStore(task)
	state := s.states["source-1/binding-1/doc-1"]
	state.SourceVersion = "v2"
	s.states["source-1/binding-1/doc-1"] = state
	core := newRecordingCoreClient()
	reducer := &recordingReducer{}
	reconciler := NewCoreResultReconciler(s, core, reducer, WithReconcilerClock(func() time.Time { return now }))

	if err := reconciler.RunOnce(ctx, "worker-a"); err != nil {
		t.Fatalf("run reconciler: %v", err)
	}
	if s.tasks["task-1"].Status != TaskStatusSuperseded {
		t.Fatalf("stale submitted task should be superseded: %+v", s.tasks["task-1"])
	}
	if len(core.ResultRequests) != 0 {
		t.Fatalf("stale task should not poll core, got %+v", core.ResultRequests)
	}
}

type reconcilerStore struct {
	tasks     map[string]store.ParseTask
	sources   map[string]store.Source
	bindings  map[string]store.Binding
	states    map[string]store.DocumentState
	documents map[string]store.Document
	completed map[string]struct{}
	failed    map[string]struct{}
}

func newReconcilerStore(tasks ...store.ParseTask) *reconcilerStore {
	s := &reconcilerStore{
		tasks:     make(map[string]store.ParseTask),
		sources:   make(map[string]store.Source),
		bindings:  make(map[string]store.Binding),
		states:    make(map[string]store.DocumentState),
		documents: make(map[string]store.Document),
		completed: make(map[string]struct{}),
		failed:    make(map[string]struct{}),
	}
	for _, task := range tasks {
		if task.SourceID == "" {
			task.SourceID = "source-1"
		}
		if task.BindingID == "" {
			task.BindingID = "binding-1"
		}
		if task.BindingGeneration == 0 {
			task.BindingGeneration = 1
		}
		if task.ObjectKey == "" {
			task.ObjectKey = "doc-1"
		}
		if task.DocumentID == "" {
			task.DocumentID = "document-1"
		}
		if task.SourceVersion == "" {
			task.SourceVersion = "v1"
		}
		if task.TaskAction == "" {
			task.TaskAction = "CREATE"
		}
		s.tasks[task.TaskID] = task
		s.sources[task.SourceID] = store.Source{SourceID: task.SourceID, DatasetID: "dataset-1", Status: "ACTIVE"}
		s.bindings[task.SourceID+"/"+task.BindingID] = store.Binding{SourceID: task.SourceID, BindingID: task.BindingID, BindingGeneration: task.BindingGeneration, Status: "ACTIVE"}
		s.states[task.SourceID+"/"+task.BindingID+"/"+task.ObjectKey] = store.DocumentState{
			SourceID:          task.SourceID,
			BindingID:         task.BindingID,
			BindingGeneration: task.BindingGeneration,
			ObjectKey:         task.ObjectKey,
			SourceVersion:     task.SourceVersion,
			SourceState:       statepkg.SourceStateNew,
			PendingAction:     statepkg.PendingActionCreate,
			ActiveTaskID:      task.TaskID,
		}
		s.documents[task.SourceID+"/"+task.BindingID+"/"+task.ObjectKey] = store.Document{
			DocumentID: task.DocumentID,
			SourceID:   task.SourceID,
			BindingID:  task.BindingID,
			ObjectKey:  task.ObjectKey,
		}
	}
	return s
}

func (s *reconcilerStore) ClaimSubmittedTask(_ context.Context, workerID string, now time.Time, ttl time.Duration) (store.ParseTask, bool, error) {
	for taskID, task := range s.tasks {
		if task.Status != TaskStatusSubmitted || task.NextRunAt.After(now) {
			continue
		}
		if task.LeaseOwner != "" && task.LeaseUntil != nil && task.LeaseUntil.After(now) {
			continue
		}
		task.LeaseOwner = workerID
		until := now.Add(ttl)
		task.LeaseUntil = &until
		s.tasks[taskID] = task
		return task, true, nil
	}
	return store.ParseTask{}, false, nil
}

func (s *reconcilerStore) GetSource(_ context.Context, sourceID string) (store.Source, error) {
	return s.sources[sourceID], nil
}

func (s *reconcilerStore) GetBinding(_ context.Context, sourceID, bindingID string) (store.Binding, error) {
	return s.bindings[sourceID+"/"+bindingID], nil
}

func (s *reconcilerStore) GetDocumentState(_ context.Context, sourceID, bindingID, objectKey string) (store.DocumentState, error) {
	return s.states[sourceID+"/"+bindingID+"/"+objectKey], nil
}

func (s *reconcilerStore) GetDocument(_ context.Context, sourceID, bindingID, objectKey string) (store.Document, error) {
	return s.documents[sourceID+"/"+bindingID+"/"+objectKey], nil
}

func (s *reconcilerStore) ReleaseTaskLease(_ context.Context, taskID, workerID string, nextRunAt time.Time) (store.ParseTask, bool, error) {
	task := s.tasks[taskID]
	if task.LeaseOwner != workerID {
		return task, false, nil
	}
	task.LeaseOwner = ""
	task.LeaseUntil = nil
	task.NextRunAt = nextRunAt
	s.tasks[taskID] = task
	return task, true, nil
}

func (s *reconcilerStore) SaveParseTask(_ context.Context, task store.ParseTask) error {
	if task.Status == TaskStatusSucceeded {
		if _, ok := s.completed[task.TaskID]; ok {
			s.tasks[task.TaskID] = task
			return nil
		}
		s.completed[task.TaskID] = struct{}{}
	}
	s.tasks[task.TaskID] = task
	return nil
}

func (s *reconcilerStore) FailTask(_ context.Context, taskID string, reason string) error {
	if _, ok := s.failed[taskID]; ok {
		return nil
	}
	task := s.tasks[taskID]
	task.Status = TaskStatusFailed
	task.LastError = store.JSON{"reason": reason}
	task.LeaseOwner = ""
	task.LeaseUntil = nil
	s.tasks[taskID] = task
	s.failed[taskID] = struct{}{}
	return nil
}

func (s *reconcilerStore) SupersedeTask(_ context.Context, taskID string, reason string) error {
	task := s.tasks[taskID]
	task.Status = TaskStatusSuperseded
	task.LeaseOwner = ""
	task.LeaseUntil = nil
	task.LastError = store.JSON{"reason": reason}
	s.tasks[taskID] = task
	return nil
}

type recordingReducer struct {
	successes []statepkg.TaskSuccessInput
	failures  []statepkg.TaskFailureInput
}

func (r *recordingReducer) ApplyTaskSuccess(_ context.Context, input statepkg.TaskSuccessInput) error {
	r.successes = append(r.successes, input)
	return nil
}

func (r *recordingReducer) ApplyTaskFailure(_ context.Context, input statepkg.TaskFailureInput) error {
	r.failures = append(r.failures, input)
	return nil
}

type recordingCoreClient struct {
	Submissions    []coreclient.SubmitParseTaskRequest
	ResultRequests []coreclient.GetCoreTaskResultRequest
	Responses      map[string]coreclient.SubmitParseTaskResponse
	Results        map[string]coreclient.CoreTaskResult
}

func newRecordingCoreClient() *recordingCoreClient {
	return &recordingCoreClient{
		Responses: make(map[string]coreclient.SubmitParseTaskResponse),
		Results:   make(map[string]coreclient.CoreTaskResult),
	}
}

func (c *recordingCoreClient) SubmitParseTask(ctx context.Context, req coreclient.SubmitParseTaskRequest) (coreclient.SubmitParseTaskResponse, error) {
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

func (c *recordingCoreClient) GetCoreTaskResult(ctx context.Context, req coreclient.GetCoreTaskResultRequest) (coreclient.CoreTaskResult, error) {
	if err := ctx.Err(); err != nil {
		return coreclient.CoreTaskResult{}, err
	}
	c.ResultRequests = append(c.ResultRequests, req)
	result, ok := c.Results[req.IdempotencyKey]
	if !ok {
		return coreclient.CoreTaskResult{Status: coreclient.ResultStatusNotFound}, nil
	}
	return result, nil
}
