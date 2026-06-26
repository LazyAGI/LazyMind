package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/filefilter"
	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
	statepkg "github.com/lazymind/scan_control_plane/internal/sourceengine/state"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type Store interface {
	GetSource(ctx context.Context, sourceID string) (store.Source, error)
	GetBinding(ctx context.Context, sourceID, bindingID string) (store.Binding, error)
	ListBindings(ctx context.Context, sourceID string) ([]store.Binding, error)
	GetSyncRun(ctx context.Context, runID string) (store.SyncRun, error)
	ListPendingStates(ctx context.Context, sourceID, bindingID string, objectKeys []string) ([]store.DocumentState, error)
	GetDocumentState(ctx context.Context, sourceID, bindingID, objectKey string) (store.DocumentState, error)
	GetObject(ctx context.Context, sourceID, bindingID, objectKey string) (store.SourceObject, error)
	UpsertDocument(ctx context.Context, document store.Document) (store.Document, error)
	FindActiveTask(ctx context.Context, sourceID, bindingID, objectKey, targetVersionID, action string) (store.ParseTask, bool, error)
	GetParseTaskByIdempotencyKey(ctx context.Context, idempotencyKey string) (store.ParseTaskWithRefs, error)
	CreateParseTask(ctx context.Context, task store.ParseTask) error
	SaveDocumentState(ctx context.Context, state store.DocumentState) error
	ListParseTasks(ctx context.Context, req store.ParseTaskListRequest) ([]store.ParseTaskWithRefs, int, error)
	GetParseTask(ctx context.Context, taskID string) (store.ParseTaskWithRefs, error)
	SaveParseTask(ctx context.Context, task store.ParseTask) error
	ClearTaskDeadLetter(ctx context.Context, taskID string) error
}

type DBTaskPlanner struct {
	store            Store
	sync             ManualSyncScheduler
	clock            func() time.Time
	newID            func(prefix string) string
	maxManualObjects int
}

type Option func(*DBTaskPlanner)

func NewDBTaskPlanner(store Store, options ...Option) *DBTaskPlanner {
	p := &DBTaskPlanner{
		store: store,
		clock: time.Now,
		newID: func(prefix string) string {
			return prefix + "-" + time.Now().Format("20060102150405.000000000")
		},
		maxManualObjects: DefaultMaxObjectsPerGenerateRequest,
	}
	for _, option := range options {
		option(p)
	}
	return p
}

func WithClock(clock func() time.Time) Option {
	return func(p *DBTaskPlanner) {
		if clock != nil {
			p.clock = clock
		}
	}
}

func WithIDGenerator(newID func(prefix string) string) Option {
	return func(p *DBTaskPlanner) {
		if newID != nil {
			p.newID = newID
		}
	}
}

func WithMaxObjectsPerGenerateRequest(limit int) Option {
	return func(p *DBTaskPlanner) {
		if limit > 0 {
			p.maxManualObjects = limit
		}
	}
}

func WithManualSyncScheduler(sync ManualSyncScheduler) Option {
	return func(p *DBTaskPlanner) {
		p.sync = sync
	}
}

func (p *DBTaskPlanner) SetManualSyncScheduler(sync ManualSyncScheduler) {
	p.sync = sync
}

func (p *DBTaskPlanner) GenerateTasks(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	if p.shouldQueueManualSync(req) {
		return p.queueManualSyncs(ctx, req)
	}
	return p.generateTasks(ctx, req, p.maxManualObjects, false, nil)
}

func (p *DBTaskPlanner) GeneratePendingTasks(ctx context.Context, req GeneratePendingRequest) (GenerateResult, error) {
	run, err := p.store.GetSyncRun(ctx, req.RunID)
	if err != nil {
		return GenerateResult{}, mapStoreError(err)
	}
	if run.Status != store.SyncRunStatusSucceeded {
		return GenerateResult{}, NewError(ErrCodeInvalidRequest, "sync run did not succeed")
	}
	if run.SourceID != req.SourceID || run.BindingID != req.BindingID {
		return GenerateResult{}, NewError(ErrCodeInvalidRequest, "sync run does not match source binding")
	}
	binding, err := p.store.GetBinding(ctx, req.SourceID, req.BindingID)
	if err != nil {
		return GenerateResult{}, mapStoreError(err)
	}
	if binding.Status != "ACTIVE" {
		return GenerateResult{}, NewError(ErrCodeInvalidRequest, "binding is not active")
	}
	if binding.BindingGeneration != run.BindingGeneration {
		return GenerateResult{}, NewError(ErrCodeTaskSuperseded, "sync run generation is stale")
	}
	coverage := newCoverageSelector(run.Coverage)
	if !coverage.complete {
		return GenerateResult{}, NewError(ErrCodeInvalidRequest, "sync run coverage is incomplete")
	}
	return p.generateTasks(ctx, GenerateRequest{
		CallerID:   req.CallerID,
		TenantID:   req.TenantID,
		SourceID:   req.SourceID,
		BindingID:  req.BindingID,
		ObjectKeys: coverage.queryObjectKeys(),
		Priority:   req.Priority,
	}, 0, true, coverage)
}

func (p *DBTaskPlanner) GeneratePendingTasksForRun(ctx context.Context, sourceID, bindingID, runID string) error {
	_, err := p.GeneratePendingTasks(ctx, GeneratePendingRequest{
		SourceID:  sourceID,
		BindingID: bindingID,
		RunID:     runID,
	})
	return err
}

func (p *DBTaskPlanner) shouldQueueManualSync(req GenerateRequest) bool {
	if p.sync == nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "full" || mode == "partial" || mode == "all" {
		return true
	}
	return len(req.Paths) > 0 || len(req.Scopes) > 0
}

func (p *DBTaskPlanner) queueManualSyncs(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	bindingID, err := p.resolveBindingID(ctx, req.SourceID, req.BindingID)
	if err != nil {
		return GenerateResult{}, err
	}
	scopes := manualSyncScopes(req, bindingID)
	if len(scopes) == 0 {
		scopes = []manualSyncScope{{scopeType: string(connector.ScopeTypeFull)}}
	}
	if p.maxManualObjects > 0 && len(scopes) > p.maxManualObjects {
		return GenerateResult{}, parseBatchLimitError(p.maxManualObjects, len(scopes), "request_object_ids")
	}
	syncReqBase := req
	if strings.TrimSpace(syncReqBase.RequestID) == "" {
		syncReqBase.RequestID = p.newID("manual-pull")
	}
	result := GenerateResult{RequestedCount: len(scopes), TaskIDs: []string{}}
	for idx, scope := range scopes {
		syncReq := sourceengine.TriggerSourceSyncRequest{
			CallerID:  req.CallerID,
			TenantID:  req.TenantID,
			RequestID: syncRequestID(syncReqBase, scope, idx),
			SourceID:  req.SourceID,
			BindingID: bindingID,
			ScopeType: scope.scopeType,
			ScopeRef:  scope.scopeRef,
		}
		resp, err := p.sync.TriggerSourceSync(ctx, syncReq)
		if err != nil {
			return result, err
		}
		result.RunIDs = append(result.RunIDs, resp.RunIDs...)
		result.JobIDs = append(result.JobIDs, resp.JobIDs...)
		result.AcceptedCount += len(resp.RunIDs)
		for _, intent := range resp.Intents {
			if !intent.Created {
				result.DuplicateCount++
			}
		}
	}
	if len(result.JobIDs) > 0 {
		result.JobID = result.JobIDs[0]
	}
	result.QueuedSyncCount = len(result.RunIDs)
	return result, nil
}

func (p *DBTaskPlanner) resolveBindingID(ctx context.Context, sourceID, bindingID string) (string, error) {
	if bindingID != "" {
		return bindingID, nil
	}
	bindings, err := p.generateBindings(ctx, sourceID)
	if err != nil {
		return "", err
	}
	if len(bindings) != 1 {
		return "", NewError(ErrCodeInvalidRequest, "binding_id is required when source has multiple bindings")
	}
	return bindings[0].BindingID, nil
}

type manualSyncScope struct {
	scopeType string
	scopeRef  map[string]any
}

func manualSyncScopes(req GenerateRequest, bindingID string) []manualSyncScope {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "full" || mode == "all" || (mode == "" && len(req.Paths) == 0 && len(req.ObjectKeys) == 0 && len(req.Scopes) == 0) {
		return []manualSyncScope{{scopeType: string(connector.ScopeTypeFull)}}
	}
	scopes := make([]manualSyncScope, 0, len(req.Scopes)+len(req.Paths)+len(req.ObjectKeys))
	for _, scope := range req.Scopes {
		if syncScope, ok := manualSyncScopeFromGenerateScope(scope, bindingID); ok {
			scopes = append(scopes, syncScope)
		}
	}
	for _, path := range compactStrings(req.Paths) {
		if objectKey := objectKeyFromTreeKey(path, bindingID); objectKey != "" {
			scopes = append(scopes, manualSyncScope{
				scopeType: string(connector.ScopeTypePartial),
				scopeRef:  map[string]any{"object_key": objectKey},
			})
			continue
		}
		scopes = append(scopes, manualSyncScope{
			scopeType: string(connector.ScopeTypePartial),
			scopeRef:  map[string]any{"path": path},
		})
	}
	for _, objectKey := range compactStrings(req.ObjectKeys) {
		scopes = append(scopes, manualSyncScope{
			scopeType: string(connector.ScopeTypePartial),
			scopeRef:  map[string]any{"object_key": objectKey},
		})
	}
	return scopes
}

func manualSyncScopeFromGenerateScope(scope GenerateScope, bindingID string) (manualSyncScope, bool) {
	scopeRef := map[string]any{}
	objectKey := firstNonBlank(scope.ObjectKey, objectKeyFromTreeKey(scope.Key, bindingID))
	if scope.IsContainer {
		nodeRef := firstNonBlank(scope.NodeRef, scope.Path, objectKey)
		if nodeRef == "" {
			return manualSyncScope{}, false
		}
		scopeRef["node_ref"] = nodeRef
		if objectKey != "" {
			scopeRef["subtree_root"] = objectKey
		}
		return manualSyncScope{scopeType: string(connector.ScopeTypePartial), scopeRef: scopeRef}, true
	}
	if objectKey != "" {
		scopeRef["object_key"] = objectKey
		return manualSyncScope{scopeType: string(connector.ScopeTypePartial), scopeRef: scopeRef}, true
	}
	if path := strings.TrimSpace(scope.Path); path != "" {
		scopeRef["path"] = path
		return manualSyncScope{scopeType: string(connector.ScopeTypePartial), scopeRef: scopeRef}, true
	}
	if nodeRef := strings.TrimSpace(scope.NodeRef); nodeRef != "" {
		scopeRef["node_ref"] = nodeRef
		return manualSyncScope{scopeType: string(connector.ScopeTypePartial), scopeRef: scopeRef}, true
	}
	return manualSyncScope{}, false
}

func objectKeyFromTreeKey(key, bindingID string) string {
	key = strings.TrimSpace(key)
	bindingID = strings.TrimSpace(bindingID)
	if key == "" || bindingID == "" || !strings.HasPrefix(key, bindingID+":") {
		return ""
	}
	return strings.TrimPrefix(key, bindingID+":")
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func compactStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func syncRequestID(req GenerateRequest, scope manualSyncScope, idx int) string {
	if req.RequestID != "" && idx == 0 {
		return req.RequestID
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%v", req.RequestID, req.SourceID, idx, scope.scopeType, scope.scopeRef)))
	return "manual-pull-" + hex.EncodeToString(sum[:12])
}

func (p *DBTaskPlanner) generateTasks(ctx context.Context, req GenerateRequest, maxObjects int, requirePendingAction bool, coverage *coverageSelector) (GenerateResult, error) {
	requestedObjects := len(req.ObjectKeys)
	if requestedObjects == 0 {
		requestedObjects = len(req.DocumentIDs)
	}
	if maxObjects > 0 && requestedObjects > maxObjects {
		return GenerateResult{}, parseBatchLimitError(maxObjects, requestedObjects, "request_object_ids")
	}
	source, err := p.store.GetSource(ctx, req.SourceID)
	if err != nil {
		return GenerateResult{}, mapStoreError(err)
	}
	bindingID := req.BindingID
	if bindingID == "" {
		bindings, err := p.generateBindings(ctx, req.SourceID)
		if err != nil {
			return GenerateResult{}, err
		}
		if len(bindings) != 1 {
			return GenerateResult{}, NewError(ErrCodeInvalidRequest, "binding_id is required when source has multiple bindings")
		}
		bindingID = bindings[0].BindingID
	}
	binding, err := p.store.GetBinding(ctx, req.SourceID, bindingID)
	if err != nil {
		return GenerateResult{}, mapStoreError(err)
	}
	if binding.Status != "ACTIVE" {
		return GenerateResult{}, NewError(ErrCodeInvalidRequest, "binding is not active")
	}
	objectKeys := req.ObjectKeys
	if len(objectKeys) == 0 && len(req.DocumentIDs) > 0 {
		objectKeys, err = p.objectKeysForDocuments(ctx, req.SourceID, bindingID, req.DocumentIDs)
		if err != nil {
			return GenerateResult{}, err
		}
	}
	states, err := p.store.ListPendingStates(ctx, req.SourceID, bindingID, objectKeys)
	if err != nil {
		return GenerateResult{}, mapStoreError(err)
	}
	if maxObjects > 0 && len(states) > maxObjects {
		return GenerateResult{}, parseBatchLimitError(maxObjects, len(states), "resolved_object_ids")
	}
	result := GenerateResult{RequestedCount: len(states)}
	policy := filefilter.FromSourceBinding(source, binding)
	for _, docState := range states {
		if requirePendingAction && docState.PendingAction == "" {
			result.SkippedCount++
			continue
		}
		action := actionForState(docState)
		if action == "" {
			result.SkippedCount++
			continue
		}
		object, err := p.store.GetObject(ctx, req.SourceID, bindingID, docState.ObjectKey)
		if err != nil {
			return result, mapStoreError(err)
		}
		if coverage != nil && !p.objectCovered(ctx, req.SourceID, bindingID, object, coverage) {
			result.SkippedCount++
			continue
		}
		if !canGenerateTaskForObject(policy, object, docState, action) {
			result.SkippedCount++
			continue
		}
		document, err := p.store.UpsertDocument(ctx, p.documentForState(source, object, docState, action))
		if err != nil {
			return result, err
		}
		parseTask := p.taskForState(source, binding, object, docState, document, action)
		if existing, ok, err := p.store.FindActiveTask(ctx, parseTask.SourceID, parseTask.BindingID, parseTask.ObjectKey, parseTask.TargetVersionID, parseTask.TaskAction); err != nil {
			return result, mapStoreError(err)
		} else if ok {
			docState.ActiveTaskID = existing.TaskID
			docState.ParseQueueState = statepkg.ParseQueueStateQueued
			docState.DocumentID = document.DocumentID
			docState.UpdatedAt = p.clock()
			if err := p.store.SaveDocumentState(ctx, docState); err != nil {
				return result, mapStoreError(err)
			}
			result.DuplicateCount++
			result.AlreadyActiveCount++
			result.TaskIDs = append(result.TaskIDs, existing.TaskID)
			continue
		}
		if existing, ok, err := p.restoreFailedTask(ctx, parseTask, docState, document); err != nil {
			return result, err
		} else if ok {
			result.AcceptedCount++
			result.TaskIDs = append(result.TaskIDs, existing.TaskID)
			continue
		}
		if err := p.store.CreateParseTask(ctx, parseTask); err != nil {
			if store.ErrorCodeOf(err) == store.ErrCodeIdempotencyKeyReused {
				result.DuplicateCount++
				continue
			}
			return result, mapStoreError(err)
		}
		docState.ActiveTaskID = parseTask.TaskID
		docState.ParseQueueState = statepkg.ParseQueueStateQueued
		docState.DocumentID = document.DocumentID
		docState.UpdatedAt = p.clock()
		if err := p.store.SaveDocumentState(ctx, docState); err != nil {
			return result, mapStoreError(err)
		}
		result.AcceptedCount++
		result.TaskIDs = append(result.TaskIDs, parseTask.TaskID)
	}
	slices.Sort(result.TaskIDs)
	return result, nil
}

func (p *DBTaskPlanner) restoreFailedTask(ctx context.Context, desired store.ParseTask, state store.DocumentState, document store.Document) (store.ParseTask, bool, error) {
	item, err := p.store.GetParseTaskByIdempotencyKey(ctx, desired.IdempotencyKey)
	if err != nil {
		if store.ErrorCodeOf(err) == store.ErrCodeTaskNotFound || store.ErrorCodeOf(err) == store.ErrCodeNotFound {
			return store.ParseTask{}, false, nil
		}
		return store.ParseTask{}, false, mapStoreError(err)
	}
	task := item.Task
	if task.Status != TaskStatusFailed {
		return store.ParseTask{}, false, nil
	}
	now := p.clock().UTC()
	task.Status = TaskStatusPending
	task.SourceVersion = desired.SourceVersion
	task.TargetVersionID = desired.TargetVersionID
	task.CoreParentDocumentID = desired.CoreParentDocumentID
	task.CoreTaskID = ""
	task.CoreDocumentID = ""
	task.LeaseOwner = ""
	task.LeaseUntil = nil
	task.RetryCount = 0
	task.NextRunAt = now
	task.LastError = store.JSON{}
	task.UpdatedAt = now
	if err := p.store.SaveParseTask(ctx, task); err != nil {
		return store.ParseTask{}, false, mapStoreError(err)
	}
	if err := p.store.ClearTaskDeadLetter(ctx, task.TaskID); err != nil {
		return store.ParseTask{}, false, mapStoreError(err)
	}
	state.ActiveTaskID = task.TaskID
	state.ParseQueueState = statepkg.ParseQueueStateQueued
	state.DocumentID = document.DocumentID
	state.LastError = store.JSON{}
	state.UpdatedAt = now
	if err := p.store.SaveDocumentState(ctx, state); err != nil {
		return store.ParseTask{}, false, mapStoreError(err)
	}
	return task, true, nil
}

func (p *DBTaskPlanner) ExpediteTasks(ctx context.Context, req ExpediteRequest) (ExpediteResult, error) {
	if _, err := p.store.GetSource(ctx, req.SourceID); err != nil {
		return ExpediteResult{}, mapStoreError(err)
	}
	items, _, err := p.store.ListParseTasks(ctx, store.ParseTaskListRequest{
		SourceID:   req.SourceID,
		BindingID:  req.BindingID,
		DocumentID: firstString(req.DocumentIDs),
	})
	if err != nil {
		return ExpediteResult{}, mapStoreError(err)
	}
	selected := selectExpediteTasks(items, req)
	result := ExpediteResult{SkippedItems: []string{}}
	now := p.clock().UTC()
	for _, item := range selected {
		task := item.Task
		if !expeditable(task) {
			result.SkippedCount++
			result.SkippedItems = append(result.SkippedItems, task.TaskID)
			continue
		}
		task.NextRunAt = now
		task.UpdatedAt = now
		if err := p.store.SaveParseTask(ctx, task); err != nil {
			return result, mapStoreError(err)
		}
		result.UpdatedCount++
		result.TaskIDs = append(result.TaskIDs, task.TaskID)
	}
	slices.Sort(result.TaskIDs)
	return result, nil
}

func (p *DBTaskPlanner) RetryTask(ctx context.Context, req RetryRequest) (ParseTaskDetailResponse, error) {
	item, err := p.store.GetParseTask(ctx, req.TaskID)
	if err != nil {
		return ParseTaskDetailResponse{}, mapStoreError(err)
	}
	if !retryable(item.Task, req.Force) {
		return ParseTaskDetailResponse{}, NewError(ErrCodeTaskNotRetryable, "parse task is not retryable")
	}
	binding, err := p.store.GetBinding(ctx, item.Task.SourceID, item.Task.BindingID)
	if err != nil {
		return ParseTaskDetailResponse{}, mapStoreError(err)
	}
	if binding.BindingGeneration != item.Task.BindingGeneration {
		return ParseTaskDetailResponse{}, NewError(ErrCodeTaskSuperseded, "parse task generation is stale")
	}
	if binding.Status != "ACTIVE" {
		return ParseTaskDetailResponse{}, NewError(ErrCodeInvalidRequest, "binding is not active")
	}
	state, err := p.store.GetDocumentState(ctx, item.Task.SourceID, item.Task.BindingID, item.Task.ObjectKey)
	if err != nil {
		return ParseTaskDetailResponse{}, mapStoreError(err)
	}
	if state.BindingGeneration != binding.BindingGeneration {
		return ParseTaskDetailResponse{}, NewError(ErrCodeTaskSuperseded, "document state generation is stale")
	}
	action := actionForState(state)
	if action == "" {
		return ParseTaskDetailResponse{}, NewError(ErrCodeTaskSuperseded, "document state has no pending action")
	}
	object, err := p.store.GetObject(ctx, item.Task.SourceID, item.Task.BindingID, item.Task.ObjectKey)
	if err != nil {
		return ParseTaskDetailResponse{}, mapStoreError(err)
	}
	if !object.IsDocument || !state.Selectable {
		return ParseTaskDetailResponse{}, NewError(ErrCodeInvalidRequest, "object is not selectable")
	}
	source, err := p.store.GetSource(ctx, item.Task.SourceID)
	if err != nil {
		return ParseTaskDetailResponse{}, mapStoreError(err)
	}
	document, err := p.store.UpsertDocument(ctx, p.documentForState(source, object, state, action))
	if err != nil {
		return ParseTaskDetailResponse{}, mapStoreError(err)
	}
	next := p.taskForState(source, binding, object, state, document, action)
	if next.IdempotencyKey == item.Task.IdempotencyKey {
		task := item.Task
		task.Status = TaskStatusPending
		task.SourceVersion = next.SourceVersion
		task.TargetVersionID = next.TargetVersionID
		task.CoreParentDocumentID = next.CoreParentDocumentID
		task.LeaseOwner = ""
		task.LeaseUntil = nil
		task.NextRunAt = p.clock().UTC()
		task.UpdatedAt = task.NextRunAt
		if task.LastError == nil {
			task.LastError = store.JSON{}
		}
		if err := p.store.SaveParseTask(ctx, task); err != nil {
			return ParseTaskDetailResponse{}, mapStoreError(err)
		}
		state.ActiveTaskID = task.TaskID
		state.ParseQueueState = statepkg.ParseQueueStateQueued
		state.DocumentID = document.DocumentID
		state.UpdatedAt = task.NextRunAt
		if err := p.store.SaveDocumentState(ctx, state); err != nil {
			return ParseTaskDetailResponse{}, mapStoreError(err)
		}
		item.Task = task
		item.Document = &document
		item.State = &state
		item.Object = &object
		return parseTaskDetailResponse(item), nil
	}
	if existing, ok, err := p.store.FindActiveTask(ctx, next.SourceID, next.BindingID, next.ObjectKey, next.TargetVersionID, next.TaskAction); err != nil {
		return ParseTaskDetailResponse{}, mapStoreError(err)
	} else if ok {
		state.ActiveTaskID = existing.TaskID
		state.ParseQueueState = statepkg.ParseQueueStateQueued
		state.DocumentID = existing.DocumentID
		state.UpdatedAt = p.clock().UTC()
		if err := p.store.SaveDocumentState(ctx, state); err != nil {
			return ParseTaskDetailResponse{}, mapStoreError(err)
		}
		existingItem, err := p.store.GetParseTask(ctx, existing.TaskID)
		if err != nil {
			return ParseTaskDetailResponse{}, mapStoreError(err)
		}
		return parseTaskDetailResponse(existingItem), nil
	}
	if err := p.store.CreateParseTask(ctx, next); err != nil {
		if store.ErrorCodeOf(err) == store.ErrCodeIdempotencyKeyReused {
			existing, ok, findErr := p.store.FindActiveTask(ctx, next.SourceID, next.BindingID, next.ObjectKey, next.TargetVersionID, next.TaskAction)
			if findErr != nil {
				return ParseTaskDetailResponse{}, mapStoreError(findErr)
			}
			if ok {
				existingItem, getErr := p.store.GetParseTask(ctx, existing.TaskID)
				if getErr != nil {
					return ParseTaskDetailResponse{}, mapStoreError(getErr)
				}
				return parseTaskDetailResponse(existingItem), nil
			}
		}
		return ParseTaskDetailResponse{}, mapStoreError(err)
	}
	state.ActiveTaskID = next.TaskID
	state.ParseQueueState = statepkg.ParseQueueStateQueued
	state.DocumentID = document.DocumentID
	state.UpdatedAt = p.clock().UTC()
	if err := p.store.SaveDocumentState(ctx, state); err != nil {
		return ParseTaskDetailResponse{}, mapStoreError(err)
	}
	item.Task = next
	item.Document = &document
	item.State = &state
	item.Object = &object
	return parseTaskDetailResponse(item), nil
}

func (p *DBTaskPlanner) generateBindings(ctx context.Context, sourceID string) ([]store.Binding, error) {
	bindings, err := p.store.ListBindings(ctx, sourceID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	active := make([]store.Binding, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Status == "ACTIVE" {
			active = append(active, binding)
		}
	}
	return active, nil
}

func (p *DBTaskPlanner) objectKeysForDocuments(ctx context.Context, sourceID, bindingID string, documentIDs []string) ([]string, error) {
	items, _, err := p.store.ListParseTasks(ctx, store.ParseTaskListRequest{SourceID: sourceID, BindingID: bindingID})
	if err != nil {
		return nil, mapStoreError(err)
	}
	wanted := stringSet(documentIDs)
	keys := make([]string, 0, len(documentIDs))
	for _, item := range items {
		if item.Document != nil {
			if _, ok := wanted[item.Document.DocumentID]; ok {
				keys = append(keys, item.Document.ObjectKey)
			}
		}
	}
	return keys, nil
}

func selectExpediteTasks(items []store.ParseTaskWithRefs, req ExpediteRequest) []store.ParseTaskWithRefs {
	out := make([]store.ParseTaskWithRefs, 0, len(items))
	taskIDs := stringSet(req.TaskIDs)
	documentIDs := stringSet(req.DocumentIDs)
	objectKeys := stringSet(req.ObjectKeys)
	for _, item := range items {
		task := item.Task
		if len(taskIDs) > 0 {
			if _, ok := taskIDs[task.TaskID]; !ok {
				continue
			}
		}
		if len(documentIDs) > 0 {
			if _, ok := documentIDs[task.DocumentID]; !ok {
				continue
			}
		}
		if len(objectKeys) > 0 {
			if _, ok := objectKeys[task.ObjectKey]; !ok {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

func expeditable(task store.ParseTask) bool {
	return task.Status == TaskStatusPending || (task.Status == TaskStatusFailed && task.RetryCount < 3)
}

func retryable(task store.ParseTask, force bool) bool {
	if force {
		return task.Status == TaskStatusFailed
	}
	return task.Status == TaskStatusFailed && task.RetryCount < 3
}

func firstString(values []string) string {
	if len(values) == 1 {
		return values[0]
	}
	return ""
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func (p *DBTaskPlanner) documentForState(source store.Source, object store.SourceObject, docState store.DocumentState, action string) store.Document {
	now := p.clock()
	documentID := docState.DocumentID
	if documentID == "" {
		documentID = p.newID("document")
	}
	return store.Document{
		DocumentID:       documentID,
		TenantID:         source.TenantID,
		SourceID:         object.SourceID,
		BindingID:        object.BindingID,
		ObjectKey:        object.ObjectKey,
		CurrentVersionID: docState.BaselineVersion,
		DesiredVersionID: targetVersion(docState, action),
		SourceVersion:    docState.SourceVersion,
		DisplayName:      object.DisplayName,
		MimeType:         object.MimeType,
		FileExtension:    object.FileExtension,
		ParseStatus:      DocumentParseStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func (p *DBTaskPlanner) taskForState(source store.Source, binding store.Binding, object store.SourceObject, docState store.DocumentState, document store.Document, action string) store.ParseTask {
	now := p.clock()
	parseTask := store.ParseTask{
		TaskID:               p.newID("task"),
		TenantID:             source.TenantID,
		SourceID:             object.SourceID,
		BindingID:            object.BindingID,
		BindingGeneration:    docState.BindingGeneration,
		ObjectKey:            object.ObjectKey,
		DocumentID:           document.DocumentID,
		TaskAction:           action,
		TargetVersionID:      targetVersion(docState, action),
		SourceVersion:        docState.SourceVersion,
		CoreParentDocumentID: binding.CoreParentDocumentID,
		Status:               TaskStatusPending,
		NextRunAt:            now,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	parseTask.IdempotencyKey = IdempotencyKey(parseTask)
	return parseTask
}

func actionForState(docState store.DocumentState) string {
	if docState.PendingAction != "" {
		switch docState.PendingAction {
		case TaskActionCreate, TaskActionReparse, TaskActionDelete:
			return docState.PendingAction
		default:
			return ""
		}
	}
	switch docState.SourceState {
	case statepkg.SourceStateNew:
		return TaskActionCreate
	case statepkg.SourceStateModified:
		return TaskActionReparse
	case statepkg.SourceStateDeleted:
		return TaskActionDelete
	case statepkg.SourceStateOutOfScope:
		return TaskActionDelete
	default:
		return ""
	}
}

func canGenerateTaskForObject(policy filefilter.Policy, object store.SourceObject, docState store.DocumentState, action string) bool {
	if !object.IsDocument || !docState.Selectable {
		return false
	}
	if filefilter.AllowsSourceObject(policy, object) {
		return true
	}
	return action == TaskActionDelete && cleanupDeleteState(docState)
}

func cleanupDeleteState(docState store.DocumentState) bool {
	if docState.PendingAction != statepkg.PendingActionDelete {
		return false
	}
	return docState.SourceState == statepkg.SourceStateDeleted || docState.SourceState == statepkg.SourceStateOutOfScope
}

func parseBatchLimitError(limit, actual int, countBy string) *ServiceError {
	return NewErrorWithDetails(
		ErrCodeParseBatchObjectLimitExceeded,
		"parse batch object limit exceeded",
		map[string]any{
			"limit":    limit,
			"actual":   actual,
			"count_by": countBy,
		},
	)
}

func targetVersion(docState store.DocumentState, action string) string {
	if action == TaskActionDelete {
		return docState.BaselineVersion
	}
	return docState.SourceVersion
}

type coverageSelector struct {
	complete bool
	root     bool
	keys     map[string]struct{}
	subtrees map[string]struct{}
}

func newCoverageSelector(coverage store.JSON) *coverageSelector {
	return &coverageSelector{
		complete: coverageBool(coverage, "complete"),
		root:     coverageBool(coverage, "covered_target_root") || coverageString(coverage, "scope_type") == string(connector.ScopeTypeFull),
		keys:     stringSet(stringSliceFromJSON(coverage["covered_object_keys"])),
		subtrees: stringSet(stringSliceFromJSON(coverage["covered_subtrees"])),
	}
}

func (s *coverageSelector) queryObjectKeys() []string {
	if s == nil || s.root || len(s.subtrees) > 0 {
		return nil
	}
	out := make([]string, 0, len(s.keys))
	for key := range s.keys {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func (p *DBTaskPlanner) objectCovered(ctx context.Context, sourceID, bindingID string, object store.SourceObject, coverage *coverageSelector) bool {
	if coverage == nil || coverage.root {
		return true
	}
	for {
		if _, ok := coverage.keys[object.ObjectKey]; ok {
			return true
		}
		if _, ok := coverage.subtrees[object.ObjectKey]; ok {
			return true
		}
		if object.ParentKey == "" || object.ParentKey == object.ObjectKey {
			return false
		}
		parent, err := p.store.GetObject(ctx, sourceID, bindingID, object.ParentKey)
		if err != nil {
			return false
		}
		object = parent
	}
}

func coverageBool(coverage store.JSON, key string) bool {
	value, _ := coverage[key].(bool)
	return value
}

func coverageString(coverage store.JSON, key string) string {
	value, _ := coverage[key].(string)
	return strings.TrimSpace(value)
}

func stringSliceFromJSON(value any) []string {
	switch typed := value.(type) {
	case []string:
		return slices.Clone(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
