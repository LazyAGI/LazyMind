package asyncjob

// BeginDrain stops admitting new iterations without canceling admitted jobs.
// It returns Done(), including handlers, heartbeats and final writes.
func (r *Runner) BeginDrain() <-chan struct{} {
	r.admission.Drain()
	return r.done
}

func (r *Runner) draining() bool {
	select {
	case <-r.admission.Stopping():
		return true
	default:
		return false
	}
}
