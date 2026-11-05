package doc

import "lazymind/core/common/maintenancegate"

var deferredIndexingWork = maintenancegate.New()

func PauseDeferredIndexingForUpdate(epoch uint64) (*maintenancegate.Ticket, error) {
	return deferredIndexingWork.Pause(epoch)
}

func ResumeDeferredIndexingFromUpdate(epoch uint64) error {
	return deferredIndexingWork.Resume(epoch)
}

func SealDeferredIndexingForBootstrap(key string) (*maintenancegate.Ticket, error) {
	return deferredIndexingWork.Seal(key)
}
func UnsealDeferredIndexingBootstrap(key string) error { return deferredIndexingWork.Unseal(key) }
