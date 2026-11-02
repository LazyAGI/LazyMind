package asyncjob

import "lazymind/core/common/maintenancegate"

func (r *Runner) PauseForUpdate(epoch uint64) (*maintenancegate.Ticket, error) {
	return r.admission.Pause(epoch)
}
func (r *Runner) ResumeFromUpdate(epoch uint64) error { return r.admission.Resume(epoch) }
