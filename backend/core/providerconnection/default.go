package providerconnection

import "sync"

var defaultState = struct {
	sync.RWMutex
	service *Service
}{}

func SetDefaultService(service *Service) {
	defaultState.Lock()
	defaultState.service = service
	defaultState.Unlock()
}

func DefaultService() *Service {
	defaultState.RLock()
	defer defaultState.RUnlock()
	return defaultState.service
}
