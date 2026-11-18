package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/schedule"
)

type updateLoopBarrier struct {
	entered, cancelled, release, finished chan struct{}
	once                                  sync.Once
	calls                                 atomic.Int32
}

func newUpdateLoopBarrier(t *testing.T) *updateLoopBarrier {
	b := &updateLoopBarrier{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	t.Cleanup(b.unblock)
	return b
}
func (b *updateLoopBarrier) unblock() { b.once.Do(func() { close(b.release) }) }
func (b *updateLoopBarrier) run(ctx context.Context) {
	if b.calls.Add(1) != 1 {
		return
	}
	close(b.entered)
	<-ctx.Done()
	close(b.cancelled)
	<-b.release
	close(b.finished)
}
func (b *updateLoopBarrier) EnqueueDueSyncRuns(ctx context.Context, _ int) ([]schedule.SyncRunIntent, error) {
	b.run(ctx)
	return nil, ctx.Err()
}
func (b *updateLoopBarrier) RecoverLocalWatchers(ctx context.Context, _ time.Time) (int, error) {
	b.run(ctx)
	return 0, ctx.Err()
}
func (b *updateLoopBarrier) MaintainAgentCommands(ctx context.Context, _ time.Time, _ int) (int64, error) {
	b.run(ctx)
	return 0, ctx.Err()
}

func awaitUpdateSignal(t *testing.T, done <-chan struct{}) {
	t.Helper()
	if done == nil {
		t.Fatal("missing completion signal")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not complete")
	}
}
func requireUpdatePending(t *testing.T, done <-chan struct{}) {
	t.Helper()
	if done == nil {
		t.Fatal("missing completion signal")
	}
	select {
	case <-done:
		t.Fatal("completion preceded admitted work cleanup")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestUpdateRuntimeCompletionWaitsForAllLoops(t *testing.T) {
	for last, name := range []string{"main", "recovery", "maintenance"} {
		t.Run(name, func(t *testing.T) {
			loops := []*updateLoopBarrier{newUpdateLoopBarrier(t), newUpdateLoopBarrier(t), newUpdateLoopBarrier(t)}
			r := &Runtime{scheduler: loops[0], watcherRecovery: loops[1], commandMaintenance: loops[2], workerPollInterval: time.Nanosecond}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := r.Start(ctx)
			for _, loop := range loops {
				awaitUpdateSignal(t, loop.entered)
			}
			cancel()
			for _, loop := range loops {
				awaitUpdateSignal(t, loop.cancelled)
			}
			for i, loop := range loops {
				if i != last {
					loop.unblock()
					awaitUpdateSignal(t, loop.finished)
				}
			}
			requireUpdatePending(t, done)
			loops[last].unblock()
			awaitUpdateSignal(t, done)
			for _, loop := range loops {
				awaitUpdateSignal(t, loop.finished)
				if got := loop.calls.Load(); got != 1 {
					t.Fatalf("cancelled runtime started another iteration: %d", got)
				}
			}
		})
	}
}

func TestUpdateRuntimeAlreadyCancelledDoesNotStartWork(t *testing.T) {
	loop := newUpdateLoopBarrier(t)
	loop.unblock()
	r := &Runtime{scheduler: loop, watcherRecovery: loop, commandMaintenance: loop, workerPollInterval: time.Nanosecond}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	awaitUpdateSignal(t, r.Start(ctx))
	if got := loop.calls.Load(); got != 0 {
		t.Fatalf("pre-cancelled runtime started work: %d", got)
	}
	var absent *Runtime
	awaitUpdateSignal(t, absent.Start(ctx))
}

func TestUpdateAppRunWaitsForRuntimeCleanup(t *testing.T) {
	for _, cause := range []string{"cancel", "server-error"} {
		t.Run(cause, func(t *testing.T) {
			loop := newUpdateLoopBarrier(t)
			a := &App{server: &http.Server{Addr: "127.0.0.1:0"}, runtime: &Runtime{scheduler: loop, workerPollInterval: time.Hour}}
			if cause == "server-error" {
				// Close the actual listener after work has started, before Serve's first
				// Accept. This produces net.ErrClosed without shutting down the server.
				a.server.BaseContext = func(listener net.Listener) context.Context {
					<-loop.entered
					_ = listener.Close()
					return context.Background()
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			returned := make(chan error, 1)
			go func() { returned <- a.Run(ctx) }()
			awaitUpdateSignal(t, loop.entered)
			if cause == "cancel" {
				cancel()
			}
			awaitUpdateSignal(t, loop.cancelled)
			select {
			case err := <-returned:
				t.Fatalf("App.Run returned while background cleanup is blocked: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			loop.unblock()
			select {
			case err := <-returned:
				expected := error(context.Canceled)
				if cause == "server-error" {
					expected = net.ErrClosed
				}
				if !errors.Is(err, expected) {
					t.Fatalf("lost shutdown cause: got %v, want %v", err, expected)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("App.Run did not return after cleanup")
			}
			awaitUpdateSignal(t, loop.finished)
		})
	}
}
