package maintenancegate

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestConcurrentRootsCannotCrossPauseBoundary(t *testing.T) {
	for range 20 {
		g := New()
		if !g.Enter() {
			t.Fatal("root rejected")
		}
		start, release := make(chan struct{}), make(chan struct{})
		var workers sync.WaitGroup
		for range 12 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				if g.Enter() {
					<-release
					g.Leave()
				}
			}()
		}
		close(start)
		p := pause(t, g, 1)
		if g.Enter() {
			t.Error("root entered after Pause returned")
			g.Leave()
		}
		close(release)
		workers.Wait()
		if p.Validate() != ErrActive {
			t.Fatal("pause lost admitted root")
		}
		g.Leave()
		wait(t, p, nil)
	}
}

func pause(t *testing.T, g *Gate, epoch uint64) *Ticket {
	t.Helper()
	ticket, err := g.Pause(epoch)
	if err != nil || ticket == nil {
		t.Fatalf("pause: %v", err)
	}
	return ticket
}

func wait(t *testing.T, p *Ticket, want error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Wait(ctx); err != want {
		t.Fatalf("wait = %v, want %v", err, want)
	}
}

func TestPauseRetainsContinuationsAndInvalidatesResumedReceipt(t *testing.T) {
	g := New()
	if !g.Enter() {
		t.Fatal("initial root rejected")
	}
	p := pause(t, g, 1)
	if g.Enter() {
		t.Fatal("new root admitted while paused")
	}
	g.Continue()
	g.Leave()
	if p.Validate() != ErrActive {
		t.Fatal("continuation omitted from pause")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Wait(ctx); err != context.Canceled {
		t.Fatalf("busy wait ignored cancellation: %v", err)
	}
	g.Leave()
	wait(t, p, nil)
	if err := g.Resume(1); err != nil {
		t.Fatal(err)
	}
	wait(t, p, ErrStale)
	if p.Validate() != ErrStale {
		t.Fatal("old receipt remained valid after resume")
	}
	if !g.Enter() {
		t.Fatal("resume did not reopen roots")
	}
	g.Leave()
	if err := g.Resume(1); err != nil {
		t.Fatal("duplicate resume", err)
	}
	if _, err := g.Pause(1); err != ErrStale {
		t.Fatal("old pause replayed", err)
	}
	q := pause(t, g, 2)
	if err := g.Resume(1); err != ErrStale {
		t.Fatal("old resume affected new epoch", err)
	}
	if g.Enter() {
		t.Fatal("stale resume reopened roots")
	}
	wait(t, q, nil)
	wait(t, p, ErrStale)
}

func TestResumeWakesWaiterWithoutForgettingActiveWork(t *testing.T) {
	g := New()
	if !g.Enter() {
		t.Fatal("root rejected")
	}
	p := pause(t, g, 1)
	result := make(chan error, 1)
	go func() { result <- p.Wait(context.Background()) }()
	if err := g.Resume(1); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != ErrStale {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("resume stranded waiter")
	}
	if !g.Enter() {
		t.Fatal("resume did not accept new work")
	}
	g.Leave()
	drained := g.Drain()
	select {
	case <-drained:
		t.Fatal("resume forgot old active work")
	case <-time.After(20 * time.Millisecond):
	}
	g.Leave()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("old work did not drain")
	}
}

func TestTerminalDrainInvalidatesPauseAndCannotResume(t *testing.T) {
	g := New()
	if !g.Enter() {
		t.Fatal("root rejected")
	}
	p := pause(t, g, 1)
	drained := g.Drain()
	select {
	case <-g.Stopping():
	default:
		t.Fatal("stop admission not signaled")
	}
	wait(t, p, ErrStopped)
	if g.Enter() {
		t.Fatal("terminal gate accepted work")
	}
	if err := g.Resume(1); err != ErrStopped {
		t.Fatal("terminal gate resumed", err)
	}
	if _, err := g.Pause(2); err != ErrStopped {
		t.Fatal("terminal gate paused", err)
	}
	select {
	case <-drained:
		t.Fatal("terminal drain omitted active root")
	default:
	}
	g.Leave()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("terminal drain incomplete")
	}
	if g.Drain() != drained {
		t.Fatal("terminal completion not idempotent")
	}
}

func TestEpochConflictAndWaitCancellationDoNotReopenAdmission(t *testing.T) {
	g := New()
	if _, err := g.Pause(0); err != ErrInvalidEpoch {
		t.Fatal("empty epoch accepted", err)
	}
	if !g.Enter() {
		t.Fatal("invalid pause changed admission")
	}
	p := pause(t, g, 2)
	q := pause(t, g, 2)
	if _, err := g.Pause(3); err != ErrConflict {
		t.Fatal("overlapping epoch accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Wait(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	if g.Enter() {
		t.Fatal("wait cancellation reopened admission")
	}
	g.Leave()
	wait(t, p, nil)
	wait(t, q, nil)
	if err := g.Resume(2); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Pause(1); err != ErrStale {
		t.Fatal("older epoch reused", err)
	}
}
