package scheduler

import "lazymind/core/common/maintenancegate"

func PauseForUpdate(epoch uint64) (*maintenancegate.Ticket, error) {
	return scheduledWork.Pause(epoch)
}
func ResumeFromUpdate(epoch uint64) error { return scheduledWork.Resume(epoch) }

func SealForBootstrap(key string) (*maintenancegate.Ticket, error) { return scheduledWork.Seal(key) }
func UnsealBootstrap(key string) error                             { return scheduledWork.Unseal(key) }
