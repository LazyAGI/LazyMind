package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	statepkg "github.com/lazymind/scan_control_plane/internal/sourceengine/state"
	taskpkg "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type DefaultParseWorker struct {
	store           Store
	registry        connector.ConnectorRegistry
	core            coreclient.Client
	reducer         StateReducer
	temp            TempObjectStore
	clock           func() time.Time
	leaseTTL        time.Duration
	maxBackoff      time.Duration
	deadLetterAfter int64
}

type Option func(*DefaultParseWorker)

func NewDefaultParseWorker(store Store, registry connector.ConnectorRegistry, core coreclient.Client, reducer StateReducer, temp TempObjectStore, options ...Option) *DefaultParseWorker {
	if temp == nil {
		panic("parse worker temp object store is required")
	}
	w := &DefaultParseWorker{
		store:           store,
		registry:        registry,
		core:            core,
		reducer:         reducer,
		temp:            temp,
		clock:           time.Now,
		leaseTTL:        60 * time.Second,
		maxBackoff:      10 * time.Minute,
		deadLetterAfter: 3,
	}
	for _, option := range options {
		option(w)
	}
	return w
}

func WithClock(clock func() time.Time) Option {
	return func(w *DefaultParseWorker) {
		if clock != nil {
			w.clock = clock
		}
	}
}

func WithLeaseTTL(ttl time.Duration) Option {
	return func(w *DefaultParseWorker) {
		if ttl > 0 {
			w.leaseTTL = ttl
		}
	}
}

func WithMaxBackoff(backoff time.Duration) Option {
	return func(w *DefaultParseWorker) {
		if backoff > 0 {
			w.maxBackoff = backoff
		}
	}
}

func WithDeadLetterAfter(maxRetries int64) Option {
	return func(w *DefaultParseWorker) {
		if maxRetries > 0 {
			w.deadLetterAfter = maxRetries
		}
	}
}

func (w *DefaultParseWorker) RunOnce(ctx context.Context, workerID string) error {
	task, ok, err := w.claim(ctx, workerID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoTask
	}
	return w.runClaimed(ctx, task)
}

func (w *DefaultParseWorker) claim(ctx context.Context, workerID string) (store.ParseTask, bool, error) {
	task, ok, err := w.store.ClaimDueTask(ctx, workerID, w.clock(), w.leaseTTL)
	if err != nil {
		return store.ParseTask{}, false, err
	}
	if !ok {
		return store.ParseTask{}, false, nil
	}
	return task, true, nil
}

func (w *DefaultParseWorker) release(ctx context.Context, task store.ParseTask) error {
	_, _, err := w.store.ReleaseTaskLease(ctx, task.TaskID, task.LeaseOwner, task.NextRunAt)
	return err
}

func (w *DefaultParseWorker) runClaimed(ctx context.Context, task store.ParseTask) error {
	exec, err := w.loadExecutionContext(ctx, task)
	if err != nil {
		return w.handleFailure(ctx, task, err)
	}
	if err := w.validateTaskFreshness(exec); err != nil {
		return w.supersede(ctx, task, err.Error())
	}
	if task.CoreTaskID != "" || task.CoreDocumentID != "" || task.Status == TaskStatusSubmitted {
		response, err := w.recoverCoreTask(ctx, task, exec.source)
		if err != nil {
			return w.handleFailure(ctx, task, err)
		}
		if response.Status == coreclient.ResultStatusNotFound {
			return w.handleFailure(ctx, task, fmt.Errorf("CORE_TASK_NOT_FOUND"))
		}
		return w.finalize(ctx, task, response)
	}
	if task.TaskAction == taskpkg.TaskActionDelete {
		response, err := w.core.SubmitParseTask(ctx, coreclient.SubmitParseTaskRequest{
			IdempotencyKey:   task.IdempotencyKey,
			DatasetID:        exec.source.DatasetID,
			ParentDocumentID: task.CoreParentDocumentID,
			SourceDocumentID: exec.document.CoreDocumentID,
			UserID:           exec.source.CreatedBy,
			DisplayName:      exec.document.DisplayName,
			Action:           task.TaskAction,
		})
		if err != nil {
			return w.handleFailure(ctx, task, err)
		}
		return w.finalize(ctx, task, response)
	}
	exported, err := w.exportObject(ctx, exec)
	if err != nil {
		if exported.CleanupToken != "" {
			_ = w.temp.Cleanup(ctx, exported.CleanupToken)
		}
		if isSupersedeError(err) {
			return w.supersede(ctx, task, err.Error())
		}
		return w.handleFailure(ctx, task, err)
	}
	if exported.CleanupToken != "" {
		defer func() {
			_ = w.temp.Cleanup(ctx, exported.CleanupToken)
		}()
	}
	if err := w.updateObjectMetadata(ctx, exec, exported); err != nil {
		return w.handleFailure(ctx, task, err)
	}
	response, err := w.submitToCore(ctx, exec, exported)
	if err != nil {
		return w.handleFailure(ctx, task, err)
	}
	return w.finalize(ctx, task, response)
}

type executionContext struct {
	task     store.ParseTask
	source   store.Source
	binding  store.Binding
	state    store.DocumentState
	document store.Document
	object   store.SourceObject
}

func (w *DefaultParseWorker) loadExecutionContext(ctx context.Context, task store.ParseTask) (executionContext, error) {
	source, err := w.store.GetSource(ctx, task.SourceID)
	if err != nil {
		return executionContext{}, err
	}
	binding, err := w.store.GetBinding(ctx, task.SourceID, task.BindingID)
	if err != nil {
		return executionContext{}, err
	}
	docState, err := w.store.GetDocumentState(ctx, task.SourceID, task.BindingID, task.ObjectKey)
	if err != nil {
		return executionContext{}, err
	}
	document, err := w.store.GetDocument(ctx, task.SourceID, task.BindingID, task.ObjectKey)
	if err != nil {
		return executionContext{}, err
	}
	object, err := w.store.GetObject(ctx, task.SourceID, task.BindingID, task.ObjectKey)
	if err != nil && task.TaskAction != taskpkg.TaskActionDelete {
		return executionContext{}, err
	}
	return executionContext{task: task, source: source, binding: binding, state: docState, document: document, object: object}, nil
}

func (w *DefaultParseWorker) validateTaskFreshness(exec executionContext) error {
	if exec.binding.Status != "ACTIVE" {
		return fmt.Errorf("binding is not active")
	}
	if exec.binding.BindingGeneration != exec.task.BindingGeneration {
		return fmt.Errorf("binding generation changed")
	}
	if exec.state.BindingGeneration != exec.task.BindingGeneration {
		return fmt.Errorf("state generation changed")
	}
	if exec.task.TaskAction == taskpkg.TaskActionDelete {
		if exec.state.SourceState != statepkg.SourceStateDeleted || exec.state.PendingAction != statepkg.PendingActionDelete {
			return fmt.Errorf("delete task is obsolete")
		}
		if exec.state.ActiveTaskID != "" && exec.state.ActiveTaskID != exec.task.TaskID {
			return fmt.Errorf("task was replaced")
		}
		return nil
	}
	if exec.state.SourceVersion != exec.task.SourceVersion {
		return fmt.Errorf("source version changed")
	}
	if exec.state.ActiveTaskID != "" && exec.state.ActiveTaskID != exec.task.TaskID {
		return fmt.Errorf("task was replaced")
	}
	return nil
}

func (w *DefaultParseWorker) exportObject(ctx context.Context, exec executionContext) (connector.ExportedObject, error) {
	conn, err := w.registry.Get(connector.ConnectorType(exec.binding.ConnectorType))
	if err != nil {
		return connector.ExportedObject{}, err
	}
	exported, err := conn.ExportObject(ctx, connector.ExportObjectRequest{
		SourceID:          exec.task.SourceID,
		BindingID:         exec.task.BindingID,
		ObjectKey:         exec.task.ObjectKey,
		BindingGeneration: exec.task.BindingGeneration,
		SourceVersion:     exec.task.SourceVersion,
		TargetVersionID:   exec.task.TargetVersionID,
		ExportFormat:      connector.ExportFormatOriginal,
		ProviderMeta:      connectorMeta(exec.object.ProviderMeta),
	})
	if err != nil {
		return connector.ExportedObject{}, err
	}
	if exported.ExportedVersion != exec.task.SourceVersion {
		return exported, fmt.Errorf("exported version changed")
	}
	return exported, nil
}

func (w *DefaultParseWorker) updateObjectMetadata(ctx context.Context, exec executionContext, exported connector.ExportedObject) error {
	object := exec.object
	changed := false
	if exported.SizeBytes > 0 && object.SizeBytes != exported.SizeBytes {
		object.SizeBytes = exported.SizeBytes
		changed = true
	}
	if exported.MimeType != "" && object.MimeType != exported.MimeType {
		object.MimeType = exported.MimeType
		changed = true
	}
	if exported.FileExtension != "" && object.FileExtension != exported.FileExtension {
		object.FileExtension = exported.FileExtension
		changed = true
	}
	if !changed {
		return nil
	}
	object.UpdatedAt = w.clock()
	return w.store.UpsertObjects(ctx, []store.SourceObject{object})
}

func (w *DefaultParseWorker) submitToCore(ctx context.Context, exec executionContext, exported connector.ExportedObject) (coreclient.SubmitParseTaskResponse, error) {
	content, err := w.temp.Open(ctx, exported.ContentURI)
	if err != nil {
		return coreclient.SubmitParseTaskResponse{}, err
	}
	defer content.Close()
	return w.core.SubmitParseTask(ctx, coreclient.SubmitParseTaskRequest{
		IdempotencyKey:   exec.task.IdempotencyKey,
		DatasetID:        exec.source.DatasetID,
		ParentDocumentID: exec.task.CoreParentDocumentID,
		SourceDocumentID: exec.document.CoreDocumentID,
		UserID:           exec.source.CreatedBy,
		DisplayName:      exec.document.DisplayName,
		ContentURI:       exported.ContentURI,
		Content:          content,
		MimeType:         exported.MimeType,
		FileExtension:    exported.FileExtension,
		Action:           exec.task.TaskAction,
	})
}

func (w *DefaultParseWorker) recoverCoreTask(ctx context.Context, task store.ParseTask, source store.Source) (coreclient.SubmitParseTaskResponse, error) {
	if task.CoreTaskID == "" {
		return coreclient.SubmitParseTaskResponse{Status: coreclient.ResultStatusNotFound}, nil
	}
	result, err := w.core.GetCoreTaskResult(ctx, coreclient.GetCoreTaskResultRequest{
		IdempotencyKey: task.IdempotencyKey,
		DatasetID:      source.DatasetID,
		CoreTaskID:     task.CoreTaskID,
		UserID:         source.CreatedBy,
	})
	if err != nil {
		return coreclient.SubmitParseTaskResponse{}, err
	}
	return coreclient.SubmitParseTaskResponse{
		CoreTaskID:     task.CoreTaskID,
		CoreDocumentID: firstNonEmpty(result.CoreDocumentID, task.CoreDocumentID),
		Status:         result.Status,
		VersionID:      result.VersionID,
		Created:        false,
	}, nil
}

func (w *DefaultParseWorker) finalize(ctx context.Context, task store.ParseTask, response coreclient.SubmitParseTaskResponse) error {
	now := w.clock()
	task.CoreTaskID = response.CoreTaskID
	task.CoreDocumentID = response.CoreDocumentID
	task.LeaseOwner = ""
	task.LeaseUntil = nil
	task.UpdatedAt = now
	if response.Status == coreclient.StatusSubmitted {
		task.Status = TaskStatusSubmitted
		return w.store.SaveParseTask(ctx, task)
	}
	task.Status = TaskStatusSucceeded
	if err := w.store.SaveParseTask(ctx, task); err != nil {
		return err
	}
	return w.reducer.ApplyTaskSuccess(ctx, statepkg.TaskSuccessInput{
		Task:           task,
		CoreDocumentID: response.CoreDocumentID,
		CoreVersionID:  response.VersionID,
		CompletedAt:    now,
	})
}

func (w *DefaultParseWorker) supersede(ctx context.Context, task store.ParseTask, reason string) error {
	return w.store.SupersedeTask(ctx, task.TaskID, reason)
}

func (w *DefaultParseWorker) handleFailure(ctx context.Context, task store.ParseTask, err error) error {
	reason := errorCode(err)
	now := w.clock().UTC()
	failed, deadLettered, storeErr := w.store.RetryOrDeadLetterTask(ctx, task.TaskID, reason, now, w.deadLetterAfter, w.backoff(task.RetryCount+1))
	if storeErr != nil {
		return storeErr
	}
	if !deadLettered {
		return nil
	}
	return w.reducer.ApplyTaskFailure(ctx, statepkg.TaskFailureInput{
		Task:      failed,
		ErrorCode: reason,
		Message:   err.Error(),
		FailedAt:  now,
	})
}

func (w *DefaultParseWorker) backoff(retryCount int64) time.Duration {
	if retryCount <= 0 {
		retryCount = 1
	}
	delay := time.Second << min(retryCount-1, 30)
	if delay > w.maxBackoff {
		return w.maxBackoff
	}
	return delay
}

func errorCode(err error) string {
	if err == nil {
		return "UNKNOWN"
	}
	if code := coreclient.ErrorCodeOf(err); code != "" {
		return code
	}
	if code, ok := connector.ErrorCodeOf(err); ok {
		return string(code)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "TRANSIENT"
	}
	text := err.Error()
	if text == "" {
		return "UNKNOWN"
	}
	return text
}

func isSupersedeError(err error) bool {
	if err == nil {
		return false
	}
	code, ok := connector.ErrorCodeOf(err)
	return ok && code == connector.ErrorCodeVersionMismatch
}

func connectorMeta(in store.JSON) connector.ProviderMeta {
	if in == nil {
		return nil
	}
	out := make(connector.ProviderMeta, len(in))
	for key, value := range in {
		if s, ok := value.(string); ok {
			out[key] = s
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
