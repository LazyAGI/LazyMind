package worker

import (
	"context"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/coreclient"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestBindingCleanupRunnerDeletesRootBeforeFinalizing(t *testing.T) {
	t.Parallel()

	repo := &bindingCleanupStoreStub{
		binding: store.Binding{SourceID: "source-1", BindingID: "binding-1", CoreParentDocumentID: "folder-1", Status: "DELETING"},
		source:  store.Source{SourceID: "source-1", DatasetID: "dataset-1", CreatedBy: "user-1"},
	}
	core := &bindingRootDeleterStub{}
	runner := NewBindingCleanupRunner(repo, core, 10)

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("run binding cleanup: %v", err)
	}
	if len(core.requests) != 1 || core.requests[0].DatasetID != "dataset-1" || core.requests[0].DocumentID != "folder-1" || core.requests[0].UserID != "user-1" {
		t.Fatalf("binding root deletion was not scoped correctly: %+v", core.requests)
	}
	if !repo.finalized {
		t.Fatalf("binding cleanup was not finalized")
	}
}

type bindingCleanupStoreStub struct {
	binding   store.Binding
	source    store.Source
	finalized bool
}

func (s *bindingCleanupStoreStub) ListReadyBindingCleanups(context.Context, int) ([]store.Binding, error) {
	if s.finalized {
		return nil, nil
	}
	return []store.Binding{s.binding}, nil
}

func (s *bindingCleanupStoreStub) GetSource(context.Context, string) (store.Source, error) {
	return s.source, nil
}

func (s *bindingCleanupStoreStub) FinalizeBindingCleanup(context.Context, string, string, time.Time) error {
	s.finalized = true
	return nil
}

type bindingRootDeleterStub struct {
	requests []coreclient.DeleteDocumentRequest
}

func (s *bindingRootDeleterStub) DeleteDocument(_ context.Context, req coreclient.DeleteDocumentRequest) error {
	s.requests = append(s.requests, req)
	return nil
}
