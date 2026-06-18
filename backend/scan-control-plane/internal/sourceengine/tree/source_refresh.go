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
		return nil
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
