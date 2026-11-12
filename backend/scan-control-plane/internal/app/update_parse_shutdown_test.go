package app

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
	source "github.com/lazymind/scan_control_plane/internal/store/source"
)

// Only the three operations on the exercised failure path are implemented.
// Any unexpected store/temp operation fails instead of silently succeeding.
type updateClaimFailureStore struct {
	worker.Store
	cleanup      *updateLoopBarrier
	claimStarted chan struct{}
	claimError   error
	cancelClaim  bool
	claims       int
}
type updateUnusedTemp struct{ worker.TempObjectStore }

func (s *updateClaimFailureStore) ClaimDueTask(ctx context.Context, _ string, _ time.Time, _ time.Duration) (source.ParseTask, bool, error) {
	s.claims++
	if s.claims == 1 {
		return source.ParseTask{TaskID: "fixture-task", SourceID: "fixture-source"}, true, nil
	}
	if s.claims != 2 {
		panic("unexpected third claim")
	}
	<-s.cleanup.entered
	close(s.claimStarted)
	if s.cancelClaim {
		<-ctx.Done()
		return source.ParseTask{}, false, ctx.Err()
	}
	return source.ParseTask{}, false, s.claimError
}
func (s *updateClaimFailureStore) GetSource(context.Context, string) (source.Source, error) {
	return source.Source{}, errors.New("fixture source is unavailable")
}
func (s *updateClaimFailureStore) RetryOrDeadLetterTask(context.Context, string, string, time.Time, int64, time.Duration) (source.ParseTask, bool, error) {
	close(s.cleanup.entered)
	<-s.cleanup.release
	close(s.cleanup.finished)
	return source.ParseTask{}, false, nil
}

func TestUpdateParseClaimFailureRetainsAdmittedWork(t *testing.T) {
	for _, boundary := range []string{"runner", "app"} {
		for _, cause := range []string{"database", "cancel", "empty"} {
			t.Run(boundary+"/"+cause, func(t *testing.T) {
				cleanup := newUpdateLoopBarrier(t)
				store := &updateClaimFailureStore{cleanup: cleanup, claimStarted: make(chan struct{}), claimError: errors.New("fixture claim failure"), cancelClaim: cause == "cancel"}
				if cause == "empty" {
					store.claimError = nil
				}
				parser := worker.NewDefaultParseWorker(store, nil, nil, nil, &updateUnusedTemp{})
				runner := worker.NewRunner(parser, worker.WithGlobalConcurrency(2))
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				returned := make(chan error, 1)
				if boundary == "app" {
					a := &App{server: &http.Server{Addr: "127.0.0.1:0"}, runtime: &Runtime{parseRunner: runner, workerPollInterval: time.Hour}}
					go func() { returned <- a.Run(ctx) }()
				} else {
					go func() { returned <- runner.RunPending(ctx, "fixture-worker") }()
				}
				awaitUpdateSignal(t, store.claimStarted)
				if cause == "cancel" || boundary == "app" {
					cancel()
				}
				select {
				case err := <-returned:
					t.Fatalf("%s returned before admitted parse finalizer: %v", boundary, err)
				case <-time.After(100 * time.Millisecond):
				}
				cleanup.unblock()
				select {
				case err := <-returned:
					expected := store.claimError
					if cause == "cancel" || boundary == "app" {
						expected = context.Canceled
					}
					if !errors.Is(err, expected) {
						t.Fatalf("lost exit cause: got %v, want %v", err, expected)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("did not return after parse finalizer")
				}
				awaitUpdateSignal(t, cleanup.finished)
			})
		}
	}
}
