package tree

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/crawl"
	stateengine "github.com/lazymind/scan_control_plane/internal/sourceengine/state"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const readRefreshTriggerType = "read_refresh"

type SourceReadRefreshRequest struct {
	SourceID  string
	BindingID string
}

type SourceReadRefresher interface {
	RefreshSourceRead(ctx context.Context, req SourceReadRefreshRequest) error
}

type SourceReadRefreshRepository interface {
	SourceTreeReadRepository
	crawl.ObjectWriter
	stateengine.Store
}

type parseTaskLister interface {
	ListParseTasks(ctx context.Context, req store.ParseTaskListRequest) ([]store.ParseTaskWithRefs, int, error)
}

type DBSourceReadRefresher struct {
	repo     SourceReadRefreshRepository
	registry connector.ConnectorRegistry
	clock    func() time.Time
}

type SourceReadRefreshOption func(*DBSourceReadRefresher)

func NewDBSourceReadRefresher(repo SourceReadRefreshRepository, registry connector.ConnectorRegistry, options ...SourceReadRefreshOption) *DBSourceReadRefresher {
	r := &DBSourceReadRefresher{repo: repo, registry: registry, clock: time.Now}
	for _, option := range options {
		option(r)
	}
	if r.clock == nil {
		r.clock = time.Now
	}
	return r
}

func WithSourceReadRefreshClock(clock func() time.Time) SourceReadRefreshOption {
	return func(r *DBSourceReadRefresher) {
		if clock != nil {
			r.clock = clock
		}
	}
}

func (r *DBSourceReadRefresher) RefreshSourceRead(ctx context.Context, req SourceReadRefreshRequest) error {
	if r == nil || r.repo == nil || r.registry == nil {
		return nil
	}
	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		return NewError(ErrCodeSourceNotFound, "source_id is required")
	}
	if _, err := r.repo.GetSource(ctx, sourceID); err != nil {
		return mapStoreError(err)
	}
	if bindingID := strings.TrimSpace(req.BindingID); bindingID != "" {
		binding, err := r.repo.GetBinding(ctx, sourceID, bindingID)
		if err != nil {
			return mapStoreError(err)
		}
		return r.refreshBinding(ctx, binding)
	}
	bindings, err := r.repo.ListBindings(ctx, sourceID)
	if err != nil {
		return mapStoreError(err)
	}
	for _, binding := range bindings {
		if err := r.refreshBinding(ctx, binding); err != nil {
			return err
		}
	}
	return nil
}

func (r *DBSourceReadRefresher) refreshBinding(ctx context.Context, binding store.Binding) error {
	if strings.TrimSpace(binding.ConnectorType) != "feishu" {
		return r.refreshCachedBindingState(ctx, binding)
	}
	if binding.Status != "" && binding.Status != crawl.BindingStatusActive {
		return nil
	}
	now := r.clock().UTC()
	reducer := stateengine.NewDBStateReducer(r.repo, stateengine.WithClock(func() time.Time { return now }))
	engine := crawl.NewDefaultCrawlEngine(
		r.repo,
		r.registry,
		r.repo,
		reducer,
		crawl.WithClock(func() time.Time { return now }),
	)
	result, err := engine.Run(ctx, crawl.BindingRunClaim{
		RunID:             fmt.Sprintf("read-refresh-%s-%d", binding.BindingID, now.UnixNano()),
		SourceID:          binding.SourceID,
		BindingID:         binding.BindingID,
		BindingGeneration: binding.BindingGeneration,
		TriggerType:       readRefreshTriggerType,
		ScopeType:         connector.ScopeTypeFull,
	})
	if err != nil {
		return err
	}
	if result.Status != crawl.RunStatusSucceeded {
		if readRefreshTargetMissing(result) {
			return targetMissingError(binding, result.ErrorMessage)
		}
		if result.ErrorMessage != "" {
			return NewError(ErrCodeInternal, result.ErrorMessage)
		}
		return NewError(ErrCodeInternal, "source read refresh failed")
	}
	return nil
}

func (r *DBSourceReadRefresher) refreshCachedBindingState(ctx context.Context, binding store.Binding) error {
	if binding.Status != "" && binding.Status != crawl.BindingStatusActive {
		return nil
	}
	states, err := r.repo.ListDocumentStates(ctx, binding.SourceID, binding.BindingID)
	if err != nil {
		return mapStoreError(err)
	}
	if len(states) == 0 {
		return nil
	}
	if err := r.repairCachedSyncedBaselines(ctx, binding, states); err != nil {
		return err
	}
	objects := make([]store.SourceObject, 0, len(states))
	for _, state := range states {
		object, err := r.repo.GetObject(ctx, binding.SourceID, binding.BindingID, state.ObjectKey)
		if err != nil {
			if store.ErrorCodeOf(err) == store.ErrCodeNotFound {
				continue
			}
			return mapStoreError(err)
		}
		if object.IsDocument {
			objects = append(objects, object)
		}
	}
	if len(objects) == 0 {
		return nil
	}
	now := r.clock().UTC()
	reducer := stateengine.NewDBStateReducer(r.repo, stateengine.WithClock(func() time.Time { return now }))
	_, err = reducer.ReduceSeenObjects(ctx, crawl.ReduceSeenInput{
		SourceID:          binding.SourceID,
		BindingID:         binding.BindingID,
		BindingGeneration: binding.BindingGeneration,
		RunID:             fmt.Sprintf("read-refresh-%s-%d", binding.BindingID, now.UnixNano()),
		Objects:           objects,
		DetectedAt:        now,
	})
	if err != nil {
		return mapStoreError(err)
	}
	return nil
}

func (r *DBSourceReadRefresher) repairCachedSyncedBaselines(ctx context.Context, binding store.Binding, states []store.DocumentState) error {
	lister, ok := r.repo.(parseTaskLister)
	if !ok {
		return nil
	}
	wanted := map[string]store.DocumentState{}
	for _, state := range states {
		if strings.TrimSpace(state.BaselineVersion) == "" && strings.TrimSpace(state.ObjectKey) != "" {
			wanted[state.ObjectKey] = state
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	latest := map[string]store.ParseTask{}
	const pageSize = 1000
	for page := 1; ; page++ {
		items, total, err := lister.ListParseTasks(ctx, store.ParseTaskListRequest{
			SourceID:  binding.SourceID,
			BindingID: binding.BindingID,
			Statuses:  []string{store.ParseTaskStatusSucceeded},
			TaskActions: []string{
				store.ParseTaskActionCreate,
				store.ParseTaskActionReparse,
				store.ParseTaskActionDelete,
			},
			Page:     page,
			PageSize: pageSize,
		})
		if err != nil {
			return mapStoreError(err)
		}
		for _, item := range items {
			task := item.Task
			if _, ok := wanted[task.ObjectKey]; !ok {
				continue
			}
			if previous, ok := latest[task.ObjectKey]; !ok || taskNewer(task, previous) {
				latest[task.ObjectKey] = task
			}
		}
		if len(items) == 0 || page*pageSize >= total {
			break
		}
	}
	now := r.clock().UTC()
	for objectKey, state := range wanted {
		task, ok := latest[objectKey]
		if ok {
			if task.TaskAction == store.ParseTaskActionDelete {
				continue
			}
			if repairStateFromSuccessfulTask(&state, task, now) {
				if err := r.repo.SaveDocumentState(ctx, state); err != nil {
					return mapStoreError(err)
				}
				continue
			}
		}
		document, err := r.repo.GetDocument(ctx, binding.SourceID, binding.BindingID, objectKey)
		if err != nil {
			if store.ErrorCodeOf(err) == store.ErrCodeNotFound {
				continue
			}
			return mapStoreError(err)
		}
		if !repairStateFromSyncedDocument(&state, document, now) {
			continue
		}
		if err := r.repo.SaveDocumentState(ctx, state); err != nil {
			return mapStoreError(err)
		}
	}
	return nil
}

func repairStateFromSuccessfulTask(state *store.DocumentState, task store.ParseTask, now time.Time) bool {
	baseline := strings.TrimSpace(task.TargetVersionID)
	if baseline == "" {
		baseline = strings.TrimSpace(task.SourceVersion)
	}
	if baseline == "" {
		return false
	}
	state.BaselineVersion = baseline
	if strings.TrimSpace(state.DocumentID) == "" {
		state.DocumentID = strings.TrimSpace(task.DocumentID)
	}
	state.UpdatedAt = now
	return true
}

func repairStateFromSyncedDocument(state *store.DocumentState, document store.Document, now time.Time) bool {
	if strings.TrimSpace(document.CoreDocumentID) == "" || !strings.EqualFold(strings.TrimSpace(document.ParseStatus), "SUCCEEDED") {
		return false
	}
	baseline := strings.TrimSpace(document.SourceVersion)
	if baseline == "" {
		return false
	}
	state.BaselineVersion = baseline
	if strings.TrimSpace(state.DocumentID) == "" {
		state.DocumentID = strings.TrimSpace(document.DocumentID)
	}
	state.UpdatedAt = now
	return true
}

func taskNewer(candidate, current store.ParseTask) bool {
	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	return candidate.TaskID > current.TaskID
}

func readRefreshTargetMissing(result crawl.RunResult) bool {
	if result.Status != crawl.RunStatusFailed || result.Coverage.ScopeType != connector.ScopeTypeFull {
		return false
	}
	if len(result.Coverage.CoveredObjectKeys) > 0 || len(result.Coverage.CoveredSubtrees) > 0 {
		return false
	}
	return targetMissingCodeMessage(result.ErrorCode, result.ErrorMessage)
}

func targetMissingCodeMessage(code, message string) bool {
	switch connector.ErrorCode(strings.TrimSpace(code)) {
	case connector.ErrorCodeNotFound, "TARGET_NOT_FOUND", "OBJECT_NOT_FOUND":
		return true
	case connector.ErrorCodeTransient:
		return strings.Contains(strings.ToLower(message), "not found")
	default:
		return false
	}
}

func targetMissingError(binding store.Binding, cause string) error {
	targetName := strings.TrimSpace(binding.CoreParentDocumentName)
	targetRef := strings.TrimSpace(binding.TargetRef)
	target := targetName
	if target == "" {
		target = targetRef
	}
	if target == "" {
		target = strings.TrimSpace(binding.BindingID)
	}
	message := fmt.Sprintf("当前数据源监控的目标 %q 在源端不存在", target)
	if targetRef != "" && targetRef != target {
		message = fmt.Sprintf("当前数据源监控的目标 %q (%s) 在源端不存在", target, targetRef)
	}
	return &QueryError{
		Code:    ErrCodeTargetNotFound,
		Message: message,
		Details: map[string]any{
			"source_id":      binding.SourceID,
			"binding_id":     binding.BindingID,
			"connector_type": binding.ConnectorType,
			"target_type":    binding.TargetType,
			"target_ref":     binding.TargetRef,
			"target_name":    binding.CoreParentDocumentName,
			"source_message": cause,
		},
	}
}
