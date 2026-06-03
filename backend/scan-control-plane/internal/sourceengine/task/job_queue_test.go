package task

import (
	"context"
	"testing"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func TestDBJobQueueDelegatesToStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stub := &syncRunEnqueueStub{}
	queue := NewDBJobQueue(stub)

	first, created, err := queue.EnqueueSyncRun(ctx, store.SyncRun{RunID: "run-1"})
	if err != nil || !created {
		t.Fatalf("enqueue first sync run created=%v err=%v", created, err)
	}
	if first.RunID != "run-1" || len(stub.runs) != 1 {
		t.Fatalf("queue did not delegate to store: run=%+v stub=%+v", first, stub.runs)
	}

	second, created, err := queue.EnqueueSyncRun(ctx, store.SyncRun{RunID: "run-1"})
	if err != nil {
		t.Fatalf("enqueue duplicate sync run: %v", err)
	}
	if created || second.RunID != first.RunID {
		t.Fatalf("expected store duplicate result, first=%+v second=%+v created=%v", first, second, created)
	}
}

type syncRunEnqueueStub struct {
	runs map[string]store.SyncRun
}

func (s *syncRunEnqueueStub) EnqueueSyncRun(_ context.Context, run store.SyncRun) (store.SyncRun, bool, error) {
	if s.runs == nil {
		s.runs = map[string]store.SyncRun{}
	}
	if existing, ok := s.runs[run.RunID]; ok {
		return existing, false, nil
	}
	s.runs[run.RunID] = run
	return run, true, nil
}
