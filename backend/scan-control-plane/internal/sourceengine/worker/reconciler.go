package worker

import (
	"context"
	"errors"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	statepkg "github.com/lazymind/scan_control_plane/internal/sourceengine/state"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const defaultCoreResultPollInterval = 10 * time.Second

type CoreResultReconciler struct {
	store        CoreResultStore
	core         coreclient.Client
	reducer      StateReducer
	clock        func() time.Time
	leaseTTL     time.Duration
	pollInterval time.Duration
}

type ReconcilerOption func(*CoreResultReconciler)

func NewCoreResultReconciler(store CoreResultStore, core coreclient.Client, reducer StateReducer, options ...ReconcilerOption) *CoreResultReconciler {
	r := &CoreResultReconciler{
		store:        store,
		core:         core,
		reducer:      reducer,
		clock:        time.Now,
		leaseTTL:     60 * time.Second,
		pollInterval: defaultCoreResultPollInterval,
	}
	for _, option := range options {
		option(r)
	}
	return r
}

func WithReconcilerClock(clock func() time.Time) ReconcilerOption {
	return func(r *CoreResultReconciler) {
		if clock != nil {
			r.clock = clock
		}
	}
}

func WithReconcilerPollInterval(interval time.Duration) ReconcilerOption {
	return func(r *CoreResultReconciler) {
		if interval > 0 {
			r.pollInterval = interval
		}
	}
}

func WithReconcilerLeaseTTL(ttl time.Duration) ReconcilerOption {
	return func(r *CoreResultReconciler) {
		if ttl > 0 {
			r.leaseTTL = ttl
		}
	}
}

func (r *CoreResultReconciler) RunOnce(ctx context.Context, workerID string) error {
	now := r.clock()
	task, ok, err := r.store.ClaimSubmittedTask(ctx, workerID, now, r.leaseTTL)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoTask
	}
	source, err := r.store.GetSource(ctx, task.SourceID)
	if err != nil {
		return err
	}
	exec, err := r.loadExecutionContext(ctx, task, source)
	if err != nil {
		return err
	}
	if err := validateReconcilerFreshness(exec); err != nil {
		return r.store.SupersedeTask(ctx, task.TaskID, err.Error())
	}
	result, err := r.core.GetCoreTaskResult(ctx, coreclient.GetCoreTaskResultRequest{
		IdempotencyKey: task.IdempotencyKey,
		DatasetID:      source.DatasetID,
		CoreTaskID:     task.CoreTaskID,
		UserID:         source.CreatedBy,
	})
	if err != nil {
		return err
	}
	switch result.Status {
	case coreclient.ResultStatusRunning:
		_, _, err = r.store.ReleaseTaskLease(ctx, task.TaskID, workerID, now.Add(r.pollInterval))
		return err
	case coreclient.ResultStatusSucceeded:
		return r.complete(ctx, task, result, now)
	case coreclient.ResultStatusFailed:
		return r.fail(ctx, task, result, now)
	default:
		_, _, err = r.store.ReleaseTaskLease(ctx, task.TaskID, workerID, now.Add(r.pollInterval))
		return err
	}
}

type reconcilerExecutionContext struct {
	task     store.ParseTask
	source   store.Source
	binding  store.Binding
	state    store.DocumentState
	document store.Document
}

func (r *CoreResultReconciler) loadExecutionContext(ctx context.Context, task store.ParseTask, source store.Source) (reconcilerExecutionContext, error) {
	binding, err := r.store.GetBinding(ctx, task.SourceID, task.BindingID)
	if err != nil {
		return reconcilerExecutionContext{}, err
	}
	state, err := r.store.GetDocumentState(ctx, task.SourceID, task.BindingID, task.ObjectKey)
	if err != nil {
		return reconcilerExecutionContext{}, err
	}
	document, err := r.store.GetDocument(ctx, task.SourceID, task.BindingID, task.ObjectKey)
	if err != nil {
		return reconcilerExecutionContext{}, err
	}
	return reconcilerExecutionContext{task: task, source: source, binding: binding, state: state, document: document}, nil
}

func validateReconcilerFreshness(exec reconcilerExecutionContext) error {
	if exec.binding.Status != "ACTIVE" {
		return errors.New("binding is not active")
	}
	if exec.binding.BindingGeneration != exec.task.BindingGeneration {
		return errors.New("binding generation changed")
	}
	if exec.state.BindingGeneration != exec.task.BindingGeneration {
		return errors.New("state generation changed")
	}
	if exec.state.ActiveTaskID != "" && exec.state.ActiveTaskID != exec.task.TaskID {
		return errors.New("task was replaced")
	}
	if exec.document.DocumentID != "" && exec.document.DocumentID != exec.task.DocumentID {
		return errors.New("document changed")
	}
	if exec.task.TaskAction != "DELETE" && exec.state.SourceVersion != exec.task.SourceVersion {
		return errors.New("source version changed")
	}
	if exec.task.TaskAction == "DELETE" && (exec.state.SourceState != statepkg.SourceStateDeleted || exec.state.PendingAction != statepkg.PendingActionDelete) {
		return errors.New("delete task is obsolete")
	}
	return nil
}

func (r *CoreResultReconciler) complete(ctx context.Context, task store.ParseTask, result coreclient.CoreTaskResult, now time.Time) error {
	if task.Status == TaskStatusSucceeded {
		return nil
	}
	task.Status = TaskStatusSucceeded
	task.CoreDocumentID = result.CoreDocumentID
	task.LeaseOwner = ""
	task.LeaseUntil = nil
	task.UpdatedAt = now
	if err := r.store.SaveParseTask(ctx, task); err != nil {
		return err
	}
	if err := r.reducer.ApplyTaskSuccess(ctx, statepkg.TaskSuccessInput{
		Task:           task,
		CoreDocumentID: result.CoreDocumentID,
		CoreVersionID:  result.VersionID,
		CompletedAt:    now,
	}); err != nil && !errors.Is(err, statepkg.ErrSuperseded) {
		return err
	}
	return nil
}

func (r *CoreResultReconciler) fail(ctx context.Context, task store.ParseTask, result coreclient.CoreTaskResult, now time.Time) error {
	if task.Status == TaskStatusFailed {
		return nil
	}
	code := result.ErrorCode
	if code == "" {
		code = "CORE_TASK_FAILED"
	}
	if err := r.store.FailTask(ctx, task.TaskID, code); err != nil {
		return err
	}
	return r.reducer.ApplyTaskFailure(ctx, statepkg.TaskFailureInput{
		Task:      task,
		ErrorCode: code,
		Message:   result.ErrorMessage,
		FailedAt:  now,
	})
}
