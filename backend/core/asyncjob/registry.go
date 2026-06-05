package asyncjob

import (
	"fmt"
	"strings"
	"sync"
)

var globalRegistry = struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}{
	handlers: map[string]Handler{},
}

func Register(jobType string, handler Handler) {
	jobType = strings.TrimSpace(jobType)
	if jobType == "" {
		panic("asyncjob: job type is required")
	}
	if handler == nil {
		panic(fmt.Sprintf("asyncjob: nil handler for %s", jobType))
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.handlers[jobType] = handler
}

func lookupHandler(jobType string) (Handler, bool) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	handler, ok := globalRegistry.handlers[jobType]
	return handler, ok
}

func resetRegistryForTest() {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.handlers = map[string]Handler{}
}
