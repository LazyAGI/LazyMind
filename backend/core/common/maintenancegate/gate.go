// Package maintenancegate coordinates in-process work admission. It does not
// authenticate a remote controller or establish durable business success.
package maintenancegate

import (
	"context"
	"sync"
)

type Failure string

func (f Failure) Error() string { return string(f) }

const (
	ErrInvalidEpoch Failure = "maintenance_epoch_invalid"
	ErrConflict     Failure = "maintenance_epoch_conflict"
	ErrStale        Failure = "maintenance_epoch_stale"
	ErrStopped      Failure = "maintenance_stopped"
	ErrActive       Failure = "maintenance_work_active"
)

type Gate struct {
	mu                sync.Mutex
	active            int
	stopped           bool
	stopping, drained chan struct{}
	highest, resumed  uint64
	pause             *Ticket
	bootstrap         *Ticket
	bootstrapKey      string
}

type Ticket struct {
	gate     *Gate
	epoch    uint64
	changed  chan struct{}
	signaled bool
}

func New() *Gate {
	return &Gate{stopping: make(chan struct{}), drained: make(chan struct{})}
}

func (g *Gate) Enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped || g.pause != nil || g.bootstrap != nil {
		return false
	}
	g.active++
	return true
}

// Continue retains work before an admitted dispatcher launches a continuation.
// The dispatcher must remain admitted until this call returns.
func (g *Gate) Continue() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active == 0 && (g.stopped || g.pause != nil || g.bootstrap != nil) {
		panic("maintenance continuation without admitted work")
	}
	g.active++
}

func (g *Gate) Leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active <= 0 {
		panic("unbalanced maintenance admission")
	}
	g.active--
	if g.active == 0 {
		if g.bootstrap != nil {
			g.bootstrap.signal()
		}
		if g.pause != nil {
			g.pause.signal()
		}
		if g.stopped {
			close(g.drained)
		}
	}
}

func (g *Gate) Drain() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.stopped {
		g.stopped = true
		close(g.stopping)
		if g.pause != nil {
			g.pause.signal()
			g.pause = nil
		}
		if g.active == 0 {
			close(g.drained)
		}
	}
	return g.drained
}

func (g *Gate) Stopping() <-chan struct{} { return g.stopping }

func (g *Gate) Pause(epoch uint64) (*Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return nil, ErrStopped
	}
	if epoch == 0 {
		return nil, ErrInvalidEpoch
	}
	if epoch < g.highest {
		return nil, ErrStale
	}
	if g.pause != nil {
		if epoch == g.pause.epoch {
			return g.pause, nil
		}
		return nil, ErrConflict
	}
	if epoch == g.highest {
		return nil, ErrStale
	}
	g.highest = epoch
	g.pause = &Ticket{gate: g, epoch: epoch, changed: make(chan struct{})}
	if g.active == 0 {
		g.pause.signal()
	}
	return g.pause, nil
}

func (g *Gate) Resume(epoch uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return ErrStopped
	}
	if epoch == 0 {
		return ErrInvalidEpoch
	}
	if g.pause == nil {
		if epoch == g.resumed {
			return nil
		}
		return ErrStale
	}
	if epoch != g.pause.epoch {
		return ErrStale
	}
	g.pause.signal()
	g.pause = nil
	g.resumed = epoch
	return nil
}

// signal is called only with the gate mutex held.
func (t *Ticket) signal() {
	if !t.signaled {
		close(t.changed)
		t.signaled = true
	}
}

// Wait reports tracked inactivity only while this epoch is still paused.
// The owner must separately verify durable outcomes before authorizing stop.
func (t *Ticket) Wait(ctx context.Context) error {
	if err := t.Validate(); err != ErrActive {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.changed:
		return t.Validate()
	}
}

func (t *Ticket) Validate() error {
	g := t.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return ErrStopped
	}
	if g.pause != t && g.bootstrap != t {
		return ErrStale
	}
	if g.active != 0 {
		return ErrActive
	}
	return nil
}

// Seal is independent of ordinary preparation epochs and has no lease expiry.
func (g *Gate) Seal(key string) (*Ticket, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return nil, ErrStopped
	}
	if key == "" {
		return nil, ErrInvalidEpoch
	}
	if g.bootstrap != nil {
		if g.bootstrapKey == key {
			return g.bootstrap, nil
		}
		return nil, ErrConflict
	}
	g.bootstrapKey = key
	g.bootstrap = &Ticket{gate: g, changed: make(chan struct{})}
	if g.active == 0 {
		g.bootstrap.signal()
	}
	return g.bootstrap, nil
}
func (g *Gate) Unseal(key string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if key == "" || key != g.bootstrapKey {
		return ErrStale
	}
	if g.bootstrap == nil {
		return nil
	}
	if g.active != 0 {
		return ErrActive
	}
	g.bootstrap.signal()
	g.bootstrap = nil
	return nil
}
