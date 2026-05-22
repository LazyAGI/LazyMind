package evolution

import "sync"

var autoEvoWorkers sync.Map

func AutoEvoWorkerKey(resourceType, resourceID string) string {
	return resourceType + ":" + resourceID
}

func TryAcquireAutoEvoWorker(workerKey string) bool {
	if workerKey == "" {
		return false
	}
	_, loaded := autoEvoWorkers.LoadOrStore(workerKey, struct{}{})
	return !loaded
}

func HasAutoEvoWorker(workerKey string) bool {
	if workerKey == "" {
		return false
	}
	_, loaded := autoEvoWorkers.Load(workerKey)
	return loaded
}

func ReleaseAutoEvoWorker(workerKey string) {
	if workerKey == "" {
		return
	}
	autoEvoWorkers.Delete(workerKey)
}
