package executor

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"lazymind/core/workflow/attempt"
)

// HostRegistry owns the process-local implementations of the common
// HostExecutor contract. Runtime dispatch selects by the persisted
// controller_host; no model or concrete Host is selected by Workflow logic.
type HostRegistry struct {
	mu        sync.RWMutex
	executors map[string]HostRegistration
}

type HostRegistration struct {
	Executor             HostExecutor
	Capabilities         map[string]bool
	AllowAllCapabilities bool
	AllowLegacyTools     bool
}

var DefaultHostRegistry = NewHostRegistry()

func NewHostRegistry() *HostRegistry {
	return &HostRegistry{executors: map[string]HostRegistration{}}
}

func (r *HostRegistry) Register(host string, executor HostExecutor) {
	r.RegisterHost(host, HostRegistration{Executor: executor})
}

func (r *HostRegistry) RegisterHost(host string, registration HostRegistration) {
	if host == "" || registration.Executor == nil {
		panic("workflow Host registration requires host and executor")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executors[host] = registration
}

func (r *HostRegistry) Hosts() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	hosts := make([]string, 0, len(r.executors))
	for host := range r.executors {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

func (r *HostRegistry) Executor(host string) (HostExecutor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.executors[host]
	return value.Executor, ok
}

func (r *HostRegistry) Supports(host string, capabilities, legacyTools []string) (bool, []string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registration, ok := r.executors[host]
	if !ok {
		return false, append(append([]string{}, capabilities...), legacyTools...)
	}
	missing := []string{}
	for _, capability := range capabilities {
		if !registration.AllowAllCapabilities && !registration.Capabilities[capability] {
			missing = append(missing, capability)
		}
	}
	if !registration.AllowLegacyTools {
		missing = append(missing, legacyTools...)
	}
	return len(missing) == 0, missing
}

type WorkerConfig struct {
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
}

// StartHostWorkers starts one deterministic Supervisor loop per registered
// Host. A Host implementation provides model execution; the shared worker owns
// claim, heartbeat, Artifact callbacks, and terminal convergence.
func StartHostWorkers(ctx context.Context, attempts AttemptService, contexts ContextLoader,
	artifacts ArtifactSink, registry *HostRegistry, config WorkerConfig) {
	interval := config.PollInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	for _, host := range registry.Hosts() {
		executor, ok := registry.Executor(host)
		if !ok {
			continue
		}
		go func(host string, executor HostExecutor) {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				supervisor := Supervisor{Attempts: attempts, Contexts: contexts, Executor: executor,
					Artifacts: artifacts, Config: Config{ExecutorID: host + "-worker", Host: host,
						HeartbeatInterval: config.HeartbeatInterval}}
				_, err := supervisor.ExecuteSync(ctx)
				if err == nil {
					continue
				}
				if !errors.Is(err, attempt.ErrNotClaimable) && ctx.Err() != nil {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}(host, executor)
	}
}
