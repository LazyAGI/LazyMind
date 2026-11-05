package asyncjob

import "lazymind/core/common/maintenancegate"

func (r *Runner) PauseForUpdate(epoch uint64) (*maintenancegate.Ticket, error) {
	return r.admission.Pause(epoch)
}
func (r *Runner) ResumeFromUpdate(epoch uint64) error { return r.admission.Resume(epoch) }

func (r *Runner) BootstrapTicket() *maintenancegate.Ticket { return r.bootstrapTicket }
func (r *Runner) UnsealBootstrap(key string) error         { return r.admission.Unseal(key) }
