package worker

import (
	"context"
	"errors"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

type BindingCleanupStore interface {
	ListReadyBindingCleanups(ctx context.Context, limit int) ([]store.Binding, error)
	GetSource(ctx context.Context, sourceID string) (store.Source, error)
	FinalizeBindingCleanup(ctx context.Context, sourceID, bindingID string, now time.Time) error
}

type bindingRootDeleter interface {
	DeleteDocument(ctx context.Context, req coreclient.DeleteDocumentRequest) error
}

type BindingCleanupRunner struct {
	store BindingCleanupStore
	core  bindingRootDeleter
	clock func() time.Time
	limit int
}

func NewBindingCleanupRunner(store BindingCleanupStore, core bindingRootDeleter, limit int) *BindingCleanupRunner {
	if limit <= 0 {
		limit = DefaultGlobalConcurrency
	}
	return &BindingCleanupRunner{store: store, core: core, clock: time.Now, limit: limit}
}

func (r *BindingCleanupRunner) RunOnce(ctx context.Context) error {
	if r == nil || r.store == nil || r.core == nil {
		return nil
	}
	bindings, err := r.store.ListReadyBindingCleanups(ctx, r.limit)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		source, err := r.store.GetSource(ctx, binding.SourceID)
		if err != nil {
			return err
		}
		if binding.CoreParentDocumentID != "" {
			if err := r.core.DeleteDocument(ctx, coreclient.DeleteDocumentRequest{
				DatasetID:  source.DatasetID,
				DocumentID: binding.CoreParentDocumentID,
				UserID:     source.CreatedBy,
			}); err != nil && !isCoreDocumentNotFound(err) {
				return err
			}
		}
		if err := r.store.FinalizeBindingCleanup(ctx, binding.SourceID, binding.BindingID, r.clock().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func isCoreDocumentNotFound(err error) bool {
	var coreErr *coreclient.Error
	return errors.As(err, &coreErr) && coreErr.StatusCode == 404
}
