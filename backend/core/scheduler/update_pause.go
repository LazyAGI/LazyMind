package scheduler

import "lazymind/core/common/maintenancegate"

func PauseForUpdate(epoch uint64) (*maintenancegate.Ticket, error) {
	return scheduledWork.Pause(epoch)
}
func ResumeFromUpdate(epoch uint64) error { return scheduledWork.Resume(epoch) }
