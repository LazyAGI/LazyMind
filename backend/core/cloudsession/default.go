package cloudsession

import "sync"

var defaultServiceState = struct {
	sync.RWMutex
	service *Service
}{service: NewService(ServiceDeps{})}

func DefaultService() *Service {
	defaultServiceState.RLock()
	defer defaultServiceState.RUnlock()
	return defaultServiceState.service
}

func SetDefaultService(service *Service) {
	if service == nil {
		service = NewService(ServiceDeps{})
	}
	defaultServiceState.Lock()
	defaultServiceState.service = service
	defaultServiceState.Unlock()
}
