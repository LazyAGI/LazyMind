package scheduler

import "lazymind/core/common/maintenancegate"

var scheduledWork = maintenancegate.New()

// BeginDrain closes scheduler admission synchronously and returns a channel
// closed after all previously admitted work and final writes have completed.
// Draining is terminal for this scheduler process; it does not cancel work.
func BeginDrain() <-chan struct{} { return scheduledWork.Drain() }

func admitScheduledWork() bool { return scheduledWork.Enter() }
func finishScheduledWork()     { scheduledWork.Leave() }

// Only admitted dispatchers call this. Retain their work before launching a
// child, so preparation cannot finish before its continuation is registered.
func continueScheduledWork(action func()) {
	scheduledWork.Continue()
	go func() {
		defer finishScheduledWork()
		action()
	}()
}
